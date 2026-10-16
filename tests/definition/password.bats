#!/usr/bin/env bats
# Exercises changing a service's passwords against a real docker daemon.
#
# A password is kept by the datastore as well as on disk, and each datastore
# takes a new one its own way, so the only proof a change worked is logging in
# with the new password and being refused with the old one, both straight away
# and once the container has been made again.

load ../test_helper

APP="ci-password-app"
NEW_PASSWORD="changed-password.1234"
NEW_ROOT_PASSWORD="changed-root-password.1234"

# the config variable a link sets by default
ALIAS="$(awk '/^  alias:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")_URL"

setup_file() {
  datastore_setup_file
  fake_dokku_setup "$APP"
  create_service "$SERVICE"
}

teardown_file() {
  # a failed test can leave the app linked, and a linked service is not
  # destroyed, which would leave its container behind
  "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart >/dev/null 2>/dev/null || true
  datastore_teardown_file "$SERVICE"
}

# whether changing a password stops the service: one read as the container is
# made, or written into a file the datastore only reads as it starts
secret_restarts_service() {
  awk '
    /^  secrets:/ { secrets = 1; next }
    secrets && /^  [^ ]/ { secrets = 0 }
    secrets && (/rebuild: true/ || /mode: offline/) { found = 1 }
    END { exit !found }
  ' "$DEFINITION_ROOT/docker-compose.yml"
}

# the file a secret is kept in
secret_file() {
  case "$1" in
  password) echo "$(service_root)/PASSWORD" ;;
  root_password) echo "$(service_root)/ROOTPASSWORD" ;;
  esac
}

# the address the service container has on its network, which a client inside
# it reaches it at the way one beside it would
container_ip() {
  container_inspect "$(service_container)" '{{ range .NetworkSettings.Networks }}{{ .IPAddress }}{{ "\n" }}{{ end }}' | head -n 1
}

# the status of an http request made beside the service, with the arguments
# given to curl
http_status_with() {
  docker container run --rm --network "container:$(service_container)" curlimages/curl:8.16.0 \
    -s -o /dev/null -w '%{http_code}' "$@"
}

# whether the account a secret belongs to is let in with a password. Each
# datastore is asked the way a client would ask it, rather than through the
# credentials on disk, so that it is the datastore saying yes. A datastore that
# only hands the secret to its container is asked what its container was given
logs_in() {
  local secret="$1" password="$2" container database
  container="$(service_container)"
  database="$("$BIN" info "$PLUGIN" "$SERVICE" --database-name 2>/dev/null)"

  case "$PLUGIN:$secret" in
  # over the container's own address rather than localhost, which the image
  # trusts without a password
  postgres:password)
    docker container exec --env "PGPASSWORD=$password" "$container" \
      psql -h "$(container_ip)" -U postgres -w -d postgres -qtAc 'SELECT 1' >/dev/null
    ;;
  # over the container's own address rather than the socket, where root may be
  # let in as the system user it is without a password
  mysql:* | mariadb:*)
    local user="$PLUGIN"
    [[ "$secret" == root_password ]] && user="root"
    docker container exec --env "MYSQL_PWD=$password" "$container" sh -c \
      'if command -v mariadb >/dev/null 2>&1; then exec mariadb "$@"; fi; exec mysql "$@"' \
      _ --protocol=TCP --host="$(container_ip)" --user="$user" -e 'SELECT 1' >/dev/null
    ;;
  mongo:password)
    docker container exec "$container" mongosh --quiet -u "$SERVICE" -p "$password" \
      --authenticationDatabase "$database" "$database" --eval 1 >/dev/null
    ;;
  mongo:root_password)
    docker container exec "$container" mongosh --quiet -u admin -p "$password" \
      --authenticationDatabase admin admin --eval 1 >/dev/null
    ;;
  redis:password)
    [[ "$(docker container exec --env "REDISCLI_AUTH=$password" "$container" redis-cli --no-auth-warning ping)" == PONG ]]
    ;;
  rabbitmq:password)
    docker container exec "$container" rabbitmqctl -q authenticate_user "$SERVICE" "$password" >/dev/null
    ;;
  couchdb:password)
    [[ "$(http_status_with "http://$SERVICE:$password@127.0.0.1:5984/_all_dbs")" == 200 ]]
    ;;
  meilisearch:password)
    [[ "$(http_status_with -H "Authorization: Bearer $password" http://127.0.0.1:7700/keys)" == 200 ]]
    ;;
  typesense:password)
    [[ "$(http_status_with -H "X-TYPESENSE-API-KEY: $password" http://127.0.0.1:8108/collections)" == 200 ]]
    ;;
  clickhouse:password)
    [[ "$(http_status_with -u "$SERVICE:$password" 'http://127.0.0.1:8123/?query=SELECT%201')" == 200 ]]
    ;;
  omnisci:*)
    local user=omnisci
    [[ "$secret" == root_password ]] && user="admin"
    docker container exec "$container" sh -c '
      client=/omnisci/bin/omnisql
      [ -x "$client" ] || client=/omnisci/bin/mapdql
      echo "SELECT 1;" | "$client" --user="$1" -p "$2" -q
    ' _ "$user" "$password" >/dev/null
    ;;
  nats:password)
    container_inspect "$container" '{{ json .Args }}' | grep -qF "\"$password\""
    ;;
  graphite:password)
    container_inspect "$container" '{{ json .Config.Env }}' | grep -qF "\"GRAPHITE_PASSWORD=$password\""
    ;;
  *)
    fail "no way to log in to $PLUGIN with its $secret"
    ;;
  esac
}

