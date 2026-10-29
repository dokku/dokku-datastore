#!/usr/bin/env bats
# Exercises whether link, unlink and destroy agree on an app being linked.

load ../test_helper

APP="ci-link-app"
UNLINKED_APP="ci-link-unlinked-app"
OTHER_APP="ci-link-other-app"

# the probe the plugin waits on a service with, which the tests reach the
# service from an app's container with
WAIT_IMAGE="$(awk -F'"' '/WaitImage *=/ { print $2; exit }' "$REPO_ROOT/internal/hostenv/hostenv.go")"

# the config variable a link sets by default
ALIAS="$(awk '/^  alias:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")_URL"

# whether the definition hands over jdbc values with link --jdbc
HAS_JDBC="$(awk '/^  jdbc:/ { print "true"; exit }' "$DEFINITION_ROOT/docker-compose.yml")"

setup_file() {
  datastore_setup_file
  fake_dokku_setup "$APP" "$UNLINKED_APP" "$OTHER_APP"
  create_service "$SERVICE"
}

teardown() {
  remove_app_containers

  # a failed test can leave the app linked, which every later test would find,
  # and a linked service is not destroyed, which would leave its container
  # behind
  "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart >/dev/null 2>/dev/null || true
  "$BIN" unlink "$PLUGIN" "$SERVICE" "$OTHER_APP" --no-restart >/dev/null 2>/dev/null || true
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

# the network the link between the service and an app runs over
link_network() {
  echo "dokku.$PLUGIN.$SERVICE.${1:-$APP}"
}

# the host the service url names, which the service answers to on every
# network its links run over
service_host() {
  local host="dokku.$PLUGIN.$SERVICE"
  host="${host//./-}"
  echo "${host//_/-}"
}

# the port the plugin waits on the service at. The tests reach the service on
# it rather than on the port its url names, which for graphite is statsd over
# udp, where there is no connection to make.
service_wait_port() {
  local name
  name="$(awk '/^  wait:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")"
  awk -v name="$name" '
    $1 == "-" && $2 == "name:" { found = ($3 == name); next }
    found && $1 == "target:" { print $2; exit }
  ' "$DEFINITION_ROOT/docker-compose.yml"
}

# starts a stand-in for one of an app's containers, labeled the way dokku
# labels every container it makes for an app
start_app_container() {
  docker container run -d --name "$1" --label "com.dokku.app-name=$2" busybox:1.37.0-uclibc sleep 300 >/dev/null
}

# removes the stand-ins the tests made
remove_app_containers() {
  docker container rm -f ci-link-app-web ci-link-app-run ci-link-other-app-web >/dev/null 2>/dev/null || true
}

# the networks a container is on, one per line
container_networks() {
  docker container inspect --format '{{ range $name, $_ := .NetworkSettings.Networks }}{{ $name }}{{ println }}{{ end }}' "$1"
}

# whether a container reaches the service by the host its url names
reaches_service() {
  docker container run --rm --network "container:$1" "$WAIT_IMAGE" -c "$(service_host):$(service_wait_port)" -t 15
}

@test "($DEFINITION) unlink of an app that was never linked fails and changes nothing" {
  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$UNLINKED_APP" --no-restart
  assert_failure
  assert_output --partial "Not linked to app $UNLINKED_APP"

  run "$BIN" links "$PLUGIN" "$SERVICE"
  assert_success
  refute_output --partial "$UNLINKED_APP"
}

@test "($DEFINITION) link sets the default alias when a key merely containing it exists" {
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"
  dokku config:set --no-restart "$APP" "EXTERNAL_$ALIAS=something"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --dsn
  assert_success
  local dsn="$output"

  run jq -r --arg key "$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "$dsn"

  run jq -r --arg key "EXTERNAL_$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "something"

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
}

@test "($DEFINITION) link --alias takes the default alias when a key merely containing it exists" {
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"
  dokku config:set --no-restart "$APP" "OTHER_$ALIAS=something"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --alias "${ALIAS%_URL}"
  assert_success
  refute_output --partial "already in use"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --dsn
  assert_success
  local dsn="$output"

  run jq -r --arg key "$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "$dsn"

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
}

@test "($DEFINITION) link finishes a link whose url was set by hand" {
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --dsn
  assert_success
  dokku config:set --no-restart "$APP" "$ALIAS=$output"

  run "$BIN" linked "$PLUGIN" "$SERVICE" "$APP"
  assert_failure

  # the bug this closes: the app was refused as already linked, and was left
  # without its container link and off the list of linked apps
  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
  assert_output --partial "none was set"

  run "$BIN" linked "$PLUGIN" "$SERVICE" "$APP"
  assert_success

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_failure
  assert_output --partial "Already linked as $ALIAS"

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
}

