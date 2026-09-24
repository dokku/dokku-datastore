#!/usr/bin/env bats
# Exercises whether link, unlink and destroy agree on an app being linked.

load ../test_helper

APP="ci-link-app"
UNLINKED_APP="ci-link-unlinked-app"

# the config variable a link sets by default
ALIAS="$(awk '/^  alias:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")_URL"

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
