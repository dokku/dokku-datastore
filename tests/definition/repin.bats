#!/usr/bin/env bats
# Before postgres had definitions for fourteen, fifteen and sixteen, a service
# on one of them was pinned to the newest definition, which mounts its data
# where postgres 18 keeps it. These pin a service the same way and check that
# it is put back on the definition for its own major without its data being
# touched, and that a service whose container agrees with its pin keeps it.

load ../test_helper

NEWEST_PINNED="$SERVICE-newest"

setup_file() {
  datastore_setup_file

  # the newest of postgres's own definitions, which every such service was
  # pinned to. Only an older one of those has a pin to put back
  NEWEST="$(default_definition)"
  export NEWEST
  if [[ "$PLUGIN" != "postgres" || ! "$DEFINITION" =~ ^postgres-[0-9]+$ || "$DEFINITION" == "$NEWEST" ]]; then
    return 0
  fi

  create_service "$SERVICE"
  "$(probe_path)" write "$SERVICE"
}

teardown_file() {
  # install run as root makes the parents of the folders it chowns as root,
  # which this run could not remove otherwise
  if [[ "$EUID" -ne 0 ]] && [[ -n "$DOKKU_LIB_ROOT" ]] && sudo -n true 2>/dev/null; then
    sudo -n chown -R "$(id -u):$(id -g)" "$DOKKU_LIB_ROOT" || true
  fi

  datastore_teardown_file "$SERVICE" "$NEWEST_PINNED"
}

setup() {
  if [[ "$PLUGIN" != "postgres" || ! "$DEFINITION" =~ ^postgres-[0-9]+$ || "$DEFINITION" == "$NEWEST" ]]; then
    skip "$DEFINITION is not an older postgres definition a service could have been pinned away from"
  fi
}

# pins the service to the newest definition the way an earlier release did,
# leaving the container it runs as it was
misplace_pin() {
  echo "$NEWEST" >"$(service_root "${1:-$SERVICE}")/DEFINITION"
}

# install is a root step, as dokku runs it: it manages /etc/sudoers.d and
# /usr/local/bin. The environment is kept, so it works on this run's data root
# and writes everything as the user the run owns it as
install_plugin() {
  if [[ "$EUID" -eq 0 ]]; then
    "$BIN" trigger-install "$PLUGIN"
  else
    sudo -n -E "$BIN" trigger-install "$PLUGIN"
  fi
}

# skips a test that installs the plugin where it cannot be run as root
needs_root() {
  if [[ "$EUID" -ne 0 ]] && ! sudo -n true 2>/dev/null; then
    skip "install needs root"
  fi
}

# the directories an upgrade kept the old data in, one per line
previous_data() {
  find "$(service_root)" -mindepth 1 -maxdepth 1 -type d -name 'data.*' -exec basename {} \;
}

@test "($DEFINITION) install puts back a pin its container contradicts" {
  needs_root
  misplace_pin

  run install_plugin
  assert_success
  assert_output --partial "pinning it to $DEFINITION"

  run cat "$(service_root)/DEFINITION"
  assert_output "$DEFINITION"

  run --separate-stderr "$(probe_path)" read "$SERVICE"
  assert_success
  assert_output "known"
}

@test "($DEFINITION) upgrade --no-migrate puts back a pin without touching the data" {
  misplace_pin

  run --separate-stderr "$BIN" upgrade "$PLUGIN" "$SERVICE" --definition "$DEFINITION" --no-migrate
  assert_success
  refute_output --partial "Migrating the data"
  refute_output --partial "Importing the data"

  run cat "$(service_root)/DEFINITION"
  assert_output "$DEFINITION"

  # the container was made again on the definition, and found the data where
  # it always was
  run --separate-stderr "$(probe_path)" read "$SERVICE"
  assert_success
  assert_output "known"

  run previous_data
  assert_output ""
}

@test "($DEFINITION) upgrade --no-migrate is refused without a definition" {
  run --separate-stderr "$BIN" upgrade "$PLUGIN" "$SERVICE" --no-migrate
  assert_failure
  assert_stderr --partial "--definition"

  run cat "$(service_root)/DEFINITION"
  assert_output "$DEFINITION"
}

@test "($DEFINITION) install keeps a pin its container agrees with" {
  needs_root

  # this major placed on the newest definition from the start, so its server
  # made its cluster where that definition mounts the data
  run "$BIN" create "$PLUGIN" "$NEWEST_PINNED" --definition "$NEWEST" --image "$IMAGE" --image-version "$IMAGE_VERSION"
  assert_success
  run "$(probe_path)" write "$NEWEST_PINNED"
  assert_success

  run install_plugin
  assert_success
  refute_output --partial "Service $NEWEST_PINNED was pinned"

  run cat "$(service_root "$NEWEST_PINNED")/DEFINITION"
  assert_output "$NEWEST"

  run --separate-stderr "$(probe_path)" read "$NEWEST_PINNED"
  assert_success
  assert_output "known"
}
