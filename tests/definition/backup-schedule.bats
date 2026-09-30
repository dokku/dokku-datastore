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
  assert_output "0 3 * * *;dokku $PLUGIN:backup $SERVICE my-bucket;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE"
  assert_success
  assert_output "0 3 * * * dokku $PLUGIN:backup $SERVICE my-bucket &>> $DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

  # and the same two as json, for something other than dokku to read
  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local --format json
  assert_success
  run jq -r '.[0] | [.schedule, .command, .["log-file"]] | join(";")' <<<"$output"
  assert_success
  assert_output "0 3 * * *;dokku $PLUGIN:backup $SERVICE my-bucket;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE" --format json
  assert_success
  run jq -r '[.schedule, .["bucket-name"], (.["use-iam"] | tostring), .["crontab-line"]] | join("|")' <<<"$output"
  assert_success
  assert_output "0 3 * * *|my-bucket|false|0 3 * * * dokku $PLUGIN:backup $SERVICE my-bucket &>> $DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

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
  assert_output "@daily;dokku $PLUGIN:backup $SERVICE my-bucket --use-iam;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-use-iam
  assert_success
  assert_output "true"
}

@test "($DEFINITION) a scheduled backup can have its output mailed" {
  schedule_backup "$SERVICE" "@daily" my-bucket
  assert_success

  # the crontab is written from the mailto, so setting it has dokku write it again
  : >"$PLUGN_LOG"
  run "$BIN" set "$PLUGIN" "$SERVICE" backup-mailto ops@example.com,dba@example.com
  assert_success
  assert_crontab_regenerated

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-mailto
  assert_success
  assert_output "ops@example.com,dba@example.com"

  # dokku versions that read json entries are handed the mailto
  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local json
  assert_success
  assert_output "{\"schedule\":\"@daily\",\"command\":\"dokku $PLUGIN:backup $SERVICE my-bucket\",\"log-file\":\"$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log\",\"mailto\":\"ops@example.com,dba@example.com\"}"
  run jq -c . <<<"$output"
  assert_success

  # which is what is printed, whatever format a person asked for
  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local json --format json
  assert_success
  run jq -r '.mailto' <<<"$output"
  assert_success
  assert_output "ops@example.com,dba@example.com"

  # and those that do not are handed the line they read, and told it is ignored
  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  assert_output "@daily;dokku $PLUGIN:backup $SERVICE my-bucket;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"
  assert_stderr --partial "backup-mailto for $SERVICE is ignored"

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local --format json
  assert_success
  run jq -r '.[0].mailto' <<<"$output"
  assert_success
  assert_output "ops@example.com,dba@example.com"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE"
  assert_success
  assert_output "@daily dokku $PLUGIN:backup $SERVICE my-bucket 2>&1 | tee -a $DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE" --format json
  assert_success
  run jq -r '.mailto' <<<"$output"
  assert_success
  assert_output "ops@example.com,dba@example.com"

  # scheduling again keeps the mailto the service was set with
  schedule_backup "$SERVICE" "0 3 * * *" my-bucket
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-mailto
  assert_success
  assert_output "ops@example.com,dba@example.com"

  # and unsetting it has dokku write the crontab without it
  : >"$PLUGN_LOG"
  run "$BIN" set "$PLUGIN" "$SERVICE" backup-mailto
  assert_success
  assert_crontab_regenerated

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local json
  assert_success
  assert_output "{\"schedule\":\"0 3 * * *\",\"command\":\"dokku $PLUGIN:backup $SERVICE my-bucket\",\"log-file\":\"$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log\"}"
}

@test "($DEFINITION) a mailto set on a service without a scheduled backup leaves the crontab alone" {
  schedule_backup "$SERVICE" "@daily" my-bucket
  assert_success
  run "$BIN" backup-unschedule "$PLUGIN" "$SERVICE"
  assert_success

  : >"$PLUGN_LOG"
  run "$BIN" set "$PLUGIN" "$SERVICE" backup-mailto ops@example.com
  assert_success
  run cat "$PLUGN_LOG"
  assert_output ""

  # and it applies to the backup the service is scheduled with next
  schedule_backup "$SERVICE" "@daily" my-bucket
  assert_success

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local json
  assert_success
  run jq -r '.mailto' <<<"$output"
  assert_success
  assert_output "ops@example.com"

  # unscheduling keeps it, since it belongs to the service
  run "$BIN" backup-unschedule "$PLUGIN" "$SERVICE"
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-mailto
  assert_success
  assert_output "ops@example.com"

  run "$BIN" set "$PLUGIN" "$SERVICE" backup-mailto
  assert_success
}

@test "($DEFINITION) a mailto cron cannot write is refused" {
  run "$BIN" set "$PLUGIN" "$SERVICE" backup-mailto ops@example.com
  assert_success

  for value in "ops@example.com;true" "ops@example.com, dba@example.com" "ops@example.com,"; do
    run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" backup-mailto "$value"
    assert_failure
    assert_stderr --partial "backup-mailto"
  done

  # and the mailto the service already had is kept
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-mailto
  assert_success
  assert_output "ops@example.com"

  run "$BIN" set "$PLUGIN" "$SERVICE" backup-mailto
  assert_success
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
  assert_output "0 3 * * *;dokku $PLUGIN:backup $SERVICE my-bucket;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"
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

  # as cron would have left it after a run
  local log_file="$DOKKU_LOGS_DIR/$PLUGIN.$DESTROYED_SERVICE.backup.log"
  echo "a backup that ran" >"$log_file"

  : >"$PLUGN_LOG"
  run "$BIN" destroy "$PLUGIN" "$DESTROYED_SERVICE" --force
  assert_success
  assert_crontab_regenerated

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  refute_output --partial "$DESTROYED_SERVICE"

  # a service created later under the same name starts without its output
  [[ ! -e "$log_file" ]]
}

@test "($DEFINITION) the log of a service's scheduled backups is shown" {
  local log_file="$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"
  rm -f "$log_file"

  run --separate-stderr "$BIN" backup-logs "$PLUGIN" "$SERVICE"
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup-logs"
  fi
  assert_failure
  assert_stderr --partial "no scheduled backup of $SERVICE has been logged yet"

  # the most recent lines, as cron appends them
  seq 1 150 >"$log_file"
  run --separate-stderr "$BIN" backup-logs "$PLUGIN" "$SERVICE"
  assert_success
  assert_output "$(seq 51 150)"

  # another service's backups are logged apart from these
  seq 1 5 >"$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE-other.backup.log"
  run --separate-stderr "$BIN" backup-logs "$PLUGIN" "$SERVICE"
  assert_success
  assert_output "$(seq 51 150)"

  run --separate-stderr "$BIN" backup-logs "$PLUGIN" "$SERVICE-missing"
  assert_failure
  assert_stderr --partial "does not exist"

  rm -f "$log_file" "$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE-other.backup.log"
}
