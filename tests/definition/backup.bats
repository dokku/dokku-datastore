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

@test "($DEFINITION) backup-auth refuses an endpoint url without a scheme" {
  run "$BIN" backup-auth "$PLUGIN" "$SERVICE" AKIAEXAMPLE wJalrXUtnFEMI nyc3 s3v4 nyc3.digitaloceanspaces.com
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup-auth"
  fi
  assert_failure
  assert_output --partial "must be an http or https url"
  [[ ! -e "$(service_root)/backup/ENDPOINT_URL" ]] || fail "backup-auth stored the endpoint url it refused"
}

@test "($DEFINITION) backup refuses a bucket named with a scheme" {
  run "$BIN" backup "$PLUGIN" "$SERVICE" s3://backups
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup"
  fi
  assert_failure
  assert_output --partial "without a scheme such as s3://"
  refute_output --partial "Backing up"
}

@test "($DEFINITION) backup refuses a bucket that breaks the s3 naming rules" {
  run "$BIN" backup "$PLUGIN" "$SERVICE" My_Bucket
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup"
  fi
  assert_failure
  assert_output --partial "invalid bucket name"
  refute_output --partial "Backing up"
}

@test "($DEFINITION) a refused backup fires the post-backup trigger" {
  fake_plugn_setup
  : >"$PLUGN_LOG"

  run "$BIN" backup "$PLUGIN" "$SERVICE" My_Bucket
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup"
  fi
  assert_failure
  assert_output --partial "invalid bucket name"

  run grep "service-action post-backup" "$PLUGN_LOG"
  assert_success
  assert_output "trigger service-action post-backup $PLUGIN $SERVICE My_Bucket failure"
}

@test "($DEFINITION) a backup of a missing service fires no post-backup trigger" {
  fake_plugn_setup
  : >"$PLUGN_LOG"

  run "$BIN" backup "$PLUGIN" missing-service my-bucket
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup"
  fi
  assert_failure
  assert_output --partial "does not exist"

  run grep "service-action post-backup" "$PLUGN_LOG"
  assert_failure
}

@test "($DEFINITION) a failing post-backup trigger is only warned about" {
  fake_plugn_setup
  : >"$PLUGN_LOG"

  PLUGN_FAIL_TRIGGER=service-action run "$BIN" backup "$PLUGIN" "$SERVICE" My_Bucket
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup"
  fi
  assert_failure 1
  assert_output --partial "invalid bucket name"
  assert_output --partial "failed to call service-action post-backup trigger"
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
  local probe
  probe="$(probe_path)"
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

  # an export named as a file on the host rather than redirected, which writes
  # nothing to stdout and a file only the dokku user and group read
  local file_dump="$BATS_TEST_TMPDIR/file.dump"
  run --separate-stderr "$BIN" export "$PLUGIN" "$SERVICE" --file "$file_dump"
  assert_success
  assert_output ""
  [[ -s "$file_dump" ]] || fail "export --file produced nothing"
  run find "$file_dump" -perm 640
  assert_output "$file_dump"

  if [[ -x "$probe" ]]; then
    run "$probe" clobber "$SERVICE"
    assert_success
  fi

  run "$BIN" import "$PLUGIN" "$SERVICE" --file "$file_dump" </dev/null
  assert_success

  if [[ -x "$probe" ]]; then
    run --separate-stderr "$probe" read "$SERVICE"
    assert_success
    assert_output "known"
  fi
}

