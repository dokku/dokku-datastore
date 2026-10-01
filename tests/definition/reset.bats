#!/usr/bin/env bats
# Exercises reset: the data goes, and the service, its credentials and the apps
# it is linked to stay as they were.

load ../test_helper

APP="ci-reset-app"

setup_file() {
  datastore_setup_file
  fake_dokku_setup "$APP"
  create_service "$SERVICE"

  # linked for the whole file, since keeping the links is the point of a reset
  # rather than a destroy and a create
  "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart >/dev/null
}

teardown_file() {
  # a linked service is not destroyed, which would leave its container behind
  "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart >/dev/null 2>/dev/null || true
  datastore_teardown_file "$SERVICE"
}

# resets the service without asking, skipping the test for a datastore that has
# no reset to run
reset_service() {
  run "$BIN" reset "$PLUGIN" "$SERVICE" --force
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement reset"
  fi
  assert_success
}

# the credentials the service was created with, one file to a line
secrets_of() {
  local file
  for file in PASSWORD ROOTPASSWORD; do
    if [[ -f "$(service_root)/$file" ]]; then
      echo "$file=$(cat "$(service_root)/$file")"
    fi
  done
}

@test "($DEFINITION) a datastore that keeps no data does not implement reset" {
  case "$PLUGIN" in
  nats | pushpin) ;;
  *) skip "$PLUGIN keeps data" ;;
  esac

  run "$BIN" reset "$PLUGIN" "$SERVICE" --force
  assert_equal "$status" "$NOT_IMPLEMENTED_EXIT"
}

@test "($DEFINITION) reset of a service that does not exist fails" {
  run "$BIN" reset "$PLUGIN" "ci-reset-missing" --force
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement reset"
  fi
  assert_failure
  assert_output --partial "service ci-reset-missing does not exist"
}

@test "($DEFINITION) reset asks for the service name and does nothing without it" {
  local probe
  probe="$(probe_path)"
  [[ -x "$probe" ]] || skip "$DEFINITION has no probe"

  run "$probe" write "$SERVICE"
  assert_success

  # the variable that answers for an operator is cleared, so the question is
  # asked here whatever the environment the tests run in says
  run env -u DOKKU_APPS_FORCE_DELETE "$BIN" reset "$PLUGIN" "$SERVICE" <<<"not-$SERVICE"
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement reset"
  fi
  assert_failure

  run --separate-stderr "$probe" read "$SERVICE"
  assert_success
  assert_output "known"
}

@test "($DEFINITION) reset deletes the data and keeps the service, its credentials and its links" {
  local probe
  probe="$(probe_path)"
  [[ -x "$probe" ]] || skip "$DEFINITION has no probe"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --dsn
  assert_success
  local dsn="$output"

  local secrets config
  secrets="$(secrets_of)"
  config="$(cat "$FAKE_CONFIG_ROOT/$APP.json")"

  run "$probe" write "$SERVICE"
  assert_success

  # answered with the service's name rather than --force, which is the other
  # way past the question
  run env -u DOKKU_APPS_FORCE_DELETE "$BIN" reset "$PLUGIN" "$SERVICE" <<<"$SERVICE"
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement reset"
  fi
  assert_success

  # not asserted to succeed: reading a record from a datastore that no longer
  # has the table, index or queue it was in fails for some and prints nothing
  # for others, and either is a record that is gone
  run --separate-stderr "$probe" read "$SERVICE"
  refute_output "known"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --status
  assert_success
  assert_output "running"

  # the service takes writes as it did before, with the credentials it had
  run "$probe" write "$SERVICE"
  assert_success

  run --separate-stderr "$probe" read "$SERVICE"
  assert_success
  assert_output "known"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --dsn
  assert_success
  assert_output "$dsn"

  assert_equal "$(secrets_of)" "$secrets"
  assert_equal "$(cat "$FAKE_CONFIG_ROOT/$APP.json")" "$config"

  run --separate-stderr "$BIN" links "$PLUGIN" "$SERVICE"
  assert_success
  assert_line "$APP"
}

@test "($DEFINITION) reset twice in a row leaves an empty service" {
  # a reset of a service that is already empty has nothing to delete, which is
  # not a failure
  run "$BIN" reset "$PLUGIN" "$SERVICE" --force
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement reset"
  fi
  assert_success

  reset_service

  local probe
  probe="$(probe_path)"
  [[ -x "$probe" ]] || return 0

  run --separate-stderr "$probe" read "$SERVICE"
  refute_output "known"
}

@test "($DEFINITION) reset flushes memcached" {
  [[ "$PLUGIN" == "memcached" ]] || skip "$PLUGIN is not memcached"

  # memcached has no probe, since a cache is not expected to keep what it holds
  # across a restart, so the item is set and read here
  memcached() {
    printf '%s\r\nquit\r\n' "$1" |
      docker container run -i --rm --network "container:$(service_container)" busybox:1.37.0-uclibc nc -w 2 127.0.0.1 11211 |
      tr -d '\r'
  }

  run memcached $'set probe 0 0 5\r\nknown'
  assert_success
  assert_output "STORED"

  run memcached "get probe"
  assert_success
  assert_output --partial "known"

  reset_service

  run memcached "get probe"
  assert_success
  assert_output "END"
}

@test "($DEFINITION) reset keeps the extensions a postgres database had" {
  local extension
  case "$DEFINITION" in
  postgres-pgvector-*) extension=vector ;;
  postgres-postgis-*) extension=postgis ;;
  postgres-timescaledb-*) extension=timescaledb ;;
  *) skip "$DEFINITION is not a postgres flavor" ;;
  esac

  run --separate-stderr "$(probe_path)" extension "$SERVICE" "$extension"
  assert_success
  assert_output "$extension"

  reset_service

  run --separate-stderr "$(probe_path)" has-extension "$SERVICE" "$extension"
  assert_success
  assert_output "$extension"

  # a timescaledb whose catalog did not come back whole would refuse this
  if [[ "$extension" == "timescaledb" ]]; then
    run --separate-stderr "$(probe_path)" hypertable "$SERVICE"
    assert_success
    assert_output "probe_metrics"
  fi
}
