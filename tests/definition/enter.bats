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

# whether the image has either of the shells enter opens with no command
has_shell() {
  docker container exec "$(service_container)" /bin/bash -c true >/dev/null 2>/dev/null ||
    docker container exec "$(service_container)" /bin/sh -c true >/dev/null 2>/dev/null
}

# enters with no command, piping a script to the shell that opens
enter_with_script() {
  echo 'echo entered' | "$BIN" enter "$PLUGIN" "$SERVICE"
}

@test "($DEFINITION) enter with no command opens a shell the image has" {
  has_shell || skip "the $PLUGIN image has no shell"

  # the shell reads what is piped to it, which is how a missing one would show
  run --separate-stderr enter_with_script
  assert_success
  assert_output "entered"
}

@test "($DEFINITION) enter with no command says when the image has no shell" {
  ! has_shell || skip "the $PLUGIN image has a shell"

  run --separate-stderr "$BIN" enter "$PLUGIN" "$SERVICE"
  assert_failure
  [[ "$stderr" == *"has no shell, so enter needs a command to run"* ]] || fail "unexpected stderr: $stderr"
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
