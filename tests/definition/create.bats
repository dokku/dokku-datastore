#!/usr/bin/env bats
# Exercises creating one definition's service against a real docker daemon.
#
# This is the check a unit test cannot make. Everything else about a definition
# is proven without a container: that it parses, that it renders, that the argv
# is right. Whether the container it describes actually starts, answers, and
# gives its data back is only knowable by starting it, and before this job a bad
# definition could only be caught after a release, when a plugin repo picked it up.

load ../test_helper

GIVEN_PASSWORD="givenpassword1234"
GIVEN_ROOT_PASSWORD="givenrootpassword1234"

setup_file() {
  datastore_setup_file

  # given rather than generated, so that everything below also runs against a
  # service whose passwords came from the flags
  local flags
  mapfile -t flags < <(password_flags "$GIVEN_PASSWORD" "$GIVEN_ROOT_PASSWORD")
  "$BIN" create "$PLUGIN" "$SERVICE" --image-version "$IMAGE_VERSION" "${flags[@]}"
}

teardown_file() {
  datastore_teardown_file "$SERVICE" "$SERVICE-unpinned" "$SERVICE-json"
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

@test "($DEFINITION) a create naming a reserved service is refused" {
  local reserved
  reserved="$(reserved_name)"
  [[ -n "$reserved" ]] || skip "$DEFINITION reserves no names"

  # the database is named after the service, so the app would be handed one the
  # datastore keeps for itself. Refused before the pull and before the service
  # root is made, so this costs the daemon nothing
  run --separate-stderr "$BIN" create "$PLUGIN" "$reserved" --image-version "$IMAGE_VERSION"
  assert_failure
  assert_stderr --partial "service name $reserved is reserved"
  [[ ! -d "$(service_root "$reserved")" ]] || fail "a refused create left $(service_root "$reserved") behind"
}

@test "($DEFINITION) a create mounting a host path that does not exist is refused" {
  [[ "$DOKKU_LIB_HOST_ROOT" == "$DOKKU_LIB_ROOT" ]] || skip "dockerd sees another host root, so the host path is not checked"

  # docker would otherwise create it, empty and owned by root, and the service
  # would start on that. Refused before the pull and before the service root is
  # made, so this costs the daemon nothing
  run --separate-stderr "$BIN" create "$PLUGIN" "$SERVICE-nomount" --image-version "$IMAGE_VERSION" --volume "$DOKKU_LIB_ROOT/not-there:/opt/dokku-mount"
  assert_failure
  assert_stderr --partial "does not exist"
  [[ ! -d "$(service_root "$SERVICE-nomount")" ]] || fail "a refused create left $(service_root "$SERVICE-nomount") behind"
}

@test "($DEFINITION) a create with a root password the definition has no secret for is refused" {
  declares_secret SERVICE_ROOT_PASSWORD && skip "$DEFINITION has a root password"

  # the flag would otherwise be dropped and the service started on a password
  # nobody was told. Refused before the pull, so this costs the daemon nothing
  run --separate-stderr "$BIN" create "$PLUGIN" "$SERVICE-noroot" --image-version "$IMAGE_VERSION" --root-password "$GIVEN_ROOT_PASSWORD"
  assert_failure
  assert_stderr --partial -- "--root-password"
  [[ ! -d "$(service_root "$SERVICE-noroot")" ]] || fail "a refused create left $(service_root "$SERVICE-noroot") behind"
}

@test "($DEFINITION) a create with a password the definition has no secret for is refused" {
  declares_secret SERVICE_PASSWORD && skip "$DEFINITION has a password"

  run --separate-stderr "$BIN" create "$PLUGIN" "$SERVICE-nopassword" --image-version "$IMAGE_VERSION" --password "$GIVEN_PASSWORD"
  assert_failure
  assert_stderr --partial -- "--password"
  [[ ! -d "$(service_root "$SERVICE-nopassword")" ]] || fail "a refused create left $(service_root "$SERVICE-nopassword") behind"
}

@test "($DEFINITION) the passwords given at create are the ones the service has" {
  declares_secret SERVICE_PASSWORD || skip "$DEFINITION has no password"

  run cat "$(service_root)/PASSWORD"
  assert_success
  assert_output "$GIVEN_PASSWORD"

  if declares_secret SERVICE_ROOT_PASSWORD; then
    run cat "$(service_root)/ROOTPASSWORD"
    assert_success
    assert_output "$GIVEN_ROOT_PASSWORD"
  fi

  # graphite's connection string carries no password, so there is nothing to
  # find in it there
  if awk '/^  dsn:/ { found = 1 } found && /^  [a-z_]+:/ && !/^  dsn:/ { exit } found' "$DEFINITION_ROOT/docker-compose.yml" | grep -q 'Secret.password'; then
    run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --dsn
    assert_success
    assert_output --partial "$GIVEN_PASSWORD"
  fi
}

@test "($DEFINITION) the datastore accepts the passwords given at create" {
  # the probe logs in with the credentials on disk, so a round trip through it
  # proves the datastore was started with the given passwords rather than only
  # that they were written down
  local probe="$REPO_ROOT/tests/probes/$DEFINITION.sh"
  [[ -x "$probe" ]] || skip "$DEFINITION has no probe"

  run "$probe" write "$SERVICE"
  assert_success

  run --separate-stderr "$probe" read "$SERVICE"
  assert_success
  assert_output "known"
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

@test "($DEFINITION) a create asked for json prints one json document" {
  # what it says on the way, and what the triggers it fires print, go to
  # stderr, so that stdout can be handed straight to a json reader
  run --separate-stderr "$BIN" create "$PLUGIN" "$SERVICE-json" --image-version "$IMAGE_VERSION" --format json
  assert_success

  run jq -e --slurp 'length == 1' <<<"$output"
  assert_success

  run "$BIN" destroy "$PLUGIN" "$SERVICE-json" --force
  assert_success
}

@test "($DEFINITION) info asked to be quiet leaves out its header" {
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE"
  assert_success
  assert_output --partial "=====>"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --quiet
  assert_success
  refute_output --partial "=====>"
  assert_output --partial "$SERVICE"
}

@test "($DEFINITION) a command asked to trace echoes what it runs" {
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --status --trace
  assert_success
  assert_output "running"
  [[ "$stderr" == *"exec: "* ]] || fail "expected the commands run to be echoed on stderr, got '$stderr'"
}

@test "($DEFINITION) a command that only prints text refuses json" {
  run --separate-stderr "$BIN" readme "$PLUGIN" --format json
  assert_failure
  assert_stderr --partial "only prints text"
}

@test "($DEFINITION) list leaves out a service a user-auth-service trigger hides" {
  local plugins="$DOKKU_LIB_ROOT/plugins" bin="$BATS_TEST_TMPDIR/bin"
  mkdir -p "$plugins/enabled/datastore-auth" "$bin"

  # handed the user, the key's name, the datastore and every service, and
  # prints the ones that may be seen. What it says on stderr, the way a traced
  # trigger does, is not a service
  cat >"$plugins/enabled/datastore-auth/user-auth-service" <<EOS
#!/usr/bin/env bash
echo "+ tracing $SERVICE" >&2
for service in "\${@:4}"; do
  [[ "\$service" == "$SERVICE" ]] || echo "\$service"
done
EOS
  chmod +x "$plugins/enabled/datastore-auth/user-auth-service"

  # plugn runs the trigger every enabled plugin has
  cat >"$bin/plugn" <<'EOS'
#!/usr/bin/env bash
shift
trigger="$1"
shift
for script in "$PLUGIN_PATH"/enabled/*/"$trigger"; do
  "$script" "$@" || exit $?
done
EOS
  chmod +x "$bin/plugn"

  run --separate-stderr env PATH="$bin:$PATH" PLUGIN_PATH="$plugins" "$BIN" list "$PLUGIN"
  rm -rf "$plugins/enabled/datastore-auth"
  assert_success
  refute_output --partial "$SERVICE"

  # and with nothing to ask, it is listed
  run --separate-stderr env PATH="$bin:$PATH" PLUGIN_PATH="$plugins" "$BIN" list "$PLUGIN"
  assert_success
  assert_output --partial "$SERVICE"
}

@test "($DEFINITION) destroy leaves nothing behind" {
  run "$BIN" destroy "$PLUGIN" "$SERVICE" --force
  assert_success

  run docker container ls -a --filter "name=^/dokku\\.$PLUGIN\\.$SERVICE$" --format '{{.Names}}'
  assert_success
  assert_output ""

  [[ ! -d "$(service_root)" ]] || fail "destroy left the service root behind"
}
