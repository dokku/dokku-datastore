#!/usr/bin/env bats
# Exercises moving where a service's volumes are mounted in its container.

load ../test_helper

FLAG="$SERVICE-flag"
ENVIRONMENT="$SERVICE-env"
COPY="$SERVICE-flag-copy"
REDIS="$SERVICE-redis"
MOVED="/dokku-moved"

setup_file() {
  datastore_setup_file
  create_service "$SERVICE"
}

teardown_file() {
  datastore_teardown_file "$COPY" "$FLAG" "$ENVIRONMENT" "$REDIS" "$SERVICE"
}

# every test starts from a service with nothing moved and nothing mounted, so a
# test that fails part way does not hand the next one what it left behind
setup() {
  "$BIN" set "$PLUGIN" "$SERVICE" volume-targets >/dev/null
  "$BIN" unmount "$PLUGIN" "$SERVICE" --all >/dev/null
}

# a definition that mounts nothing has nothing to move
volume_key_or_skip() {
  VOLUME_KEY="$(volume_key)"
  [[ -n "$VOLUME_KEY" ]] || skip "$DEFINITION mounts no volumes"
  DEFAULT_TARGET="$(default_target_of "$VOLUME_KEY")"
}

@test "($DEFINITION) a moved volume reaches the container it is rebuilt with" {
  volume_key_or_skip

  # a record written before the move, which has to be there again once the
  # volume is back, since moving it moves nothing on the host
  local probe
  probe="$(probe_path)"
  if [[ -x "$probe" && "$VOLUME_KEY" == "data" ]]; then
    run "$probe" write "$SERVICE"
    assert_success
  fi

  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" volume-targets "$VOLUME_KEY=$MOVED"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --volume-targets
  assert_success
  assert_output "$VOLUME_KEY=$MOVED"

  # nothing reaches the running container: like a mount, it is read when a
  # container is made
  run mount_of "$(service_container)" "$MOVED"
  assert_success
  assert_output ""

  # the image may well keep writing to its own path while the volume is
  # elsewhere, so only where things are mounted is checked here
  run rebuild_service
  assert_success

  run mount_of "$(service_container)" "$MOVED"
  assert_success
  assert_output "$(host_service_root)/$VOLUME_KEY:true"

  # the service's own directory is no longer mounted where the definition puts
  # it. Something else may be: an image declaring the path a VOLUME gets an
  # anonymous volume of docker's own there
  run mount_of "$(service_container)" "$DEFAULT_TARGET"
  assert_success
  refute_output "$(host_service_root)/$VOLUME_KEY:true"

  # and back to where the definition mounts it
  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" volume-targets
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --volume-targets
  assert_success
  assert_output ""

  run rebuild_service
  assert_success

  run mount_of "$(service_container)" "$DEFAULT_TARGET"
  assert_success
  assert_output "$(host_service_root)/$VOLUME_KEY:true"

  run mount_of "$(service_container)" "$MOVED"
  assert_success
  assert_output ""

  if [[ -x "$probe" && "$VOLUME_KEY" == "data" ]]; then
    run --separate-stderr "$probe" read "$SERVICE"
    assert_success
    assert_output "known"
  fi
}

@test "($DEFINITION) a volume target docker could not mount is refused first" {
  volume_key_or_skip

  local value
  for value in "not-a-volume=$MOVED" "$VOLUME_KEY=relative" "$VOLUME_KEY=/" "$VOLUME_KEY" "$VOLUME_KEY=$MOVED:ro"; do
    run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" volume-targets "$value"
    assert_failure
    assert_stderr --partial "volume"
  done

  # onto another of the definition's volumes, where docker would mount two
  # things at one path
  local other
  other="$(volume_targets | awk -v key="$VOLUME_KEY" '$1 != key { print $2; exit }')"
  if [[ -n "$other" ]]; then
    run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" volume-targets "$VOLUME_KEY=$other"
    assert_failure
    assert_stderr --partial "would both be mounted at $other"
  fi

  # onto a directory holding a script the definition mounts into the container
  local payload
  if [[ -d "$DEFINITION_ROOT/rootfs" ]]; then
    payload="$(cd "$DEFINITION_ROOT/rootfs" && find . -type f | sort | head -n 1)"
    payload="/${payload#./}"
    run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" volume-targets "$VOLUME_KEY=$(dirname "$payload")"
    assert_failure
    assert_stderr --partial "which holds the $payload"
  fi

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --volume-targets
  assert_success
  assert_output ""
}

@test "($DEFINITION) a mount and a moved volume are kept apart" {
  volume_key_or_skip

  local source
  source="$(mount_source volume-targets)"

  # a volume cannot be moved onto a path a mount holds
  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source:$MOVED"
  assert_success

  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" volume-targets "$VOLUME_KEY=$MOVED"
  assert_failure
  assert_stderr --partial "Container path $MOVED is already mounted by the $PLUGIN definition"

  run --separate-stderr "$BIN" unmount "$PLUGIN" "$SERVICE" "$source:$MOVED"
  assert_success

  # nor a mount made at the path a volume was moved to, while the path it left
  # is free for one
  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" volume-targets "$VOLUME_KEY=$MOVED"
  assert_success

  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source:$MOVED"
  assert_failure
  assert_stderr --partial "Container path $MOVED is already mounted by the $PLUGIN definition"

  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source:$DEFAULT_TARGET"
  assert_success

  run --separate-stderr "$BIN" unmount "$PLUGIN" "$SERVICE" --all
  assert_success

  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" volume-targets
  assert_success
}

