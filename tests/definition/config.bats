#!/usr/bin/env bats
# Graphite's config directory is mounted where carbon reads its configuration,
# and filled with the files the image ships before the mount hides them. A
# service made before it was mounted has the directory already, empty or
# holding what an operator put there, and has the rest filled in once its
# container is made again.

load ../test_helper

# a schema carbon accepts, so a service that starts on it proves the file was
# read rather than only copied
CUSTOM_SCHEMA='[dokku]
pattern = .*
retentions = 10s:1d'

setup_file() {
  datastore_setup_file
  [[ "$DEFINITION" == "graphite" ]] || return 0
  create_service "$SERVICE"
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

setup() {
  if [[ "$DEFINITION" != "graphite" ]]; then
    skip "$DEFINITION does not mount its config directory over the image's"
  fi
}

# the seeded files are the container's user's, root, so they are written from
# inside it rather than from the host
in_container() {
  docker container exec "$(service_container)" sh -c "$1"
}

@test "($DEFINITION) a new service has the image's config in its config directory" {
  [[ -f "$(service_root)/config/carbon.conf" ]] || fail "expected carbon.conf to be copied out"
  [[ -f "$(service_root)/config/storage-schemas.conf" ]] || fail "expected storage-schemas.conf to be copied out"
  [[ -s "$(service_root)/PRE_CREATE" ]] || fail "expected the hook that ran to be recorded"

  run in_container "cat /opt/graphite/conf/carbon.conf"
  assert_success
  assert_output "$(cat "$(service_root)/config/carbon.conf")"

  run "$(probe_path)" write "$SERVICE"
  assert_success
  run "$(probe_path)" read "$SERVICE"
  assert_success
}

@test "($DEFINITION) an edited config is kept when the service is rebuilt" {
  run in_container "printf '%s\n' '$CUSTOM_SCHEMA' > /opt/graphite/conf/storage-schemas.conf"
  assert_success

  run rebuild_service
  assert_success

  run cat "$(service_root)/config/storage-schemas.conf"
  assert_output "$CUSTOM_SCHEMA"

  run "$(probe_path)" read "$SERVICE"
  assert_success
}

@test "($DEFINITION) a service made before its config was mounted keeps what it has and gets the rest" {
  # what a service made before this looks like: no hook recorded, and a
  # config directory holding only the file its operator put there
  run in_container "find /opt/graphite/conf -mindepth 1 ! -name storage-schemas.conf -delete"
  assert_success
  rm -f "$(service_root)/PRE_CREATE"
  [[ ! -e "$(service_root)/config/carbon.conf" ]] || fail "expected carbon.conf to be gone"

  run rebuild_service
  assert_success

  [[ -f "$(service_root)/config/carbon.conf" ]] || fail "expected carbon.conf to be copied in"
  [[ -s "$(service_root)/PRE_CREATE" ]] || fail "expected the hook that ran to be recorded"

  run cat "$(service_root)/config/storage-schemas.conf"
  assert_output "$CUSTOM_SCHEMA"

  run "$(probe_path)" read "$SERVICE"
  assert_success
}
