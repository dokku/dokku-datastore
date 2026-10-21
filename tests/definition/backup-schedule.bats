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
  assert_output "0 3 * * *;dokku $PLUGIN:backup $SERVICE my-bucket --id default;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE"
  assert_success
  assert_output "0 3 * * * dokku $PLUGIN:backup $SERVICE my-bucket --id default &>> $DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

  # and the same two as json, for something other than dokku to read
  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local --format json
  assert_success
  run jq -r '.[0] | [.schedule, .command, .["log-file"]] | join(";")' <<<"$output"
  assert_success
  assert_output "0 3 * * *;dokku $PLUGIN:backup $SERVICE my-bucket --id default;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE" --format json
  assert_success
  run jq -r '[.schedule, .["bucket-name"], (.["use-iam"] | tostring), .["crontab-line"]] | join("|")' <<<"$output"
  assert_success
  assert_output "0 3 * * *|my-bucket|false|0 3 * * * dokku $PLUGIN:backup $SERVICE my-bucket --id default &>> $DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

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
  assert_output "@daily;dokku $PLUGIN:backup $SERVICE my-bucket --id default --use-iam;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

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
  assert_output "{\"schedule\":\"@daily\",\"command\":\"dokku $PLUGIN:backup $SERVICE my-bucket --id default\",\"log-file\":\"$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log\",\"mailto\":\"ops@example.com,dba@example.com\"}"
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
  assert_output "@daily;dokku $PLUGIN:backup $SERVICE my-bucket --id default;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"
  assert_stderr --partial "backup-mailto for $SERVICE is ignored"

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local --format json
  assert_success
  run jq -r '.[0].mailto' <<<"$output"
  assert_success
  assert_output "ops@example.com,dba@example.com"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE"
  assert_success
  assert_output "@daily dokku $PLUGIN:backup $SERVICE my-bucket --id default 2>&1 | tee -a $DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

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
  assert_output "{\"schedule\":\"0 3 * * *\",\"command\":\"dokku $PLUGIN:backup $SERVICE my-bucket --id default\",\"log-file\":\"$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log\"}"
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
  assert_output "0 3 * * *;dokku $PLUGIN:backup $SERVICE my-bucket --id default;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"
}

@test "($DEFINITION) a bucket that breaks the s3 naming rules is refused" {
  schedule_backup "$SERVICE" "0 3 * * *" my-bucket
  assert_success

  schedule_backup "$SERVICE" "0 3 * * *" My_Bucket
  assert_failure
  assert_output --partial "invalid bucket name"

  schedule_backup "$SERVICE" "0 3 * * *" s3://my-bucket
  assert_failure
  assert_output --partial "without a scheme such as s3://"

  # and the schedule the service already had is kept
  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  assert_output "0 3 * * *;dokku $PLUGIN:backup $SERVICE my-bucket --id default;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"
}