@test "($DEFINITION) link --env-var sets the variable without a _URL suffix" {
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --env-var MB_DB_CONNECTION_URI
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --dsn
  assert_success
  local dsn="$output"

  run jq -r '.MB_DB_CONNECTION_URI' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "$dsn"

  run jq -r --arg key "$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "null"

  # linking again finds the variable it set, rather than adding the default
  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_failure
  assert_output --partial "Already linked as MB_DB_CONNECTION_URI"

  run jq -r --arg key "$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "null"

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success

  run jq -r '.MB_DB_CONNECTION_URI' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "null"
}

@test "($DEFINITION) link refuses --alias with --env-var" {
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --alias BLUE --env-var MB_DB_CONNECTION_URI
  assert_failure
  assert_output --partial "--alias and --env-var cannot be used together"

  run "$BIN" linked "$PLUGIN" "$SERVICE" "$APP"
  assert_failure
}

@test "($DEFINITION) unlink removes a linked variable after its scheme changes" {
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --dsn
  assert_success

  # the bug this closes: unlink looked for the exact url, and left the
  # variable behind once its scheme no longer matched
  dokku config:set --no-restart "$APP" "$ALIAS=changed://${output#*://}"

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
  refute_output --partial "none was unset"

  run jq -r --arg key "$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "null"
}

@test "($DEFINITION) link --jdbc sets the jdbc values, and unlink unsets them" {
  [[ "$HAS_JDBC" == "true" ]] || skip "the definition hands over no jdbc values"
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --jdbc
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --dsn
  assert_success
  local dsn="$output"

  # the dsn is scheme://user:password@host:port/database, and the jdbc url
  # names the same host, port, database and credentials
  local credentials="${dsn#*://}"
  credentials="${credentials%%@*}"
  local user="${credentials%%:*}"
  local password="${credentials#*:}"

  run jq -r --arg key "$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "$dsn"

  run jq -r --arg key "JDBC_$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output --regexp "^jdbc:[a-z]+://"
  assert_output --partial "://${dsn#*@}?"
  assert_output --partial "user=$user&password=$password"

  run jq -r --arg key "JDBC_${ALIAS%_URL}_USERNAME" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "$user"

  run jq -r --arg key "JDBC_${ALIAS%_URL}_PASSWORD" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "$password"

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
  refute_output --partial "none was unset"

  run jq -r 'keys | join(" ")' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output ""
}

@test "($DEFINITION) link --jdbc refuses a jdbc variable already in use" {
  [[ "$HAS_JDBC" == "true" ]] || skip "the definition hands over no jdbc values"
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"
  dokku config:set --no-restart "$APP" "JDBC_$ALIAS=something"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --jdbc
  assert_failure
  assert_output --partial "Specified jdbc env var JDBC_$ALIAS already in use"

  run "$BIN" linked "$PLUGIN" "$SERVICE" "$APP"
  assert_failure

  run jq -r 'keys | join(" ")' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "JDBC_$ALIAS"
}

@test "($DEFINITION) link --jdbc is refused by a datastore with no jdbc driver" {
  [[ "$HAS_JDBC" != "true" ]] || skip "the definition hands over jdbc values"
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --jdbc
  assert_failure
  assert_output --partial "does not support jdbc urls"

  run "$BIN" linked "$PLUGIN" "$SERVICE" "$APP"
  assert_failure

  run jq -r 'keys | join(" ")' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output ""
}

@test "($DEFINITION) link puts the app on a network of its own with the service" {
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"
  start_app_container ci-link-app-web "$APP"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success

  # internal, so joining it changes no container's route to anything else
  run docker network inspect --format '{{ .Internal }}' "$(link_network)"
  assert_success
  assert_output "true"

  # the service answers to the host its url names
  run docker container inspect --format "{{ json (index .NetworkSettings.Networks \"$(link_network)\").Aliases }}" "$(service_container)"
  assert_success
  assert_output --partial "\"$(service_host)\""

  # a container already running joins it without a restart, and reaches the
  # service the way the app's url says to
  run container_networks ci-link-app-web
  assert_success
  assert_line "$(link_network)"

  run reaches_service ci-link-app-web
  assert_success

  # no container link is given, and one an earlier version gave is removed
  run grep -F "docker-options:add $APP" "$FAKE_CONFIG_ROOT/docker-options.log"
  assert_failure
  run grep -F -- "docker-options:remove $APP build,deploy,run --link $(service_container):$(service_host)" "$FAKE_CONFIG_ROOT/docker-options.log"
  assert_success

  # unlinking takes the service away straight away, even with no restart
  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success

  run docker network inspect "$(link_network)"
  assert_failure

  run container_networks ci-link-app-web
  assert_success
  refute_line "$(link_network)"
}