@test "($DEFINITION) a volume target given at create is mounted from the start" {
  volume_key_or_skip

  run "$BIN" create "$PLUGIN" "$FLAG" --image "$IMAGE" --image-version "$IMAGE_VERSION" --volume-target "$VOLUME_KEY=$MOVED"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$FLAG" --volume-targets
  assert_success
  assert_output "$VOLUME_KEY=$MOVED"

  run mount_of "$(service_container "$FLAG")" "$MOVED"
  assert_success
  assert_output "$(host_service_root "$FLAG")/$VOLUME_KEY:true"

  # and read from the environment when the flag is not given
  run env "$(plugin_variable)_VOLUME_TARGETS=$VOLUME_KEY=$MOVED" "$BIN" create "$PLUGIN" "$ENVIRONMENT" --image "$IMAGE" --image-version "$IMAGE_VERSION"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$ENVIRONMENT" --volume-targets
  assert_success
  assert_output "$VOLUME_KEY=$MOVED"

  run mount_of "$(service_container "$ENVIRONMENT")" "$MOVED"
  assert_success
  assert_output "$(host_service_root "$ENVIRONMENT")/$VOLUME_KEY:true"
}

# whether the definition tells its datastore where the volume the tests move is
# mounted, so that it keeps its data there rather than at the image's own path.
# A clone copies the data through an export, which only finds it where the
# datastore keeps it
follows_a_move() {
  case "$DEFINITION" in
  redis | typesense) return 0 ;;
  esac
  return 1
}

@test "($DEFINITION) a clone keeps where the source's volumes are mounted" {
  volume_key_or_skip
  [[ -d "$(service_root "$FLAG")" ]] || skip "the service created with a volume target is missing"
  follows_a_move || skip "$DEFINITION keeps its data at the image's own path wherever the volume is mounted"

  run "$BIN" clone "$PLUGIN" "$FLAG" "$COPY"
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement clone"
  fi
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$COPY" --volume-targets
  assert_success
  assert_output "$VOLUME_KEY=$MOVED"

  run mount_of "$(service_container "$COPY")" "$MOVED"
  assert_success
  assert_output "$(host_service_root "$COPY")/$VOLUME_KEY:true"
}

@test "($DEFINITION) an upgrade keeps where the service's volumes are mounted" {
  volume_key_or_skip
  [[ -d "$(service_root "$FLAG")" ]] || skip "the service created with a volume target is missing"

  # a setting the upgrade is asked to change, so that the container is made
  # again on the image it already runs
  run "$BIN" upgrade "$PLUGIN" "$FLAG" --wait-timeout 90
  assert_success

  run mount_of "$(service_container "$FLAG")" "$MOVED"
  assert_success
  assert_output "$(host_service_root "$FLAG")/$VOLUME_KEY:true"

  # and one asked to put it back does
  run "$BIN" upgrade "$PLUGIN" "$FLAG" --volume-target ""
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$FLAG" --volume-targets
  assert_success
  assert_output ""

  run mount_of "$(service_container "$FLAG")" "$(default_target_of "$VOLUME_KEY")"
  assert_success
  assert_output "$(host_service_root "$FLAG")/$VOLUME_KEY:true"
}

# the case the issue asked about: an image that keeps its data somewhere else.
# Redis is told where its data is mounted, so it writes its dump there and the
# dump verbs read and write the same file
@test "($DEFINITION) redis keeps its data in a moved data volume" {
  [[ "$DEFINITION" == "redis" ]] || skip "only redis is told where its data volume is"

  local dump="$BATS_TEST_TMPDIR/dump"
  run "$BIN" create "$PLUGIN" "$REDIS" --image "$IMAGE" --image-version "$IMAGE_VERSION" --volume-target data=/redis-data
  assert_success

  # started in the data volume, which is where the image's entrypoint hands
  # ownership to the redis user and where redis writes its dump
  run container_inspect "$(service_container "$REDIS")" '{{ .Config.WorkingDir }}'
  assert_success
  assert_output "/redis-data"

  run "$(probe_path)" write "$REDIS"
  assert_success

  "$BIN" export "$PLUGIN" "$REDIS" >"$dump"
  [[ -s "$dump" ]] || fail "export produced nothing"

  # the dump the export asked for landed in the volume, on the host
  if [[ "$DOKKU_LIB_HOST_ROOT" == "$DOKKU_LIB_ROOT" ]]; then
    [[ -f "$(service_root "$REDIS")/data/dump.rdb" ]] || fail "expected the dump in the data volume"
  fi

  run "$(probe_path)" clobber "$REDIS"
  assert_success

  run "$BIN" import "$PLUGIN" "$REDIS" <"$dump"
  assert_success

  run --separate-stderr "$(probe_path)" read "$REDIS"
  assert_success
  assert_output "known"

  # and it is still there once the container is made again
  run rebuild_service "$REDIS"
  assert_success

  run --separate-stderr "$(probe_path)" read "$REDIS"
  assert_success
  assert_output "known"
}
