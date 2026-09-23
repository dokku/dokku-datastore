#!/usr/bin/env bats
# The version a service runs is what it recorded, and only an upgrade changes
# that. Several of these deliberately take the service down, and each puts it
# back before it ends.

load ../test_helper

setup_file() {
  datastore_setup_file
  create_service "$SERVICE"

  RECORDED_IMAGE="$("$BIN" info "$PLUGIN" "$SERVICE" --image)"
  RECORDED_VERSION="$("$BIN" info "$PLUGIN" "$SERVICE" --image-version)"
  export RECORDED_IMAGE RECORDED_VERSION
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

setup() {
  RECORDED="$RECORDED_IMAGE:$RECORDED_VERSION"
  PULL_VARIABLE="$(echo "$PLUGIN" | tr '[:lower:]' '[:upper:]')_DISABLE_PULL"
}

@test "($DEFINITION) start brings the service back on the version it recorded" {
  run rebuild_service
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --version
  assert_success
  assert_output "$RECORDED"
}

@test "($DEFINITION) start records the image of a service that never did" {
  run "$BIN" pause "$PLUGIN" "$SERVICE"
  assert_success
  rm -f "$(service_root)/IMAGE" "$(service_root)/IMAGE_VERSION"

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success

  run cat "$(service_root)/IMAGE"
  assert_output "$RECORDED_IMAGE"
  run cat "$(service_root)/IMAGE_VERSION"
  assert_output "$RECORDED_VERSION"
}

@test "($DEFINITION) a pause and a start is still a restart" {
  local before after
  before="$(container_inspect "$(service_container)" '{{ .Id }}')"

  run "$BIN" pause "$PLUGIN" "$SERVICE"
  assert_success
  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success

  after="$(container_inspect "$(service_container)" '{{ .Id }}')"
  [[ "$before" == "$after" ]] || fail "start recreated the container instead of starting the one it had"
}

@test "($DEFINITION) the record wins over a container that disagrees with it" {
  run "$BIN" pause "$PLUGIN" "$SERVICE"
  assert_success
  echo "0.0.0-nonexistent" >"$(service_root)/IMAGE_VERSION"

  run --separate-stderr "$BIN" start "$PLUGIN" "$SERVICE"
  assert_failure
  assert_stderr --partial "0.0.0-nonexistent"

  # the image is fetched before the stale container is taken away, so a start that
  # cannot get that far leaves the service with the container it already had
  run docker container inspect "$(service_container)"
  assert_success

  echo "$RECORDED_VERSION" >"$(service_root)/IMAGE_VERSION"
  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
}

@test "($DEFINITION) start refuses a service it cannot place" {
  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success
  rm -f "$(service_root)/IMAGE" "$(service_root)/IMAGE_VERSION"

  run --separate-stderr "$BIN" start "$PLUGIN" "$SERVICE"
  assert_failure
  assert_stderr --partial "upgrade"

  echo "$RECORDED_IMAGE" >"$(service_root)/IMAGE"
  echo "$RECORDED_VERSION" >"$(service_root)/IMAGE_VERSION"
  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
}

@test "($DEFINITION) start fetches the version the service recorded" {
  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success

  # the container is gone by now, so the image can go too. Skipped rather than
  # forced if anything else on the host still holds it.
  if ! docker image rm --force "$RECORDED" >/dev/null 2>/dev/null || docker image inspect "$RECORDED" >/dev/null 2>/dev/null; then
    run "$BIN" start "$PLUGIN" "$SERVICE"
    assert_success
    skip "$RECORDED is still in use and cannot be removed"
  fi

  # start explains an image it is not allowed to fetch
  run --separate-stderr env "$PULL_VARIABLE=true" "$BIN" start "$PLUGIN" "$SERVICE"
  assert_failure
  assert_stderr --partial "docker image pull $RECORDED"

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --version
  assert_success
  assert_output "$RECORDED"
}

@test "($DEFINITION) start fetches a sidecar image the host no longer has" {
  # The images the plugin runs beside a service are pulled once, when the plugin
  # is installed, and then only ever run - so a host pruned since has none of
  # them and nothing used to fetch them back. The readiness probe is the one to
  # prove it on, because it runs on every start and the start path is where a
  # missing image is felt.
  #
  # read from the source rather than repeated here so a bump cannot leave this
  # asserting on a version nothing runs
  local wait_image
  wait_image="$(awk -F'"' '/WaitImage = / { print $2; exit }' "$REPO_ROOT/internal/hostenv/hostenv.go")"

  # Removed without --force on purpose. A non-forced removal fails while a
  # container still holds the image, so a second run probing on this daemon makes
  # this skip rather than pull the image out from under it. A probe that has not
  # started yet is not covered, and does not need to be: the refusal below is
  # scoped to one invocation with env rather than exported, so the worst another
  # run sees is one extra pull.
  if ! docker image rm "$wait_image" >/dev/null 2>/dev/null || docker image inspect "$wait_image" >/dev/null 2>/dev/null; then
    skip "$wait_image is still in use and cannot be removed"
  fi

  # start explains a sidecar image it is not allowed to fetch
  run --separate-stderr env "$PULL_VARIABLE=true" "$BIN" start "$PLUGIN" "$SERVICE"
  assert_failure
  assert_stderr --partial "docker image pull $wait_image"

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success

  run docker image inspect "$wait_image"
  assert_success
}

@test "($DEFINITION) start thaws a frozen container rather than replacing it" {
  # docker refuses to start a container it froze, and nothing in the plugin ever
  # freezes one - its own pause is a stop - so this is the state a hand-run
  # docker pause leaves behind, and start used to see straight past it and try to
  # build a second container of the same name
  local frozen thawed
  frozen="$(container_inspect "$(service_container)" '{{ .Id }}')"
  docker container pause "$(service_container)" >/dev/null

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success

  thawed="$(container_inspect "$(service_container)" '{{ .Id }}')"
  [[ "$frozen" == "$thawed" ]] || fail "start replaced the frozen container instead of thawing it"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --status
  assert_success
  assert_output "running"
}

@test "($DEFINITION) a bare upgrade stays on the definition the service was created with" {
  # the service was created on the version this definition pins, so a bare upgrade
  # lands on the one it is already running. What is being checked is that it did
  # not reach for a newer definition to get there
  run "$BIN" upgrade "$PLUGIN" "$SERVICE"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --image-version
  assert_success
  assert_output "$RECORDED_VERSION"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --definition
  assert_success
  assert_output "$DEFINITION"
}
