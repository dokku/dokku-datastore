#!/usr/bin/env bats
# A graphite service made on the image's grafana 6 release is upgraded onto the
# version the definition pins and keeps what it had: the metrics carbon wrote,
# grafana's dashboards and the datasource made on grafana 6, and graphite-web's
# database, which the
# newer graphite-web migrates rather than starts over.

load ../test_helper

# the last release on grafana 6 and graphite-web 0.9, which every graphite
# service made before this one runs
PREVIOUS_IMAGE_VERSION="6.4.4"

setup_file() {
  datastore_setup_file
  [[ "$DEFINITION" == "graphite" ]] || return 0
  "$BIN" create --backend "$BACKEND" "$PLUGIN" "$SERVICE" --image "$IMAGE" --image-version "$PREVIOUS_IMAGE_VERSION"

  wait_for_grafana
  "$(probe_path)" write "$SERVICE"
  grafana -X POST -H 'Content-Type: application/json' http://127.0.0.1:80/api/dashboards/db \
    --data-binary '{"overwrite":true,"dashboard":{"title":"Upgrade Probe","panels":[{"id":1,"type":"graph","title":"value","targets":[{"refId":"A","target":"probe.value"}]}]}}'

  # the image adds the datasource on every start, so the one grafana 6 has is
  # marked to tell it from one added again after the upgrade. Grafana 6 gives
  # a datasource no uid to tell them apart by
  local datasource id
  datasource="$(wait_for_datasource)"
  id="$(echo "$datasource" | grep -o '"id":[0-9]*' | head -n 1 | cut -d ':' -f 2)"
  grafana -X PUT -H 'Content-Type: application/json' "http://127.0.0.1:80/api/datasources/$id" \
    --data-binary '{"name":"Local Graphite","type":"graphite","url":"http://localhost:8000","access":"proxy","isDefault":true,"jsonData":{"upgradeProbe":true}}'
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

setup() {
  if [[ "$DEFINITION" != "graphite" ]]; then
    skip "$DEFINITION does not run grafana"
  fi
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

  run wait_for_datasource
  assert_success
  assert_output --partial '"upgradeProbe":true'
}

@test "($DEFINITION) the upgraded service has the dashboards grafana 6 never read" {
  # grafana 6 ran from a homepath of its own, so the dashboards mounted for
  # grafana 13 first appear once the service is upgraded
  for uid in dokku-graphite dokku-statsd; do
    # provisioning runs as grafana starts, which can be after it first answers
    for _ in $(seq 1 30); do
      run grafana "http://127.0.0.1:80/api/dashboards/uid/$uid"
      [[ "$status" -eq 0 ]] && break
      sleep 2
    done
    assert_success
    assert_output --partial '"folderTitle":"Dokku"'
  done
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
