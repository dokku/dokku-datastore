#!/usr/bin/env bats
# A graphite service made on the image's grafana 6 release is upgraded onto the
# version the definition pins and keeps what it had: the metrics carbon wrote,
# grafana's dashboards and datasource, and graphite-web's database, which the
# newer graphite-web migrates rather than starts over.

load ../test_helper

# the last release on grafana 6 and graphite-web 0.9, which every graphite
# service made before this one runs
PREVIOUS_IMAGE_VERSION="6.4.4"

setup_file() {
  datastore_setup_file
  [[ "$DEFINITION" == "graphite" ]] || return 0
  "$BIN" create "$PLUGIN" "$SERVICE" --image "$IMAGE" --image-version "$PREVIOUS_IMAGE_VERSION"

  wait_for_grafana
  "$(probe_path)" write "$SERVICE"
  grafana -X POST -H 'Content-Type: application/json' http://127.0.0.1:80/api/dashboards/db \
    --data-binary '{"overwrite":true,"dashboard":{"title":"Upgrade Probe","panels":[{"id":1,"type":"graph","title":"value","targets":[{"refId":"A","target":"probe.value"}]}]}}'
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

setup() {
  if [[ "$DEFINITION" != "graphite" ]]; then
    skip "$DEFINITION does not run grafana"
  fi
}

# a request to grafana from a client beside the service, as the admin account
# the image's own config names
grafana() {
  docker container run --rm --network "container:$(service_container)" curlimages/curl:8.16.0 \
    -fsS -u admin:admin "$@"
}

# the service is ready once nginx answers, which is before grafana does
wait_for_grafana() {
  for _ in $(seq 1 150); do
    grafana -o /dev/null http://127.0.0.1:80/api/health 2>/dev/null && return 0
    sleep 2
  done

  echo "grafana did not answer" >&2
  return 1
}

@test "($DEFINITION) a bare upgrade moves a grafana 6 service onto the version the definition pins" {
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --image-version
  assert_output "$PREVIOUS_IMAGE_VERSION"

  run "$BIN" upgrade "$PLUGIN" "$SERVICE"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --image-version
  assert_output "$IMAGE_VERSION"

  run wait_for_grafana
  assert_success
  run grafana http://127.0.0.1:80/api/health
  assert_success
  assert_output --partial "\"version\": \"$IMAGE_VERSION\""
}

@test "($DEFINITION) the upgraded service keeps its metrics" {
  run "$(probe_path)" read "$SERVICE"
  assert_success
  assert_output "known"
}

@test "($DEFINITION) the upgraded service keeps grafana's dashboards and datasource" {
  run grafana "http://127.0.0.1:80/api/search?query=Upgrade%20Probe"
  assert_success
  assert_output --partial '"title":"Upgrade Probe"'

  run grafana "http://127.0.0.1:80/api/datasources/name/Local%20Graphite"
  assert_success
}

@test "($DEFINITION) the upgraded service migrates graphite-web's database and keeps its accounts" {
  # admin is the account graphite-web 0.9's database was made with. The newer
  # graphite-web adds a default account of its own on its first request, so
  # admin is looked for rather than taken to be the only one
  # the tags api reads tables graphite-web 0.9 never had, so it only answers
  # once the database has been migrated
  run http_status http://127.0.0.1:81/tags
  assert_output "200"

  run docker container exec "$(service_container)" python3 -c \
    "import sqlite3; print(*[r[0] for r in sqlite3.connect('/opt/graphite/storage/graphite.db').execute(\"select username from auth_user where username = 'admin'\")])"
  assert_success
  assert_output "admin"
}

@test "($DEFINITION) the migrated service starts again as it is" {
  run rebuild_service
  assert_success

  run wait_for_grafana
  assert_success
  run http_status http://127.0.0.1:81/tags
  assert_output "200"
  run "$(probe_path)" read "$SERVICE"
  assert_output "known"
}
