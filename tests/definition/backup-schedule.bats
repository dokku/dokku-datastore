#!/usr/bin/env bats
# A scheduled backup is handed to dokku through the cron-entries trigger, which
# dokku reads whenever it writes its crontab. Scheduling one records it and asks
# dokku to write the crontab again, and the trigger prints what was recorded.

load ../test_helper

DESTROYED_SERVICE="$SERVICE-destroyed"

setup_file() {
  datastore_setup_file
  fake_dokku_setup
  create_service "$SERVICE"
}

teardown_file() {
  datastore_teardown_file "$SERVICE" "$DESTROYED_SERVICE"
}

setup() {
  : >"$PLUGN_LOG"
}

# schedules a backup, skipping the test for a datastore with no backups
schedule_backup() {
  run "$BIN" backup-schedule "$PLUGIN" "$@"
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup-schedule"
  fi
}

# asserts that dokku was asked to write its crontab again, under the trigger
# name of every dokku version the plugin supports
assert_crontab_regenerated() {
  run cat "$PLUGN_LOG"
  assert_line "trigger scheduler-cron-write"
  assert_line "trigger cron-write"
}

@test "($DEFINITION) a scheduled backup is handed to dokku" {
  schedule_backup "$SERVICE" "0 3 * * *" my-bucket
  assert_success
  assert_crontab_regenerated

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  assert_output "0 3 * * *;dokku $PLUGIN:backup $SERVICE my-bucket;/var/log/dokku/$PLUGIN.log"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE"
  assert_success
  assert_output "0 3 * * * dokku $PLUGIN:backup $SERVICE my-bucket &>> /var/log/dokku/$PLUGIN.log"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-schedule
  assert_success
  assert_output "0 3 * * *"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-bucket
  assert_success
  assert_output "my-bucket"
}

@test "($DEFINITION) a scheduled backup can use an iam profile" {
  schedule_backup "$SERVICE" "@daily" my-bucket --use-iam
  assert_success

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  assert_output "@daily;dokku $PLUGIN:backup $SERVICE my-bucket --use-iam;/var/log/dokku/$PLUGIN.log"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-use-iam
  assert_success
  assert_output "true"
}

@test "($DEFINITION) a schedule cron cannot run is refused" {
  schedule_backup "$SERVICE" "0 3 * * *" my-bucket
  assert_success

  # what issue 6 was scheduled with, which cron skipped without a word
  schedule_backup "$SERVICE" daily my-bucket
  assert_failure

  schedule_backup "$SERVICE" "0 3 * * *" "my-bucket;true"
  assert_failure

  # and the schedule the service already had is kept
  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  assert_output "0 3 * * *;dokku $PLUGIN:backup $SERVICE my-bucket;/var/log/dokku/$PLUGIN.log"
}

@test "($DEFINITION) an unscheduled backup is taken back from dokku" {
  schedule_backup "$SERVICE" "0 3 * * *" my-bucket
  assert_success

  : >"$PLUGN_LOG"
  run "$BIN" backup-unschedule "$PLUGIN" "$SERVICE"
  assert_success
  assert_crontab_regenerated

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  assert_output ""

  run "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE"
  assert_failure
}

@test "($DEFINITION) a destroyed service is taken back from dokku" {
  create_service "$DESTROYED_SERVICE"
  schedule_backup "$DESTROYED_SERVICE" "0 3 * * *" my-bucket
  assert_success

  : >"$PLUGN_LOG"
  run "$BIN" destroy "$PLUGIN" "$DESTROYED_SERVICE" --force
  assert_success
  assert_crontab_regenerated

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  refute_output --partial "$DESTROYED_SERVICE"
}
