#!/usr/bin/env bats
# enter opens a prompt in the service container, or runs whatever follows the
# service name there instead. What follows is the container's to read, flags
# included, and bats has no terminal, which is how enter is run from cron.

load ../test_helper

setup_file() {
  datastore_setup_file
  create_service "$SERVICE"
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

# an image built from scratch has nothing to run a command with
skip_unless_echo() {
  if ! docker container exec "$(service_container)" echo >/dev/null 2>/dev/null; then
    skip "the $PLUGIN image has no echo"
  fi
}

@test "($DEFINITION) enter runs a command with its flags in the container" {
  skip_unless_echo

  run --separate-stderr "$BIN" enter "$PLUGIN" "$SERVICE" echo --backup -u root
  assert_success
  assert_output "--backup -u root"
}

@test "($DEFINITION) enter drops the -- fencing off a command" {
  skip_unless_echo

  run --separate-stderr "$BIN" enter "$PLUGIN" "$SERVICE" -- echo --backup
  assert_success
  assert_output "--backup"
}

@test "($DEFINITION) a command that fails in the container fails enter" {
  skip_unless_echo

  run --separate-stderr "$BIN" enter "$PLUGIN" "$SERVICE" sh -c "exit 3"
  assert_failure
}