@test "($DEFINITION) a container made for a linked app joins its link through post-container-create" {
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"
  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success

  docker container create --name ci-link-app-run --label "com.dokku.app-name=$APP" busybox:1.37.0-uclibc sleep 300 >/dev/null

  # a container dokku makes for anything but the app is left alone
  run "$BIN" trigger-post-container-create "$PLUGIN" service "$(docker container inspect --format '{{ .Id }}' ci-link-app-run)" "$APP" run
  assert_success
  run container_networks ci-link-app-run
  refute_line "$(link_network)"

  run "$BIN" trigger-post-container-create "$PLUGIN" app "$(docker container inspect --format '{{ .Id }}' ci-link-app-run)" "$APP" run
  assert_success

  docker container start ci-link-app-run >/dev/null
  run reaches_service ci-link-app-run
  assert_success

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
}

@test "($DEFINITION) two apps linked to one service cannot find each other through it" {
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"
  echo '{}' >"$FAKE_CONFIG_ROOT/$OTHER_APP.json"
  start_app_container ci-link-app-web "$APP"
  start_app_container ci-link-other-app-web "$OTHER_APP"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
  run "$BIN" link "$PLUGIN" "$SERVICE" "$OTHER_APP" --no-restart
  assert_success

  run docker network inspect "$(link_network "$OTHER_APP")"
  assert_success

  # both reach the service, and neither resolves the other
  run reaches_service ci-link-app-web
  assert_success
  run reaches_service ci-link-other-app-web
  assert_success

  run docker container exec ci-link-app-web ping -c 1 -W 2 ci-link-other-app-web
  assert_failure
  run docker container exec ci-link-other-app-web ping -c 1 -W 2 ci-link-app-web
  assert_failure

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$OTHER_APP" --no-restart
  assert_success
}

@test "($DEFINITION) a service container made again rejoins the networks of its links" {
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"
  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success

  # stop removes the container, and start makes a new one
  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success
  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success

  run docker container inspect --format "{{ json (index .NetworkSettings.Networks \"$(link_network)\").Aliases }}" "$(service_container)"
  assert_success
  assert_output --partial "\"$(service_host)\""

  start_app_container ci-link-app-web "$APP"
  run "$BIN" trigger-post-container-create "$PLUGIN" app "$(docker container inspect --format '{{ .Id }}' ci-link-app-web)" "$APP" deploy web
  assert_success
  run reaches_service ci-link-app-web
  assert_success

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
}

@test "($DEFINITION) unlink and destroy agree once the app's url is repointed" {
  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success

  run jq -r --arg key "$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  refute_output "null"

  # the bug this closes: an app pointed at another datastore was still linked
  # according to destroy, but not according to unlink
  dokku config:set --no-restart "$APP" "$ALIAS=scheme://user:pass@elsewhere:1234/db"

  run "$BIN" destroy "$PLUGIN" "$SERVICE" --force
  assert_failure
  assert_output --partial "Cannot delete linked service"
  assert_output --partial "Linked to app(s): $APP"

  run "$BIN" linked "$PLUGIN" "$SERVICE" "$APP"
  assert_success

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
  assert_output --partial "none was unset"

  # the url the app was repointed at is not the service's, so it is left alone
  run jq -r --arg key "$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "scheme://user:pass@elsewhere:1234/db"

  run "$BIN" linked "$PLUGIN" "$SERVICE" "$APP"
  assert_failure

  # a network left behind for an app that is no longer linked goes with the
  # service, as nothing else would ever remove it
  docker network create --internal --label dokku=service-link --label "dokku.service=$PLUGIN" \
    --label "dokku.service.name=$SERVICE" "$(link_network ci-link-gone-app)" >/dev/null

  run "$BIN" destroy "$PLUGIN" "$SERVICE" --force
  assert_success

  run docker network inspect "$(link_network ci-link-gone-app)"
  assert_failure
}
