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

@test "($DEFINITION) a create naming an invalid service is refused with the characters it may use" {
  # the dash is accepted, so the message has to say so, or someone with a
  # refused name is told a name like this file's own service is not allowed
  run --separate-stderr "$BIN" create "$PLUGIN" "not.valid" --image-version "$IMAGE_VERSION"
  assert_failure
  assert_stderr --partial "Valid characters are: [A-Za-z0-9_-]+"
  [[ ! -d "$(service_root "not.valid")" ]] || fail "a refused create left $(service_root "not.valid") behind"
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

@test "($DEFINITION) the database is named after the service with its hyphens replaced" {
  # some datastores refuse a hyphen in a database name, so the one a service
  # records has it replaced, and that is the name every template is handed. The
  # service this file creates has a hyphen in its name for exactly this reason
  [[ "$SERVICE" == *-* ]] || skip "$SERVICE has no hyphen to replace"

  run cat "$(service_root)/DATABASE_NAME"
  assert_success
  assert_output "${SERVICE//-/_}"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --database-name
  assert_success
  assert_output "${SERVICE//-/_}"
}

@test "($DEFINITION) a service with no recorded database name is given one" {
  # the way get_database_name did in the bash plugins: the service name as it
  # is, since that is the database a service made before the name was recorded
  # was created with
  local recorded
  recorded="$(cat "$(service_root)/DATABASE_NAME")"
  rm -f "$(service_root)/DATABASE_NAME"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --database-name
  assert_success
  assert_output "$SERVICE"

  run cat "$(service_root)/DATABASE_NAME"
  assert_success
  assert_output "$SERVICE"

  # and back to the database this service was actually created with
  echo "$recorded" >"$(service_root)/DATABASE_NAME"
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
