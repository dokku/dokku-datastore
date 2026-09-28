#!/usr/bin/env bats
# A datastore's data is only as safe as the directory it lands in. The service
# binds its data directory from the service root, and that is the only place a
# rebuild keeps. Data an image writes anywhere else ends up in the container's
# own layer or in an anonymous volume docker made for it, and both are left
# behind the next time the container is replaced.

load ../test_helper

setup_file() {
  datastore_setup_file
  create_service "$SERVICE"

  # a record of its own where the datastore has a probe, so there is data to
  # find even for a datastore that writes nothing until asked to
  local probe
  probe="$(probe_path)"
  if [[ -x "$probe" ]]; then
    "$probe" write "$SERVICE"
  fi
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

# the destinations of the binds from the service's data directory, one per line
data_targets() {
  local host_root="$DOKKU_LIB_HOST_ROOT/services/$DATA_DIR/$SERVICE" source destination
  while read -r source destination; do
    [[ -n "$source" ]] || continue
    if is_under "$source" "$host_root/data"; then
      echo "$destination"
    fi
  done < <(binds_of "$(service_container)")
}

# the volumes an image declares that a definition leaves unbound on purpose,
# each with the reason in the definition beside its binds
unbound_on_purpose() {
  case "$DEFINITION:$1" in
  # solr's logs and its log4j2.xml, with the cores bound below it
  solr-8:/var/solr) return 0 ;;
  esac
  return 1
}

# whether a path in the container is on one of the service's binds
on_a_bind() {
  local destination
  while read -r _ destination; do
    [[ -n "$destination" ]] || continue
    is_under "$1" "$destination" && return 0
  done <<<"$BINDS"
  return 1
}

@test "($DEFINITION) nothing is written to an anonymous volume" {
  # before the rebuild below, which would hand the container fresh ones
  local destination entry written=""
  BINDS="$(binds_of "$(service_container)")"
  while read -r destination; do
    [[ -n "$destination" ]] || continue
    unbound_on_purpose "$destination" && continue
    # a bind below the volume is the service's own, and is copied along with it
    while read -r entry; do
      [[ -n "$entry" ]] || continue
      on_a_bind "$entry" || written+="$entry"$'\n'
    done < <(container_listing "$(service_container)" "$destination")
  done < <(volumes_of "$(service_container)")

  [[ -z "$written" ]] || fail "written to anonymous volumes, which a rebuild leaves behind:"$'\n'"$written"
}

@test "($DEFINITION) every volume the image declares is under a bind from the service" {
  local path missing=""
  BINDS="$(binds_of "$(service_container)")"
  while read -r path; do
    [[ -n "$path" ]] || continue
    on_a_bind "$path" || unbound_on_purpose "$path" || missing+=" $path"
  done < <(docker image inspect "$IMAGE:$IMAGE_VERSION" --format '{{ range $path, $_ := .Config.Volumes }}{{ $path }}{{ "\n" }}{{ end }}')

  [[ -z "$missing" ]] || fail "the image declares$missing, which no bind covers. Binds are:"$'\n'"$BINDS"
}

@test "($DEFINITION) a record written before a rebuild is there after it" {
  local probe
  probe="$(probe_path)"
  [[ -x "$probe" ]] || skip "$DEFINITION has no probe"

  run rebuild_service
  assert_success

  run --separate-stderr "$probe" read "$SERVICE"
  assert_success
  assert_output "known"
}

@test "($DEFINITION) the datastore writes into its data directory on the host" {
  # after the rebuild above, since a datastore such as redis only writes its
  # data out when it is stopped
  local targets target
  targets="$(data_targets)"
  [[ -n "$targets" ]] || skip "$DEFINITION keeps no data on disk"

  for target in $targets; do
    [[ -n "$(container_listing "$(service_container)" "$target")" ]] && return 0
  done

  fail "nothing was written to ${targets//$'\n'/ }, so the datastore is keeping its data somewhere else"
}
