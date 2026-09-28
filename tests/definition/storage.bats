#!/usr/bin/env bats
# A datastore's data is only as safe as the directory it lands in. The service
# binds its data directory from the service root, and that is the only place a
# rebuild keeps. Data an image writes anywhere else ends up in the container's
# own layer or in an anonymous volume docker made for it, and both are left
# behind the next time the container is replaced.
#
# Every check that needs data writes its own record through the datastore's
# probe, so each stands on its own and none relies on another having run first.

load ../test_helper

setup_file() {
  datastore_setup_file
  create_service "$SERVICE"
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

# the service root as dockerd sees it, which is what a bind's source is
host_root() {
  echo "$DOKKU_LIB_HOST_ROOT/services/$DATA_DIR/$SERVICE"
}

# the destinations of the binds from the service's data directory, one per line
data_targets() {
  local source destination
  while read -r source destination; do
    [[ -n "$source" ]] || continue
    if is_under "$source" "$(host_root)/data"; then
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

# paths in a container's own layer that hold nothing a datastore keeps: pid
# files and sockets, scratch space, logs, which docker keeps a copy of anyway,
# caches rebuilt on start, the history a client the probes run keeps, and the
# certificate authority a docker desktop such as orbstack adds to every
# container it starts
not_data() {
  case "$1" in
  /run | /run/* | /var/run | /var/run/* | /tmp | /tmp/*) return 0 ;;
  /var/log/* | */logs/* | *.log | *.log.[0-9]*) return 0 ;;
  */.cache/* | */dropin.cache | /var/lib/nginx/*) return 0 ;;
  /root/.*_history) return 0 ;;
  */orbstack-root.crt | /etc/ssl/certs/ca-certificates.crt) return 0 ;;
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

# whether a path in the container is on one of its docker volumes, whose
# contents are checked on their own
on_a_volume() {
  local destination
  while read -r destination; do
    [[ -n "$destination" ]] || continue
    is_under "$1" "$destination" && return 0
  done <<<"$VOLUMES"
  return 1
}

# every path the container's own layer added or changed, less the directories
# that only changed because something was added below them
layer_changes() {
  docker container diff "$1" | awk '{ print $2 }' | awk '
    { paths[NR] = $0 }
    END {
      for (i = 1; i <= NR; i++) {
        parent = 0
        for (j = 1; j <= NR; j++) {
          if (index(paths[j], paths[i] "/") == 1) { parent = 1; break }
        }
        if (!parent) print paths[i]
      }
    }
  '
}

# writes the probe's record, or does nothing for a datastore without a probe
write_record() {
  local probe
  probe="$(probe_path)"
  if [[ -x "$probe" ]]; then
    "$probe" write "$SERVICE"
  fi
}

# stops the service container without removing it, which nothing but docker
# does, so that what a datastore only writes when it shuts down is written, and
# the container is still there to look inside
stop_in_place() {
  docker container stop "$(service_container)" >/dev/null
}

# runs a shell command against the service root as root, in a container of its
# own, since the files a datastore writes belong to its own user rather than
# to whoever runs the tests
as_root() {
  docker container run --rm --volume "$(host_root):/mnt" busybox:1.37.0-uclibc sh -c "$1"
}

# the service's data directory and every bind source inside it, relative to
# the service root, the directory itself first
data_directories() {
  local source
  echo data
  while read -r source _; do
    [[ -n "$source" ]] || continue
    if [[ "$source" == "$(host_root)/data/"* ]]; then
      echo "${source#"$(host_root)"/}"
    fi
  done < <(binds_of "$(service_container)") | sort -u
}

@test "($DEFINITION) nothing is written outside the service's binds" {
  run write_record
  assert_success

  BINDS="$(binds_of "$(service_container)")"
  VOLUMES="$(volumes_of "$(service_container)")"

  # stopped before anything is looked at, so that what the datastore only
  # writes when it shuts down, or on a timer, is there to be found
  stop_in_place

  # an anonymous volume, which a rebuild leaves behind with whatever is in it
  local destination entry in_volumes="" in_layer=""
  while read -r destination; do
    [[ -n "$destination" ]] || continue
    unbound_on_purpose "$destination" && continue
    # a bind below the volume is the service's own, and is copied along with it
    while read -r entry; do
      [[ -n "$entry" ]] || continue
      on_a_bind "$entry" || in_volumes+="$entry"$'\n'
    done < <(container_listing "$(service_container)" "$destination")
  done <<<"$VOLUMES"

  # the container's own layer, which a rebuild throws away. A bind or volume
  # shows up in it as the directory it is mounted on
  while read -r entry; do
    [[ -n "$entry" ]] || continue
    on_a_bind "$entry" || on_a_volume "$entry" || not_data "$entry" || in_layer+="$entry"$'\n'
  done < <(layer_changes "$(service_container)")

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success

  [[ -z "$in_volumes" ]] || fail "written to anonymous volumes, which a rebuild leaves behind:"$'\n'"$in_volumes"
  [[ -z "$in_layer" ]] || fail "written to the container's own filesystem, which a rebuild throws away:"$'\n'"$in_layer"
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

  run "$probe" write "$SERVICE"
  assert_success

  run rebuild_service
  assert_success

  run --separate-stderr "$probe" read "$SERVICE"
  assert_success
  assert_output "known"
}

@test "($DEFINITION) the record is kept in the service's data directory" {
  local probe directories
  probe="$(probe_path)"
  [[ -x "$probe" ]] || skip "$DEFINITION has no probe"
  [[ -n "$(data_targets)" ]] || skip "$DEFINITION keeps no data on disk"

  run "$probe" write "$SERVICE"
  assert_success

  # an empty data directory in place of the one holding the record, each bind
  # source in it made again as it was, so the datastore starts on nothing
  directories="$(data_directories | tr '\n' ' ')"
  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success
  run as_root "mv /mnt/data /mnt/data.kept && for directory in $directories; do kept=/mnt/data.kept\${directory#data}; mkdir -p /mnt/\$directory && chown \$(stat -c %u:%g \$kept) /mnt/\$directory && chmod \$(stat -c %a \$kept) /mnt/\$directory; done"
  assert_success

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success

  # the record is gone with the directory, so it was in the directory rather
  # than anywhere the container keeps across a start
  run --separate-stderr "$probe" read "$SERVICE"
  refute_output "known"

  # and back with it
  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success
  run as_root "rm -rf /mnt/data && mv /mnt/data.kept /mnt/data"
  assert_success

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success

  run --separate-stderr "$probe" read "$SERVICE"
  assert_success
  assert_output "known"
}