@test "($DEFINITION) a scheduled backup's bucket and iam setting are changed with set" {
  schedule_backup "$SERVICE" "0 3 * * *" my-bucket
  assert_success

  : >"$PLUGN_LOG"
  run "$BIN" set "$PLUGIN" "$SERVICE" backup-bucket other-bucket
  assert_success
  assert_crontab_regenerated

  : >"$PLUGN_LOG"
  run "$BIN" set "$PLUGIN" "$SERVICE" backup-use-iam true
  assert_success
  assert_crontab_regenerated

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  assert_output "0 3 * * *;dokku $PLUGIN:backup $SERVICE other-bucket --id default --use-iam;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

  # the crontab entry cannot be written without a bucket
  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" backup-bucket
  assert_failure
  assert_stderr --partial "backup-unschedule"

  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" backup-bucket My_Bucket
  assert_failure
  assert_stderr --partial "invalid bucket name"

  run "$BIN" set "$PLUGIN" "$SERVICE" backup-use-iam false
  assert_success

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  assert_output "0 3 * * *;dokku $PLUGIN:backup $SERVICE other-bucket --id default;$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

  # with nothing scheduled there is nothing for either to apply to
  run "$BIN" backup-unschedule "$PLUGIN" "$SERVICE"
  assert_success

  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" backup-bucket other-bucket
  assert_failure
  assert_stderr --partial "There is no scheduled backup for $SERVICE"

  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" backup-use-iam true
  assert_failure
  assert_stderr --partial "There is no scheduled backup for $SERVICE"
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

@test "($DEFINITION) a service can have several backup schedules" {
  run "$BIN" backup-unschedule "$PLUGIN" "$SERVICE" --all
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup-schedule"
  fi
  assert_success

  schedule_backup "$SERVICE" "0 3 * * *" my-bucket
  assert_success
  schedule_backup "$SERVICE" "*/15 * * * *" bucket-a --id frequent
  assert_success
  schedule_backup "$SERVICE" "@monthly" bucket-c --id monthly --use-iam
  assert_success

  local log="$DOKKU_LOGS_DIR/$PLUGIN.$SERVICE.backup.log"

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  assert_line "0 3 * * *;dokku $PLUGIN:backup $SERVICE my-bucket --id default;$log"
  assert_line "*/15 * * * *;dokku $PLUGIN:backup $SERVICE bucket-a --id frequent;$log"
  assert_line "@monthly;dokku $PLUGIN:backup $SERVICE bucket-c --id monthly --use-iam;$log"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE"
  assert_success
  assert_line $'default\t0 3 * * * dokku '"$PLUGIN"':backup '"$SERVICE"' my-bucket --id default &>> '"$log"
  assert_line $'frequent\t*/15 * * * * dokku '"$PLUGIN"':backup '"$SERVICE"' bucket-a --id frequent &>> '"$log"
  assert_line $'monthly\t@monthly dokku '"$PLUGIN"':backup '"$SERVICE"' bucket-c --id monthly --use-iam &>> '"$log"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE" --format json
  assert_success
  run jq -r '.schedules[] | [.id, .schedule, .["bucket-name"], (.["use-iam"] | tostring)] | join("|")' <<<"$output"
  assert_success
  assert_line "default|0 3 * * *|my-bucket|false"
  assert_line "frequent|*/15 * * * *|bucket-a|false"
  assert_line "monthly|@monthly|bucket-c|true"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE" --id frequent
  assert_success
  assert_output "*/15 * * * * dokku $PLUGIN:backup $SERVICE bucket-a --id frequent &>> $log"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE" --id frequent --format json
  assert_success
  run jq -r '.id' <<<"$output"
  assert_success
  assert_output "frequent"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-schedule
  assert_success
  assert_output "0 3 * * *"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-schedules
  assert_success
  assert_output "default|0 3 * * *|my-bucket|false;frequent|*/15 * * * *|bucket-a|false;monthly|@monthly|bucket-c|true"

  run "$BIN" set "$PLUGIN" "$SERVICE" backup-storage-class STANDARD_IA --id frequent
  assert_success

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE" --id frequent --format json
  assert_success
  run jq -r '[.["storage-class"], (.overrides | join(","))] | join("|")' <<<"$output"
  assert_success
  assert_output "STANDARD_IA|backup-storage-class"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE" --id monthly --format json
  assert_success
  run jq -r '[.["storage-class"], (.overrides | length | tostring)] | join("|")' <<<"$output"
  assert_success
  assert_output "|0"

  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" backup-bucket other-bucket
  assert_failure
  assert_stderr --partial "another scheduled backup"
  refute_stderr --partial "There is no scheduled backup"

  run --separate-stderr "$BIN" backup-unschedule "$PLUGIN" "$SERVICE"
  assert_failure
  assert_stderr --partial "pass --id or --all"

  run "$BIN" backup-unschedule "$PLUGIN" "$SERVICE" --id frequent
  assert_success

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  refute_line --partial "--id frequent"
  assert_line --partial "--id default"
  assert_line --partial "--id monthly"

  run "$BIN" backup-unschedule "$PLUGIN" "$SERVICE" --all
  assert_success

  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  assert_output ""
}

@test "($DEFINITION) a schedule can store its own backup credentials" {
  run "$BIN" backup-unschedule "$PLUGIN" "$SERVICE" --all
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup-schedule"
  fi
  assert_success

  run "$BIN" backup-auth "$PLUGIN" "$SERVICE" AKIAEXAMPLE wJalrXUtnFEMI us-east-1 s3v4 http://127.0.0.1:9000
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup-auth"
  fi
  assert_success

  schedule_backup "$SERVICE" "@monthly" bucket-c --id monthly
  assert_success

  run "$BIN" backup-auth "$PLUGIN" "$SERVICE" AKIAMONTHLY monthly-secret eu-west-1 --id monthly
  assert_success

  # the service credentials are untouched
  [[ "$(cat "$(service_root)/backup/AWS_ACCESS_KEY_ID")" == "AKIAEXAMPLE" ]] || fail "backup-auth --id replaced the service access key"
  [[ -e "$(service_root)/backup/ENDPOINT_URL" ]] || fail "backup-auth --id removed the service endpoint"

  local service_fingerprint monthly_fingerprint
  service_fingerprint="$(printf '%s\n%s' AKIAEXAMPLE wJalrXUtnFEMI | sha256sum | cut -d' ' -f1)"
  monthly_fingerprint="$(printf '%s\n%s' AKIAMONTHLY monthly-secret | sha256sum | cut -d' ' -f1)"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --backup-auth-fingerprint
  assert_success
  assert_output "$service_fingerprint"

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE" --id monthly --format json
  assert_success
  refute_output --partial "AKIAMONTHLY"
  refute_output --partial "monthly-secret"
  run jq -r '[.["auth-fingerprint"], .["default-region"], .["endpoint-url"], (.overrides | join(","))] | join("|")' <<<"$output"
  assert_success
  assert_output "$monthly_fingerprint|eu-west-1|http://127.0.0.1:9000|backup-access-key-id,backup-secret-access-key,backup-default-region"

  run "$BIN" set "$PLUGIN" "$SERVICE" backup-access-key-id AKIAROTATED --id monthly
  assert_success

  run "$BIN" set "$PLUGIN" "$SERVICE" backup-secret-access-key --id monthly
  assert_failure
  assert_stderr --partial "backup-deauth --id"

  run "$BIN" backup-deauth "$PLUGIN" "$SERVICE" --id monthly
  assert_success

  run --separate-stderr "$BIN" backup-schedule-cat "$PLUGIN" "$SERVICE" --id monthly --format json
  assert_success
  run jq -r '.["auth-fingerprint"]' <<<"$output"
  assert_success
  assert_output "$service_fingerprint"

  [[ -d "$(service_root)/backup" ]] || fail "backup-deauth --id removed the service credentials"

  run "$BIN" backup-deauth "$PLUGIN" "$SERVICE"
  assert_success
  run "$BIN" backup-unschedule "$PLUGIN" "$SERVICE" --all
  assert_success
}
