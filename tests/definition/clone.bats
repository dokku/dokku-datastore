#!/usr/bin/env bats
# Exercises what a clone of a service is made with.

load ../test_helper

COPY="$SERVICE-copy"
OVERRIDE="$SERVICE-override"
MOUNT_TARGET="/opt/dokku-mount"

setup_file() {
  datastore_setup_file

  # every setting given a value the defaults would not, so that finding it on
  # the clone means it was copied rather than defaulted. Log settings are left
  # out: a max-size is refused by a daemon that logs any way but json-file or
  # local, and the unit tests already cover copying them
  "$BIN" create "$PLUGIN" "$SERVICE" --image-version "$IMAGE_VERSION" \
    --memory 512 --shm-size 128m --restart unless-stopped --custom-env FOO=bar \
    --volume "$(mount_source clone):$MOUNT_TARGET:ro"
  "$BIN" set "$PLUGIN" "$SERVICE" backup-keyserver keys.example.com
}

teardown_file() {
  datastore_teardown_file "$COPY" "$OVERRIDE" "$SERVICE"
}

# a clone copies the data through an export and an import, so a datastore
# without them has no clone to check
clone_or_skip() {
  run "$BIN" clone "$PLUGIN" "$SERVICE" "$@"
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement clone"
  fi
  assert_success
}

@test "($DEFINITION) a volume given at create is mounted from the start" {
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --mounts
  assert_success
  assert_output "$(mount_source clone):$MOUNT_TARGET:ro"

  run mount_of "$(service_container)" "$MOUNT_TARGET"
  assert_success
  assert_output "$(mount_source clone):false"
}

@test "($DEFINITION) a clone starts from the source's settings" {
  # the bug this closes: a clone made without repeating every flag landed on the
  # defaults rather than on what the source runs with
  clone_or_skip "$COPY"

  local key expected
  for key in memory shm-size custom-env restart-policy mounts backup-keyserver; do
    run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" "--$key"
    assert_success
    expected="$output"

    run --separate-stderr "$BIN" info "$PLUGIN" "$COPY" "--$key"
    assert_success
    assert_output "$expected"
  done

  run container_inspect "$(service_container "$COPY")" '{{ .HostConfig.RestartPolicy.Name }}'
  assert_success
  assert_output "unless-stopped"

  run mount_of "$(service_container "$COPY")" "$MOUNT_TARGET"
  assert_success
  assert_output "$(mount_source clone):false"
}

@test "($DEFINITION) a flag passed to clone overrides that one setting" {
  clone_or_skip "$OVERRIDE" --restart no --custom-env "" --volume ""

  run --separate-stderr "$BIN" info "$PLUGIN" "$OVERRIDE" --restart-policy
  assert_success
  assert_output "no"

  # given empty, which clears what the source has rather than keeping it
  run --separate-stderr "$BIN" info "$PLUGIN" "$OVERRIDE" --custom-env
  assert_success
  assert_output ""

  run container_inspect "$(service_container "$OVERRIDE")" '{{ .HostConfig.RestartPolicy.Name }}'
  assert_success
  assert_output "no"

  # --volume given empty drops the mounts the source has
  run --separate-stderr "$BIN" info "$PLUGIN" "$OVERRIDE" --mounts
  assert_success
  assert_output ""

  run mount_of "$(service_container "$OVERRIDE")" "$MOUNT_TARGET"
  assert_success
  assert_output ""

  # and what no flag was given for is still the source's
  run --separate-stderr "$BIN" info "$PLUGIN" "$OVERRIDE" --memory
  assert_success
  assert_output "512"

  run --separate-stderr "$BIN" info "$PLUGIN" "$OVERRIDE" --backup-keyserver
  assert_success
  assert_output "keys.example.com"
}
