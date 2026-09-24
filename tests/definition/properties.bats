#!/usr/bin/env bats
# Exercises the properties a service is set with, and that they reach the
# container it is rebuilt with.

load ../test_helper

setup_file() {
  datastore_setup_file
  create_service "$SERVICE"
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

# only json-file and local take a max-size, so a daemon logging any other way
# would refuse one and there is nothing to check
skip_unless_log_is_capped() {
  local daemon_driver
  daemon_driver="$(docker system info --format '{{ .LoggingDriver }}')"
  if [[ "$daemon_driver" != "json-file" && "$daemon_driver" != "local" ]]; then
    skip "the daemon logs with $daemon_driver, which takes no max-size"
  fi
}

@test "($DEFINITION) a property set on the service is read back by info" {
  # the keyserver is the property to set here: it needs no network to exist and
  # is only ever read when a backup runs, so setting it changes nothing else
  run "$BIN" set "$PLUGIN" "$SERVICE" backup-keyserver keys.example.com
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-keyserver
  assert_success
  assert_output "keys.example.com"

  # the whole report has to carry it too, since that is what a machine reads
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --format json
  assert_success
  assert_output --partial '"backup-keyserver":"keys.example.com"'

  run "$BIN" set "$PLUGIN" "$SERVICE" backup-keyserver
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-keyserver
  assert_success
  assert_output ""
}

@test "($DEFINITION) the container log is bounded" {
  # the bug this closes: a container was made with nothing to say how large its log
  # was allowed to get, and on the default driver it grew until the host ran out of
  # room
  skip_unless_log_is_capped

  # no dokku is installed, so there is no global to inherit and the built-in
  # default is what a service lands on
  run container_inspect "$(service_container)" '{{ index .HostConfig.LogConfig.Config "max-size" }}'
  assert_success
  assert_output "10m"
}

@test "($DEFINITION) a log setting reaches the container it is rebuilt with" {
  skip_unless_log_is_capped

  run "$BIN" set "$PLUGIN" "$SERVICE" log-opt max-size=15m,max-file=3
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --log-opt
  assert_success
  assert_output "max-size=15m,max-file=3"

  run rebuild_service
  assert_success

  run container_inspect "$(service_container)" '{{ .HostConfig.LogConfig.Config }}'
  assert_success
  assert_output --partial "max-size:15m"
  assert_output --partial "max-file:3"

  # and back to what every other check expects of this service
  run "$BIN" set "$PLUGIN" "$SERVICE" log-opt
  assert_success
  run rebuild_service
  assert_success
}

@test "($DEFINITION) unlimited is how a service opts out" {
  # what is asserted is that the plugin stops asking for a cap, not that the
  # container ends up with none: a daemon configured with log-opts of its own
  # still applies them, which is docker's business rather than this plugin's
  skip_unless_log_is_capped

  # a cap only this plugin would ask for, so that finding it gone means the
  # plugin stopped asking rather than that the daemon never had one
  run "$BIN" set "$PLUGIN" "$SERVICE" log-opt max-size=15m
  assert_success
  run rebuild_service
  assert_success

  run "$BIN" set "$PLUGIN" "$SERVICE" log-opt max-size=unlimited
  assert_success
  run rebuild_service
  assert_success

  run container_inspect "$(service_container)" '{{ .HostConfig.LogConfig.Config }}'
  assert_success
  refute_output --partial "max-size:15m"

  # and back to what every other check expects of this service
  run "$BIN" set "$PLUGIN" "$SERVICE" log-opt
  assert_success
  run rebuild_service
  assert_success
}

@test "($DEFINITION) a log option docker would refuse is refused first" {
  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" log-opt max-size=20
  assert_failure
  assert_stderr --partial "max-size"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --log-opt
  assert_success
  assert_output ""
}

@test "($DEFINITION) a restart policy reaches the container it is rebuilt with" {
  run container_inspect "$(service_container)" '{{ .HostConfig.RestartPolicy.Name }}'
  assert_success
  assert_output "always"

  run "$BIN" set "$PLUGIN" "$SERVICE" restart-policy unless-stopped
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --restart-policy
  assert_success
  assert_output "unless-stopped"

  # nothing reaches the running container: like a log setting, it is read when a
  # container is made
  run container_inspect "$(service_container)" '{{ .HostConfig.RestartPolicy.Name }}'
  assert_success
  assert_output "always"

  run rebuild_service
  assert_success

  run container_inspect "$(service_container)" '{{ .HostConfig.RestartPolicy.Name }}'
  assert_success
  assert_output "unless-stopped"

  # and back to what every other check expects of this service
  run "$BIN" set "$PLUGIN" "$SERVICE" restart-policy
  assert_success
  run rebuild_service
  assert_success

  run container_inspect "$(service_container)" '{{ .HostConfig.RestartPolicy.Name }}'
  assert_success
  assert_output "always"
}

@test "($DEFINITION) a restart policy docker would refuse is refused first" {
  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" restart-policy on-failure:abc
  assert_failure
  assert_stderr --partial "restart-policy"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --restart-policy
  assert_success
  assert_output ""
}

@test "($DEFINITION) a mount reaches the container it is rebuilt with" {
  local source target="/opt/dokku-mount"
  source="$(mount_source properties)"

  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source:$target:ro"
  assert_success
  assert_output --partial "mounted at $target"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --mounts
  assert_success
  assert_output "$source:$target:ro"

  # nothing reaches the running container: like a restart policy, it is read
  # when a container is made
  run mount_of "$(service_container)" "$target"
  assert_success
  assert_output ""

  run rebuild_service
  assert_success

  run mount_of "$(service_container)" "$target"
  assert_success
  assert_output "$source:false"

  # the same mount again rewrites its options rather than being refused
  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source:$target"
  assert_success
  assert_output --partial "updated"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --mounts
  assert_success
  assert_output "$source:$target"

  # and it goes away the same way it arrived
  run --separate-stderr "$BIN" unmount "$PLUGIN" "$SERVICE" "$source:$target"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --mounts
  assert_success
  assert_output ""

  run rebuild_service
  assert_success

  run mount_of "$(service_container)" "$target"
  assert_success
  assert_output ""
}

@test "($DEFINITION) mount --replace swaps every mount and unmount --all removes them" {
  local first second
  first="$(mount_source first)"
  second="$(mount_source second)"

  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$first:/opt/first"
  assert_success

  run --separate-stderr "$BIN" mount --replace "$PLUGIN" "$SERVICE" "$second:/opt/second:ro" "$first:/opt/other"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --mounts
  assert_success
  assert_output "$second:/opt/second:ro $first:/opt/other"

  # an empty replacement is refused rather than read as removing everything
  run --separate-stderr "$BIN" mount --replace "$PLUGIN" "$SERVICE"
  assert_failure
  assert_stderr --partial "unmount --all"

  run --separate-stderr "$BIN" unmount --all "$PLUGIN" "$SERVICE"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --mounts
  assert_success
  assert_output ""

  # nothing left to remove is not an error
  run --separate-stderr "$BIN" unmount --all "$PLUGIN" "$SERVICE"
  assert_success
}

@test "($DEFINITION) a mount docker would refuse or mishandle is refused first" {
  local source data_target
  source="$(mount_source refused)"

  # the directory docker would create, empty and owned by root, if asked to
  # mount one that is not there
  if [[ "$DOKKU_LIB_HOST_ROOT" == "$DOKKU_LIB_ROOT" ]]; then
    run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source/missing:/opt/missing"
    assert_failure
    assert_stderr --partial "does not exist"
  fi

  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source:opt/relative"
  assert_failure
  assert_stderr --partial "must be absolute"

  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source:/opt/noexec:noexec"
  assert_failure
  assert_stderr --partial "noexec"

  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source:/opt/twice:ro" --volume-readonly
  assert_failure
  assert_stderr --partial -- "--volume-readonly"

  # a directory the definition already mounts is one docker would refuse to
  # mount a second thing at. Found from the running container, since each
  # definition keeps its data somewhere else
  data_target="$(container_inspect "$(service_container)" "{{ range .Mounts }}{{ if eq .Source \"$(service_root)/data\" }}{{ .Destination }}{{ end }}{{ end }}")"
  if [[ -n "$data_target" ]]; then
    run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source:$data_target"
    assert_failure
    assert_stderr --partial "already mounted by the"
  fi

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --mounts
  assert_success
  assert_output ""
}
