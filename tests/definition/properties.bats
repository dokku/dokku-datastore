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

@test "($DEFINITION) a backup storage class is set, read back and unset" {
  run "$BIN" set "$PLUGIN" "$SERVICE" backup-storage-class STANDARD_IA
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-storage-class
  assert_success
  assert_output "STANDARD_IA"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --format json
  assert_success
  assert_output --partial '"backup-storage-class":"STANDARD_IA"'

  run "$BIN" set "$PLUGIN" "$SERVICE" backup-storage-class
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-storage-class
  assert_success
  assert_output ""
}

@test "($DEFINITION) a backup storage class the aws cli would refuse is refused first" {
  local value
  # the aws cli in the backup image matches the name exactly, and refuses it
  # only once the service has already been exported
  for value in standard_ia NOT_A_CLASS "STANDARD IA"; do
    run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" backup-storage-class "$value"
    assert_failure
    assert_stderr --partial "backup-storage-class"
  done

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-storage-class
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

@test "($DEFINITION) a wait timeout reaches the readiness probe" {
  run "$BIN" set "$PLUGIN" "$SERVICE" wait-timeout 90
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --wait-timeout
  assert_success
  assert_output "90"

  # read when the probe runs rather than when a container is made, so a restart
  # is enough for it to take
  run "$BIN" restart "$PLUGIN" "$SERVICE"
  assert_success
  assert_output --partial "to be ready (timeout: 90s)"

  # and back to what every other check expects of this service
  run "$BIN" set "$PLUGIN" "$SERVICE" wait-timeout
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --wait-timeout
  assert_success
  assert_output ""
}

@test "($DEFINITION) a wait timeout the probe cannot use is refused first" {
  local value
  for value in abc 0; do
    run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" wait-timeout "$value"
    assert_failure
    assert_stderr --partial "wait-timeout"
  done

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --wait-timeout
  assert_success
  assert_output ""
}

@test "($DEFINITION) the expose settings are set, read back and unset" {
  run "$BIN" set "$PLUGIN" "$SERVICE" expose-address 10.0.0.5
  assert_success
  run "$BIN" set "$PLUGIN" "$SERVICE" expose-source-range 203.0.113.7
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --expose-address
  assert_success
  assert_output "10.0.0.5"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --expose-source-range
  assert_success
  assert_output "203.0.113.7"

  # and back to what every other check expects of this service
  run "$BIN" set "$PLUGIN" "$SERVICE" expose-address
  assert_success
  run "$BIN" set "$PLUGIN" "$SERVICE" expose-source-range
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --expose-address
  assert_success
  assert_output ""

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --expose-source-range
  assert_success
  assert_output ""
}

@test "($DEFINITION) an expose setting docker or socat would refuse is refused first" {
  local value
  # docker publishes on an address, not a name, and without a port's brackets
  for value in localhost "[::1]" 10.0.0.5:6379; do
    run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" expose-address "$value"
    assert_failure
    assert_stderr --partial "expose-address"
  done

  # socat honors a single range for each port it listens on
  for value in 10.0.0.0/33 "10.0.0.0/8,192.168.0.0/16" example.com; do
    run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" expose-source-range "$value"
    assert_failure
    assert_stderr --partial "expose-source-range"
  done

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --expose-address
  assert_success
  assert_output ""

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --expose-source-range
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

@test "($DEFINITION) a host path is mounted from its subpath" {
  local source target="/opt/dokku-subpath"
  source="$(mount_source subpath)"
  mkdir -p "$source/inner"

  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source:$target:volume-subpath=inner"
  assert_success

  run rebuild_service
  assert_success

  run mount_of "$(service_container)" "$target"
  assert_success
  assert_output "$source/inner:true"

  # a subpath that is not there is one docker would create, empty and owned by
  # root, so it is refused the way a missing host path is
  if [[ "$DOKKU_LIB_HOST_ROOT" == "$DOKKU_LIB_ROOT" ]]; then
    run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source:/opt/dokku-missing:volume-subpath=missing"
    assert_failure
    assert_stderr --partial "$source/missing does not exist"
  fi

  run --separate-stderr "$BIN" unmount --all "$PLUGIN" "$SERVICE"
  assert_success

  run rebuild_service
  assert_success
}

@test "($DEFINITION) a docker volume is mounted from its subpath" {
  local api volume="dokku-datastore-$SERVICE-subpath" target="/opt/dokku-volume-subpath"
  api="$(docker version --format '{{ .Server.APIVersion }}')"
  if [[ "$(printf '%s\n' "1.45" "$api" | sort -V | head -n 1)" != "1.45" ]]; then
    skip "the daemon speaks api $api, older than the 1.45 a volume subpath needs"
  fi

  # docker requires the subpath to exist inside the volume already
  docker volume create "$volume" >/dev/null
  docker container run --rm --volume "$volume:/volume" busybox:1.37.0-uclibc mkdir -p /volume/inner

  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$volume:$target:volume-subpath=inner"
  assert_success

  run rebuild_service
  assert_success

  run container_inspect "$(service_container)" "{{ range .HostConfig.Mounts }}{{ if eq .Target \"$target\" }}{{ .Source }}:{{ .VolumeOptions.Subpath }}{{ end }}{{ end }}"
  assert_success
  assert_output "$volume:inner"

  # a bind mount's options mean nothing to a volume, and are refused rather
  # than handed to a --mount that would not take them
  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$volume:/opt/dokku-relabelled:volume-subpath=inner,z"
  assert_failure
  assert_stderr --partial "only takes nocopy"

  run --separate-stderr "$BIN" unmount --all "$PLUGIN" "$SERVICE"
  assert_success

  run rebuild_service
  assert_success

  docker volume rm "$volume" >/dev/null
}

@test "($DEFINITION) a mounted directory inside the service is chowned before the container is made" {
  [[ "$DOKKU_LIB_HOST_ROOT" == "$DOKKU_LIB_ROOT" ]] || skip "dockerd sees another host root, so the owner is not readable from here"

  local directory target="/opt/dokku-chown"
  directory="$(service_root)/chowned"
  mkdir -p "$directory"

  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$directory:$target:volume-chown=4321"
  assert_success

  run rebuild_service
  assert_success

  run stat -c %u "$directory"
  assert_success
  assert_output "4321"

  run --separate-stderr "$BIN" unmount --all "$PLUGIN" "$SERVICE"
  assert_success

  run rebuild_service
  assert_success
}

@test "($DEFINITION) a chown outside the service is refused" {
  local source
  source="$(mount_source chown)"

  # anything outside the service's own directory belongs to somebody else
  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "$source:/opt/dokku-chown:volume-chown=heroku"
  assert_failure
  assert_stderr --partial "only supported on a host path inside"

  run --separate-stderr "$BIN" mount "$PLUGIN" "$SERVICE" "some-volume:/opt/dokku-chown:volume-chown=heroku"
  assert_failure
  assert_stderr --partial "not supported on docker volume"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --mounts
  assert_success
  assert_output ""
}

@test "($DEFINITION) the export and import arguments are set, read back and unset" {
  local key verb
  for verb in export import; do
    key="$verb-args"

    # refused for a definition whose tools would ignore them, and nothing kept
    if [[ -z "$(extra_arg "$verb")" ]]; then
      run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" "$key" -- --anything
      assert_failure
      assert_stderr --partial "does not take extra arguments"
    else
      # the value follows -- so that set does not read it as a flag of its own
      run "$BIN" set "$PLUGIN" "$SERVICE" "$key" -- "$(extra_arg "$verb") --where=\"id > 1\""
      assert_success

      run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" "--$key"
      assert_success
      assert_output "$(extra_arg "$verb") --where=\"id > 1\""

      # a value that does not split into arguments is refused before it is kept
      run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" "$key" -- "--where=\"id > 1"
      assert_failure
      assert_stderr --partial "invalid $key value"

      run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" "$key" -- "--where=\$ID"
      assert_failure
      assert_stderr --partial "invalid $key value"
    fi

    # clearing is always allowed
    run "$BIN" set "$PLUGIN" "$SERVICE" "$key"
    assert_success

    run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" "--$key"
    assert_success
    assert_output ""
  done
}