@test "($DEFINITION) export through a terminal round trip" {
  # ssh -t, which the dokku client runs whenever its stdin is a terminal, makes
  # stdout a terminal, and a terminal adds a carriage return before every
  # newline written to it unless the export turns that off
  command -v script >/dev/null || skip "script is not installed"

  local probe
  probe="$(probe_path)"
  if [[ -x "$probe" ]]; then
    run "$probe" write "$SERVICE"
    assert_success
  fi

  # stderr is kept out of the terminal, or what the export logs would end up in
  # the dump script hands back
  local dump="$BATS_TEST_TMPDIR/terminal.dump" export_status=0
  script -qec "$(printf '%q ' "$BIN" export "$PLUGIN" "$SERVICE") 2>$(printf '%q' "$dump.err")" /dev/null >"$dump" || export_status=$?

  if [[ "$export_status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement export"
  fi
  [[ "$export_status" -eq 0 ]] || fail "export failed with status $export_status: $(cat "$dump.err")"
  [[ -s "$dump" ]] || fail "export produced nothing"

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
}

@test "($DEFINITION) export to an existing file needs --force" {
  local existing="$BATS_TEST_TMPDIR/existing.dump"
  echo "old" >"$existing"

  run "$BIN" export "$PLUGIN" "$SERVICE" --file "$existing"
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement export"
  fi
  assert_failure
  assert_output --partial "$existing"
  assert_output --partial "pass --force"
  run cat "$existing"
  assert_output "old"

  # --force is only for a file, since stdout has nothing to replace
  run "$BIN" export "$PLUGIN" "$SERVICE" --force
  assert_failure
  assert_output --partial "--force only applies"

  run --separate-stderr "$BIN" export "$PLUGIN" "$SERVICE" --file "$existing" --force
  assert_success
  assert_output ""
  [[ "$(cat "$existing")" != "old" ]] || fail "export --force left the old file in place"
  [[ -s "$existing" ]] || fail "export --force produced nothing"
}

@test "($DEFINITION) export to an unwritable destination fails before exporting" {
  # a directory is not a file an export can replace
  run "$BIN" export "$PLUGIN" "$SERVICE" --file "$BATS_TEST_TMPDIR"
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement export"
  fi
  assert_failure
  assert_output --partial "$BATS_TEST_TMPDIR"
  assert_output --partial "not a regular file"

  # the dump is written beside the path, so a directory the user cannot write
  # into fails before anything is exported and leaves nothing behind
  if [[ "$EUID" -eq 0 ]]; then
    skip "root writes a directory whatever its mode"
  fi
  local read_only="$BATS_TEST_TMPDIR/read-only"
  mkdir "$read_only"
  chmod 555 "$read_only"
  run "$BIN" export "$PLUGIN" "$SERVICE" --file "$read_only/data.dump"
  chmod 755 "$read_only"
  assert_failure
  assert_output --partial "$read_only/data.dump"

  run ls -A "$read_only"
  assert_success
  assert_output ""
}

@test "($DEFINITION) export and import of every database round trip" {
  if ! exports_all_databases; then
    skip "$DEFINITION does not export every database"
  fi

  # a record in the service's own database and one in another the service
  # holds, the way an app that makes a database of its own leaves it
  local probe
  probe="$(probe_path)"
  run "$probe" write "$SERVICE"
  assert_success
  run "$probe" write-extra "$SERVICE"
  assert_success

  local dump="$BATS_TEST_TMPDIR/all.dump" export_status=0
  "$BIN" export "$PLUGIN" "$SERVICE" --all-databases >"$dump" 2>"$dump.err" || export_status=$?
  [[ "$export_status" -eq 0 ]] || fail "export --all-databases failed with status $export_status: $(cat "$dump.err")"
  [[ -s "$dump" ]] || fail "export --all-databases produced nothing"

  run "$probe" clobber "$SERVICE"
  assert_success
  run "$probe" clobber-extra "$SERVICE"
  assert_success

  run "$BIN" import "$PLUGIN" "$SERVICE" --all-databases <"$dump"
  assert_success

  run --separate-stderr "$probe" read "$SERVICE"
  assert_success
  assert_output "known"
  run --separate-stderr "$probe" read-extra "$SERVICE"
  assert_success
  assert_output "known"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --status
  assert_success
  assert_output "running"

  # extra arguments reach the tools that dump and load every database too,
  # which a flag none of them has proves by being refused, leaving the data
  # as it was
  run "$BIN" export "$PLUGIN" "$SERVICE" --all-databases --file "$BATS_TEST_TMPDIR/refused.dump" -- "$UNKNOWN_ARG"
  assert_failure
  [[ ! -e "$BATS_TEST_TMPDIR/refused.dump" ]] || fail "a failed export left a dump behind"

  run "$BIN" import "$PLUGIN" "$SERVICE" --all-databases -- "$UNKNOWN_ARG" <"$dump"
  assert_failure

  run --separate-stderr "$probe" read-extra "$SERVICE"
  assert_success
  assert_output "known"

  # the same through files on the host
  local file_dump="$BATS_TEST_TMPDIR/all-file.dump"
  run --separate-stderr "$BIN" export "$PLUGIN" "$SERVICE" --all-databases --file "$file_dump"
  assert_success
  assert_output ""
  [[ -s "$file_dump" ]] || fail "export --all-databases --file produced nothing"

  run "$probe" clobber "$SERVICE"
  assert_success
  run "$probe" clobber-extra "$SERVICE"
  assert_success

  run "$BIN" import "$PLUGIN" "$SERVICE" --all-databases --file "$file_dump" </dev/null
  assert_success

  run --separate-stderr "$probe" read "$SERVICE"
  assert_success
  assert_output "known"
  run --separate-stderr "$probe" read-extra "$SERVICE"
  assert_success
  assert_output "known"

  # without the flag only the service's own database is exported, so an import
  # of it puts that back and leaves the other as it was
  local single="$BATS_TEST_TMPDIR/single.dump"
  "$BIN" export "$PLUGIN" "$SERVICE" >"$single" 2>"$single.err" || fail "export failed: $(cat "$single.err")"

  run "$probe" clobber "$SERVICE"
  assert_success
  run "$probe" clobber-extra "$SERVICE"
  assert_success

  run "$BIN" import "$PLUGIN" "$SERVICE" <"$single"
  assert_success

  run --separate-stderr "$probe" read "$SERVICE"
  assert_success
  assert_output "known"
  run --separate-stderr "$probe" read-extra "$SERVICE"
  assert_success
  assert_output "clobbered"
}

@test "($DEFINITION) an export of every database keeps the locale each was made with" {
  if [[ "$PLUGIN" != "postgres" ]]; then
    skip "only postgres makes each database again with its own locale"
  fi

  local probe
  probe="$(probe_path)"
  run --separate-stderr "$probe" write-icu "$SERVICE"
  assert_success
  [[ -n "$output" ]] || skip "$DEFINITION has no icu locale provider"
  assert_output "i:en-US:known"

  local dump="$BATS_TEST_TMPDIR/icu.dump" export_status=0
  "$BIN" export "$PLUGIN" "$SERVICE" --all-databases >"$dump" 2>"$dump.err" || export_status=$?
  [[ "$export_status" -eq 0 ]] || fail "export --all-databases failed with status $export_status: $(cat "$dump.err")"

  run "$probe" clobber-icu "$SERVICE"
  assert_success

  run "$BIN" import "$PLUGIN" "$SERVICE" --all-databases <"$dump"
  assert_success

  # dropped and made again from the dump, on the provider and locale it had
  run --separate-stderr "$probe" read-icu "$SERVICE"
  assert_success
  assert_output "i:en-US:known"
}

@test "($DEFINITION) every database is refused where it cannot be exported" {
  if exports_all_databases; then
    skip "$DEFINITION exports every database"
  fi

  # refused before the destination is made, so nothing is left at the path
  local file_dump="$BATS_TEST_TMPDIR/all.dump"
  run "$BIN" export "$PLUGIN" "$SERVICE" --all-databases --file "$file_dump"
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement export"
  fi
  assert_failure
  assert_output --partial "--all-databases is not supported"
  [[ ! -e "$file_dump" ]] || fail "a refused export left $file_dump behind"
  run ls -A "$BATS_TEST_TMPDIR"
  assert_output ""

  run "$BIN" import "$PLUGIN" "$SERVICE" --all-databases </dev/null
  assert_failure
  assert_output --partial "--all-databases is not supported"
}

@test "($DEFINITION) import of a missing file leaves the data alone" {
  local probe
  probe="$(probe_path)"
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

@test "($DEFINITION) extra arguments reach the export and import tools" {
  local probe
  probe="$(probe_path)"
  if [[ -x "$probe" ]]; then
    run "$probe" write "$SERVICE"
    assert_success
  fi

  local dump="$BATS_TEST_TMPDIR/dump" export_status=0
  "$BIN" export "$PLUGIN" "$SERVICE" >"$dump" 2>"$dump.err" || export_status=$?
  if [[ "$export_status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement export"
  fi
  [[ "$export_status" -eq 0 ]] || fail "export failed with status $export_status: $(cat "$dump.err")"

  # a definition whose tools ignore their arguments refuses them rather than
  # making a dump without them, and leaves the data alone
  if [[ -z "$(extra_arg export)" ]]; then
    run "$BIN" export "$PLUGIN" "$SERVICE" -- "$UNKNOWN_ARG"
    assert_failure
    assert_output --partial "does not take extra arguments"

    run "$BIN" import "$PLUGIN" "$SERVICE" -- "$UNKNOWN_ARG" <"$dump"
    assert_failure
    assert_output --partial "does not take extra arguments"

    if [[ -x "$probe" ]]; then
      run --separate-stderr "$probe" read "$SERVICE"
      assert_success
      assert_output "known"
    fi
    return
  fi

  # a flag the tool does not have is refused by the tool, which is what shows
  # the argument reached it. The import is refused before it touches the data
  run "$BIN" export "$PLUGIN" "$SERVICE" -- "$UNKNOWN_ARG"
  assert_failure

  run "$BIN" import "$PLUGIN" "$SERVICE" -- "$UNKNOWN_ARG" <"$dump"
  assert_failure

  if [[ -x "$probe" ]]; then
    run --separate-stderr "$probe" read "$SERVICE"
    assert_success
    assert_output "known"
  fi

  # and one it has makes a dump that loads back, alongside --file, which is
  # still read as the command's own flag
  local file_dump="$BATS_TEST_TMPDIR/extra.dump"
  run --separate-stderr "$BIN" export "$PLUGIN" "$SERVICE" --file "$file_dump" -- "$(extra_arg export)"
  assert_success
  [[ -s "$file_dump" ]] || fail "export with extra arguments produced nothing"

  if [[ -x "$probe" ]]; then
    run "$probe" clobber "$SERVICE"
    assert_success
  fi

  run "$BIN" import "$PLUGIN" "$SERVICE" --file "$file_dump" -- "$(extra_arg import)" </dev/null
  assert_success

  if [[ -x "$probe" ]]; then
    run --separate-stderr "$probe" read "$SERVICE"
    assert_success
    assert_output "known"
  fi
}

@test "($DEFINITION) the export and import properties are used unless replaced" {
  if [[ -z "$(extra_arg export)" ]]; then
    skip "$PLUGIN does not take extra arguments"
  fi

  local probe
  probe="$(probe_path)"
  if [[ -x "$probe" ]]; then
    run "$probe" write "$SERVICE"
    assert_success
  fi

  # the value follows -- so that set does not read it as a flag of its own
  run "$BIN" set "$PLUGIN" "$SERVICE" export-args -- "$UNKNOWN_ARG"
  assert_success
  run "$BIN" set "$PLUGIN" "$SERVICE" import-args -- "$UNKNOWN_ARG"
  assert_success

  # every export and import is made with the property, which the tools refuse
  local dump="$BATS_TEST_TMPDIR/dump"
  run "$BIN" export "$PLUGIN" "$SERVICE" --file "$dump"
  assert_failure

  # until arguments given for the run replace it
  run --separate-stderr "$BIN" export "$PLUGIN" "$SERVICE" --file "$dump" -- "$(extra_arg export)"
  assert_success
  [[ -s "$dump" ]] || fail "export with extra arguments produced nothing"

  run "$BIN" import "$PLUGIN" "$SERVICE" --file "$dump" </dev/null
  assert_failure

  if [[ -x "$probe" ]]; then
    run --separate-stderr "$probe" read "$SERVICE"
    assert_success
    assert_output "known"
    run "$probe" clobber "$SERVICE"
    assert_success
  fi

  run "$BIN" import "$PLUGIN" "$SERVICE" --file "$dump" -- "$(extra_arg import)" </dev/null
  assert_success

  if [[ -x "$probe" ]]; then
    run --separate-stderr "$probe" read "$SERVICE"
    assert_success
    assert_output "known"
  fi

  # and unset, the datastore's own arguments are enough again
  run "$BIN" set "$PLUGIN" "$SERVICE" export-args
  assert_success
  run "$BIN" set "$PLUGIN" "$SERVICE" import-args
  assert_success

  run --separate-stderr "$BIN" export "$PLUGIN" "$SERVICE" --file "$dump" --force
  assert_success

  run "$BIN" import "$PLUGIN" "$SERVICE" --file "$dump" </dev/null
  assert_success
}

# whether timescaledb holds the service's database in restore mode, which keeps
# its background workers from running. Asked in a session of its own, so it
# reads what the database was left set to rather than what one session set
restoring_of() {
  local password database
  password="$(cat "$(service_root)/PASSWORD")"
  database="$(cat "$(service_root)/DATABASE_NAME")"
  docker container exec --env "PGPASSWORD=$password" "$(service_container)" \
    psql -qtAX -h localhost -U postgres -d "$database" -c "SHOW timescaledb.restoring;"
}

@test "($DEFINITION) a timescaledb import leaves the database out of restore mode" {
  [[ "$DEFINITION" == postgres-timescaledb-* ]] || skip "$DEFINITION has no background workers to stop for an import"

  # the workers are stopped for the restore, so that one cannot write the
  # catalog row the restore is about to copy back, and started again after
  local dump="$BATS_TEST_TMPDIR/timescaledb.dump"
  run "$BIN" export "$PLUGIN" "$SERVICE" --file "$dump"
  assert_success
  run "$BIN" import "$PLUGIN" "$SERVICE" --file "$dump" </dev/null
  assert_success
  run --separate-stderr restoring_of
  assert_success
  assert_output "off"

  # and started again when the restore fails, rather than left stopped
  local not_a_dump="$BATS_TEST_TMPDIR/not-a-dump"
  echo "not a dump" >"$not_a_dump"
  run "$BIN" import "$PLUGIN" "$SERVICE" --file "$not_a_dump" </dev/null
  assert_failure
  run --separate-stderr restoring_of
  assert_success
  assert_output "off"
}
