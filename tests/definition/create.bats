#!/usr/bin/env bats
# Exercises creating one definition's service against a real docker daemon.
#
# This is the check a unit test cannot make. Everything else about a definition
# is proven without a container: that it parses, that it renders, that the argv
# is right. Whether the container it describes actually starts, answers, and
# gives its data back is only knowable by starting it, and before this job a bad
# definition could only be caught after a release, when a plugin repo picked it up.

load ../test_helper

setup_file() {
  datastore_setup_file
  create_service "$SERVICE"
}

teardown_file() {
  datastore_teardown_file "$SERVICE" "$SERVICE-unpinned"
}

@test "($DEFINITION) a create naming an image with no version is refused" {
  # the version this definition pins belongs to the image it pins, so there is
  # nothing to fall back to for another repository. Pasting it on anyway named a
  # tag nobody ever built, and the create then failed saying that image could not
  # be had. Nothing reaches docker here: the refusal lands before the pull and
  # before the service root is made, so this costs the daemon nothing
  run --separate-stderr "$BIN" create "$PLUGIN" "$SERVICE-noversion" --image example.invalid/not-the-definition-image
  assert_failure
  assert_stderr --partial -- "--image-version"
  assert_stderr --partial "example.invalid/not-the-definition-image"
  [[ ! -d "$(service_root "$SERVICE-noversion")" ]] || fail "a refused create left $(service_root "$SERVICE-noversion") behind"
}

@test "($DEFINITION) the service is running and reports a connection string" {
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --status
  assert_success
  assert_output "running"

  local scheme
  scheme="$(awk '/^  scheme:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")"
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --dsn
  assert_success
  [[ "$output" == "$scheme://"?* ]] || fail "expected a $scheme connection string, got '$output'"
}

@test "($DEFINITION) the service records the definition it was created with" {
  run cat "$(service_root)/DEFINITION"
  assert_success
  assert_output "$DEFINITION"
}

@test "($DEFINITION) info reports the state the service was created with" {
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --definition
  assert_success
  assert_output "$DEFINITION"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --image-version
  assert_success
  assert_output "$IMAGE_VERSION"
}

@test "($DEFINITION) info reports every service when none is named" {
  run --separate-stderr "$BIN" info "$PLUGIN"
  assert_success
  assert_output --partial "$SERVICE"
}

@test "($DEFINITION) the rendered compose file is valid" {
  local compose
  compose="$(service_root)/docker-compose.yml"
  assert [ -f "$compose" ]

  run docker compose --file "$compose" config --quiet
  assert_success
}

@test "($DEFINITION) the files holding secrets are unreadable by other users" {
  # the compose file carries the service's password in the clear, and the custom
  # environment and config options can carry credentials of their own
  assert_mode 640 "$(service_root)/docker-compose.yml"
  assert_mode 640 "$(service_root)/ENV"
  assert_mode 640 "$(service_root)/CONFIG_OPTIONS"
}

@test "($DEFINITION) a create with no version still records one" {
  # only that both halves are there, not which version they name: with no version
  # given, a datastore split by major version lands on its newest definition
  # rather than on the one this run is for
  run "$BIN" create "$PLUGIN" "$SERVICE-unpinned"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE-unpinned" --image
  assert_success
  assert [ -n "$output" ]

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE-unpinned" --image-version
  assert_success
  assert [ -n "$output" ]

  run "$BIN" destroy "$PLUGIN" "$SERVICE-unpinned" --force
  assert_success
}

@test "($DEFINITION) destroy leaves nothing behind" {
  run "$BIN" destroy "$PLUGIN" "$SERVICE" --force
  assert_success

  run docker container ls -a --filter "name=^/dokku\\.$PLUGIN\\.$SERVICE$" --format '{{.Names}}'
  assert_success
  assert_output ""

  [[ ! -d "$(service_root)" ]] || fail "destroy left the service root behind"
}
