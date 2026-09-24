#!/usr/bin/env bats
# The triggers dokku fires before an app starts, builds, releases or is restored
# bring the services it is linked to up first. Each of these takes the service
# down, and the trigger is what has to put it back.

load ../test_helper

APP="ci-triggers-app"
UNLINKED_APP="ci-triggers-unlinked-app"

setup_file() {
  datastore_setup_file
  fake_dokku_setup "$APP" "$UNLINKED_APP"
  create_service "$SERVICE"
  "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart

  RECORDED_IMAGE="$("$BIN" info "$PLUGIN" "$SERVICE" --image)"
  RECORDED_VERSION="$("$BIN" info "$PLUGIN" "$SERVICE" --image-version)"
  export RECORDED_IMAGE RECORDED_VERSION
}

teardown_file() {
  "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart >/dev/null 2>/dev/null || true
  datastore_teardown_file "$SERVICE"
}

setup() {
  RECORDED="$RECORDED_IMAGE:$RECORDED_VERSION"
  PULL_VARIABLE="$(echo "$PLUGIN" | tr '[:lower:]' '[:upper:]')_DISABLE_PULL"
}

# stops the service, which removes its container
stop_service() {
  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success
}

# removes the recorded image, as a host restored from a backup would not have
# it. Skipped rather than forced if anything else on the host still holds it
remove_recorded_image() {
  if ! docker image rm --force "$RECORDED" >/dev/null 2>/dev/null || docker image inspect "$RECORDED" >/dev/null 2>/dev/null; then
    run "$BIN" start "$PLUGIN" "$SERVICE"
    assert_success
    skip "$RECORDED is still in use and cannot be removed"
  fi
}

assert_service_running() {
  run container_inspect "$(service_container)" '{{ .State.Status }}'
  assert_success
  assert_output "running"
}

@test "($DEFINITION) pre-start starts a stopped linked service" {
  stop_service

  run "$BIN" trigger-pre-start "$PLUGIN" "$APP"
  assert_success
  assert_service_running
}

@test "($DEFINITION) pre-build starts a stopped linked service" {
  stop_service

  run "$BIN" trigger-pre-build "$PLUGIN" herokuish "$APP" "$BATS_TEST_TMPDIR"
  assert_success
  assert_service_running
}

@test "($DEFINITION) pre-release-builder starts a stopped linked service" {
  stop_service

  run "$BIN" trigger-pre-release-builder "$PLUGIN" herokuish "$APP" "dokku/$APP:latest"
  assert_success
  assert_service_running
}

@test "($DEFINITION) pre-build recreates a linked service whose image is gone" {
  stop_service
  remove_recorded_image

  run "$BIN" trigger-pre-build "$PLUGIN" herokuish "$APP" "$BATS_TEST_TMPDIR"
  assert_success
  assert_service_running

  run docker image inspect "$RECORDED"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --version
  assert_success
  assert_output "$RECORDED"
}

@test "($DEFINITION) pre-build stops the build when a linked service cannot start" {
  stop_service
  remove_recorded_image

  run --separate-stderr env "$PULL_VARIABLE=true" "$BIN" trigger-pre-build "$PLUGIN" herokuish "$APP" "$BATS_TEST_TMPDIR"
  assert_failure
  assert_stderr --partial "$SERVICE linked to app $APP"
  assert_stderr --partial "docker image pull $RECORDED"

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
}

@test "($DEFINITION) pre-build leaves the services of an app it is not linked to alone" {
  stop_service

  run "$BIN" trigger-pre-build "$PLUGIN" herokuish "$UNLINKED_APP" "$BATS_TEST_TMPDIR"
  assert_success

  run docker container inspect "$(service_container)"
  assert_failure

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
}

@test "($DEFINITION) pre-restore starts every linked service without naming an app" {
  stop_service

  run "$BIN" trigger-pre-restore "$PLUGIN"
  assert_success
  assert_service_running
}

@test "($DEFINITION) pre-restore warns rather than fails when a linked service cannot start" {
  stop_service
  remove_recorded_image

  run --separate-stderr env "$PULL_VARIABLE=true" "$BIN" trigger-pre-restore "$PLUGIN"
  assert_success
  assert_stderr --partial "unable to start $PLUGIN service $SERVICE"

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
}
