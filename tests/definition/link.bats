#!/usr/bin/env bats
# Exercises whether link, unlink and destroy agree on an app being linked.

load ../test_helper

APP="ci-link-app"
UNLINKED_APP="ci-link-unlinked-app"

# the config variable a link sets by default
ALIAS="$(awk '/^  alias:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")_URL"

# whether the definition hands over jdbc values with link --jdbc
HAS_JDBC="$(awk '/^  jdbc:/ { print "true"; exit }' "$DEFINITION_ROOT/docker-compose.yml")"

setup_file() {
  datastore_setup_file
  fake_dokku_setup "$APP" "$UNLINKED_APP"
  create_service "$SERVICE"
}

teardown_file() {
  # a failed test can leave the app linked, and a linked service is not
  # destroyed, which would leave its container behind
  "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart >/dev/null 2>/dev/null || true
  datastore_teardown_file "$SERVICE"
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

  run "$BIN" destroy "$PLUGIN" "$SERVICE" --force
  assert_success
}
