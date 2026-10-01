#!/usr/bin/env bats
# Exercises an upgrade that carries a service's data onto the next major
# version: the data survives, the old data is kept aside until it is removed,
# and nothing is touched unless the linked apps are stopped for it.

load ../test_helper

setup_file() {
  datastore_setup_file

  # the definition one major up, where this one has one that migrates the data
  # of a service moved onto it. postgres-17 moves to postgres-18 and each pg17
  # flavor to its pg18 counterpart
  NEXT_DEFINITION=""
  if [[ "$DEFINITION" == *17 ]]; then
    NEXT_DEFINITION="${DEFINITION%17}18"
  fi
  if [[ -n "$NEXT_DEFINITION" ]] && ! grep -q '^    migrate: true' "$REPO_ROOT/internal/registry/definitions/$NEXT_DEFINITION/docker-compose.yml" 2>/dev/null; then
    NEXT_DEFINITION=""
  fi
  export NEXT_DEFINITION

  if [[ -n "$NEXT_DEFINITION" ]]; then
    create_service "$SERVICE"
  fi
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

setup() {
  [[ -n "$NEXT_DEFINITION" ]] || skip "$DEFINITION has no next major that migrates its data"
}

# the directories an upgrade kept the old data in, one per line
previous_data() {
  find "$(service_root)" -mindepth 1 -maxdepth 1 -type d -name 'data.*' -exec basename {} \;
}

# the extension a postgres flavor's image exists to ship, or nothing for the
# official image
flavor_extension() {
  case "$DEFINITION" in
  postgres-pgvector-*) echo vector ;;
  postgres-postgis-*) echo postgis ;;
  postgres-timescaledb-*) echo timescaledb ;;
  esac
}

@test "($DEFINITION) an upgrade that migrates the data needs the linked apps stopped" {
  run "$(probe_path)" write "$SERVICE"
  assert_success

  run --separate-stderr "$BIN" upgrade "$PLUGIN" "$SERVICE" --definition "$NEXT_DEFINITION"
  assert_failure
  assert_stderr --partial "--restart-apps"

  # refused before anything was touched, so the service runs on as it was
  run cat "$(service_root)/DEFINITION"
  assert_output "$DEFINITION"

  run --separate-stderr "$(probe_path)" read "$SERVICE"
  assert_success
  assert_output "known"

  run previous_data
  assert_output ""
}

@test "($DEFINITION) an upgrade onto the next major carries the data across" {
  local extension
  extension="$(flavor_extension)"
  if [[ -n "$extension" ]]; then
    run --separate-stderr "$(probe_path)" extension "$SERVICE" "$extension"
    assert_success
    assert_output "$extension"
  fi

  run "$(probe_path)" write "$SERVICE"
  assert_success

  run --separate-stderr "$BIN" upgrade "$PLUGIN" "$SERVICE" --definition "$NEXT_DEFINITION" --restart-apps
  assert_success

  # the official image migrates in place, and a step that fell back to an
  # export would say so
  if [[ -z "$extension" ]]; then
    assert_output --partial "in place"
    refute_output --partial "exporting and importing it instead"
    refute_stderr --partial "exporting and importing it instead"
  else
    assert_output --partial "Importing the data"
  fi

  run cat "$(service_root)/DEFINITION"
  assert_output "$NEXT_DEFINITION"

  run --separate-stderr "$(probe_path)" read "$SERVICE"
  assert_success
  assert_output "known"

  if [[ -n "$extension" ]]; then
    run --separate-stderr "$(probe_path)" has-extension "$SERVICE" "$extension"
    assert_success
    assert_output "$extension"
  fi

  run previous_data
  assert_output --regexp "^data\.$DEFINITION\.[0-9]{8}T[0-9]{6}$"
}

@test "($DEFINITION) upgrade-cleanup removes the data an upgrade kept aside" {
  [[ -n "$(previous_data)" ]] || skip "no upgrade kept data aside"

  run "$BIN" invoke "$PLUGIN" upgrade-cleanup "$SERVICE"
  assert_success

  run previous_data
  assert_output ""

  # and only that: the service still has the data it runs on
  run --separate-stderr "$(probe_path)" read "$SERVICE"
  assert_success
  assert_output "known"
}

@test "($DEFINITION) destroy removes data an upgrade kept aside" {
  # kept aside the way an upgrade leaves it, owned by the user the datastore
  # writes as rather than by dokku
  run docker container run --rm --volume "$(host_service_root):/service" "$IMAGE:$IMAGE_VERSION" \
    sh -c "mkdir -p /service/data.$DEFINITION.20261001T000000/base && chown -R 999:999 /service/data.$DEFINITION.20261001T000000 && chmod 700 /service/data.$DEFINITION.20261001T000000"
  assert_success

  run "$BIN" destroy "$PLUGIN" "$SERVICE" --force
  assert_success

  [[ ! -e "$(service_root)" ]] || fail "destroy left $(service_root) behind"
}