# changes a secret and proves the datastore took it: the new password lets the
# account in and the old one no longer does, the file and the fingerprint hold
# the new one, and both still hold once the container is made again
assert_secret_changes() {
  local secret="$1" key="$2" value="$3" before flags=()
  before="$(cat "$(secret_file "$secret")")"
  logs_in "$secret" "$before" || fail "expected the $secret the service was created with to log in"

  secret_restarts_service && flags+=(--allow-restart)
  run --separate-stderr "$BIN" set "${flags[@]}" "$PLUGIN" "$SERVICE" "$key" "$value"
  assert_success
  refute_output --partial "$value"
  refute_stderr --partial "$value"

  run cat "$(secret_file "$secret")"
  assert_success
  assert_output "$value"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" "--$key-fingerprint"
  assert_success
  assert_output "$(printf '%s' "$value" | sha256sum | awk '{ print $1 }')"

  logs_in "$secret" "$value" || fail "expected the new $secret to log in"
  ! logs_in "$secret" "$before" || fail "expected the old $secret to be refused"

  # the datastore keeps the password it was given rather than taking the one
  # its container was first made with again
  rebuild_service
  logs_in "$secret" "$value" || fail "expected the new $secret to log in once the container was made again"
  ! logs_in "$secret" "$before" || fail "expected the old $secret to be refused once the container was made again"
}

@test "($DEFINITION) a password the datastore does not have is refused" {
  declares_secret SERVICE_PASSWORD && skip "$DEFINITION has a password"

  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" password "$NEW_PASSWORD"
  assert_failure
  assert_stderr --partial "has no password"
}

@test "($DEFINITION) a root password the datastore does not have is refused" {
  declares_secret SERVICE_ROOT_PASSWORD && skip "$DEFINITION has a root password"

  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" root-password "$NEW_ROOT_PASSWORD"
  assert_failure
  assert_stderr --partial "has no root-password"
}

@test "($DEFINITION) a password cannot be cleared or hold what a dsn cannot" {
  declares_secret SERVICE_PASSWORD || skip "$DEFINITION has no password"
  local before
  before="$(cat "$(secret_file password)")"

  run --separate-stderr "$BIN" set --allow-restart "$PLUGIN" "$SERVICE" password
  assert_failure
  assert_stderr --partial "cannot be cleared"

  run --separate-stderr "$BIN" set --allow-restart "$PLUGIN" "$SERVICE" password "with@an:at"
  assert_failure
  assert_stderr --partial "may only contain"

  run cat "$(secret_file password)"
  assert_output "$before"
}

@test "($DEFINITION) --no-restart is refused for anything but a password" {
  run --separate-stderr "$BIN" set --no-restart "$PLUGIN" "$SERVICE" wait-timeout 90
  assert_failure
  assert_stderr --partial "--no-restart only applies to a password"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --wait-timeout
  assert_output ""
}

@test "($DEFINITION) a password the service already has changes nothing" {
  declares_secret SERVICE_PASSWORD || skip "$DEFINITION has no password"
  local before service_before
  before="$(cat "$(secret_file password)")"
  service_before="$(service_id)"

  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" password "$before"
  assert_success
  assert_output --partial "password is unchanged"
  [[ "$(service_id)" == "$service_before" ]] || fail "expected a password set again to leave the container alone"
}

@test "($DEFINITION) a password read as the service starts needs --allow-restart" {
  declares_secret SERVICE_PASSWORD || skip "$DEFINITION has no password"
  secret_restarts_service || skip "$DEFINITION changes its password while it runs"
  local before service_before
  before="$(cat "$(secret_file password)")"
  service_before="$(service_id)"

  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" password "$NEW_PASSWORD"
  assert_failure
  assert_stderr --partial -- "--allow-restart"

  run cat "$(secret_file password)"
  assert_output "$before"
  [[ "$(service_id)" == "$service_before" ]] || fail "expected a refused change to leave the container alone"
}

@test "($DEFINITION) a password is not changed on a stopped service it has to be told to" {
  declares_secret SERVICE_PASSWORD || skip "$DEFINITION has no password"
  secret_restarts_service && skip "$DEFINITION starts the service to change its password"
  local before
  before="$(cat "$(secret_file password)")"

  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success

  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" password "$NEW_PASSWORD"
  assert_failure
  assert_stderr --partial "is not running"

  run cat "$(secret_file password)"
  assert_output "$before"

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
}

@test "($DEFINITION) the password is changed in the datastore and in every linked app" {
  declares_secret SERVICE_PASSWORD || skip "$DEFINITION has no password"

  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"
  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success

  assert_secret_changes password password "$NEW_PASSWORD"

  # the app holds the url as it is now, which carries the new password for a
  # datastore whose url carries one at all
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --dsn
  assert_success
  local dsn="$output"

  run jq -r --arg key "$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "$dsn"

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
}

@test "($DEFINITION) the root password is changed in the datastore" {
  declares_secret SERVICE_ROOT_PASSWORD || skip "$DEFINITION has no root password"

  assert_secret_changes root_password root-password "$NEW_ROOT_PASSWORD"

  # the commands that log in as root use the new password from here on
  local probe
  probe="$(probe_path)"
  [[ -x "$probe" ]] || return 0

  run "$probe" write "$SERVICE"
  assert_success

  run --separate-stderr "$probe" read "$SERVICE"
  assert_success
  assert_output "known"
}
