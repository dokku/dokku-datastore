#!/usr/bin/env bats
# Creates one definition's service through both execution backends and compares
# the containers.
#
# The claim the compose backend rests on is that it produces the same container
# the docker cli does, because both are handed one set of resolved values. This
# is what turns that claim into a check: a difference is a bug in the renderer
# rather than in either backend.

load test_helper

setup_file() {
  datastore_setup_file
  DOKKU_DATASTORE_BACKEND=docker "$BIN" create "$PLUGIN" viadocker --image-version "$IMAGE_VERSION" >/dev/null
  DOKKU_DATASTORE_BACKEND=compose "$BIN" create "$PLUGIN" viacompose --image-version "$IMAGE_VERSION" >/dev/null
}

teardown_file() {
  datastore_teardown_file viadocker viacompose
}

# a generated secret differs between the two by design, and a datastore that
# authenticates on its command line carries it into the container, so the values
# are handed over to be replaced rather than compared
secrets() {
  local service file
  for service in viadocker viacompose; do
    for file in "$(service_root "$service")"/*PASSWORD; do
      [[ -f "$file" ]] && cat "$file" && echo
    done
  done
}

assert_containers_agree() {
  local secret_values=()
  mapfile -t secret_values < <(secrets)

  run "$REPO_ROOT/tests/inspect-diff.sh" "$(service_container viadocker)" "$(service_container viacompose)" "${secret_values[@]}"
  assert_success
}

@test "($DEFINITION) each service records the backend that made it" {
  # the record is what stops a later change of the host default addressing a
  # service the other way, so it has to say what actually made it
  run cat "$(service_root viadocker)/BACKEND"
  assert_output "docker"

  run cat "$(service_root viacompose)/BACKEND"
  assert_output "compose"
}

@test "($DEFINITION) the two containers agree" {
  assert_containers_agree
}

@test "($DEFINITION) the two containers agree on a restart policy of their own" {
  # a retry count rides in docker's own syntax on the command line and in the
  # compose file, and both have to arrive at the same policy
  local service
  for service in viadocker viacompose; do
    run "$BIN" set "$PLUGIN" "$service" restart-policy on-failure:3
    assert_success
    run rebuild_service "$service"
    assert_success
  done

  assert_containers_agree

  run restart_policy_of "$(service_container viacompose)"
  assert_success
  assert_output "on-failure:3"
}
