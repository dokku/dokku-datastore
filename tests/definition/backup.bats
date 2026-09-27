#!/usr/bin/env bats
# Exercises what a service's data can be taken out as and put back from.

load ../test_helper

setup_file() {
  datastore_setup_file
  create_service "$SERVICE"
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

@test "($DEFINITION) backup credentials are unreadable by other users" {
  run "$BIN" backup-auth "$PLUGIN" "$SERVICE" AKIAEXAMPLE wJalrXUtnFEMI us-east-1 s3v4 http://127.0.0.1:9000
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup-auth"
  fi
  assert_success

  assert_mode 750 "$(service_root)/backup"
  local name
  for name in AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION AWS_SIGNATURE_VERSION ENDPOINT_URL; do
    assert_mode 640 "$(service_root)/backup/$name"
  done

  # the pair is reported only as a fingerprint tooling can compute for itself
  local fingerprint
  fingerprint="$(printf '%s\n%s' AKIAEXAMPLE wJalrXUtnFEMI | sha256sum | cut -d' ' -f1)"
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-auth-fingerprint
  assert_success
  assert_output "$fingerprint"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --format json
  assert_success
  refute_output --partial "wJalrXUtnFEMI"
  assert_output --partial '"backup-authenticated":"true"'
  assert_output --partial '"backup-default-region":"us-east-1"'
  assert_output --partial '"backup-signature-version":"s3v4"'
  assert_output --partial '"backup-endpoint-url":"http://127.0.0.1:9000"'

  run "$BIN" backup-deauth "$PLUGIN" "$SERVICE"
  assert_success
  [[ ! -d "$(service_root)/backup" ]] || fail "backup-deauth left the credentials behind"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-auth-fingerprint
  assert_success
  assert_output ""
}

@test "($DEFINITION) backup-auth replaces the settings it stored earlier" {
  run "$BIN" backup-auth "$PLUGIN" "$SERVICE" AKIAEXAMPLE wJalrXUtnFEMI us-east-1 s3v4 http://127.0.0.1:9000
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup-auth"
  fi
  assert_success

  run "$BIN" backup-auth "$PLUGIN" "$SERVICE" AKIAEXAMPLE wJalrXUtnFEMI
  assert_success

  local name
  for name in AWS_DEFAULT_REGION AWS_SIGNATURE_VERSION ENDPOINT_URL; do
    [[ ! -e "$(service_root)/backup/$name" ]] || fail "backup-auth left $name behind"
  done

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --format json
  assert_success
  assert_output --partial '"backup-authenticated":"true"'
  assert_output --partial '"backup-default-region":""'
  assert_output --partial '"backup-signature-version":""'
  assert_output --partial '"backup-endpoint-url":""'

  run "$BIN" backup-deauth "$PLUGIN" "$SERVICE"
  assert_success
}

@test "($DEFINITION) the backup passphrase is reported as a fingerprint" {
  run "$BIN" backup-set-encryption "$PLUGIN" "$SERVICE" "correct horse battery staple"
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup-set-encryption"
  fi
  assert_success

  local fingerprint
  fingerprint="$(printf '%s' "correct horse battery staple" | sha256sum | cut -d' ' -f1)"
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-encryption-fingerprint
  assert_success
  assert_output "$fingerprint"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --format json
  assert_success
  refute_output --partial "correct horse battery staple"

  run "$BIN" backup-unset-encryption "$PLUGIN" "$SERVICE"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-encryption-fingerprint
  assert_success
  assert_output ""
}

@test "($DEFINITION) export and import round trip" {
  # a probe writes and reads back a known record, so the round trip proves the
  # data survived rather than only that the service came back up. A definition
  # without one still gets every other check
  local probe="$REPO_ROOT/tests/probes/$DEFINITION.sh"
  if [[ -x "$probe" ]]; then
    run "$probe" write "$SERVICE"
    assert_success
  fi

  # written straight to a file rather than through run, since a dump may be
  # binary and $output would not carry it faithfully
  local dump="$BATS_TEST_TMPDIR/dump" export_status=0
  "$BIN" export "$PLUGIN" "$SERVICE" >"$dump" 2>"$dump.err" || export_status=$?

  # a datastore that declares no export exits the way dokku expects of a plugin
  # that does not handle a command, and there is no round trip to make. Every
  # other check still applies: a cache is still created and still destroyed.
  if [[ "$export_status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement export"
  fi
  [[ "$export_status" -eq 0 ]] || fail "export failed with status $export_status: $(cat "$dump.err")"
  [[ -s "$dump" ]] || fail "export produced nothing"

  # overwritten first, so that finding the record afterwards means the import
  # put it back rather than that it was never gone
  if [[ -x "$probe" ]]; then
    run "$probe" clobber "$SERVICE"
    assert_success
  fi

  run "$BIN" import "$PLUGIN" "$SERVICE" <"$dump"
  assert_success

  if [[ -x "$probe" ]]; then
    run --separate-stderr "$probe" read "$SERVICE"
    assert_success
    assert_output "known"
  fi

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --status
  assert_success
  assert_output "running"

  # the same dump named as a file on the host rather than piped in. stdin is a
  # char device here, which import refuses unless it has a file to read instead
  if [[ -x "$probe" ]]; then
    run "$probe" clobber "$SERVICE"
    assert_success
  fi

  run "$BIN" import "$PLUGIN" "$SERVICE" --file "$dump" </dev/null
  assert_success

  if [[ -x "$probe" ]]; then
    run --separate-stderr "$probe" read "$SERVICE"
    assert_success
    assert_output "known"
  fi

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --status
  assert_success
  assert_output "running"
}

@test "($DEFINITION) import of a missing file leaves the data alone" {
  local probe="$REPO_ROOT/tests/probes/$DEFINITION.sh"
  if [[ -x "$probe" ]]; then
    run "$probe" write "$SERVICE"
    assert_success
  fi

  run "$BIN" import "$PLUGIN" "$SERVICE" --file "$BATS_TEST_TMPDIR/missing.dump" </dev/null
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement import"
  fi
  assert_failure
  assert_output --partial "missing.dump"

  if [[ -x "$probe" ]]; then
    run --separate-stderr "$probe" read "$SERVICE"
    assert_success
    assert_output "known"
  fi
}
