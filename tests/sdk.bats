#!/usr/bin/env bats
# Runs one definition's datastore through a plugin serving every datastore under
# a single command, the way dokku ships it: one binary built on the sdk package,
# linked as the plugin's commands file, as each subcommand and as each trigger.
#
# The service it makes is the one the per-datastore binary sees, since both
# keep their services in the same place, and that is what lets a host move from
# one to the other.

load test_helper

APP="ci-sdk-app"

setup_file() {
  datastore_setup_file
  fake_dokku_setup "$APP"

  # the help is compared as text
  export DOKKU_NO_COLOR=1

  # the plugin dokku would ship, linked the way dokku's own go plugins are
  export PLUGIN_DIR="$BATS_FILE_TMPDIR/datastore"
  mkdir -p "$PLUGIN_DIR/subcommands"
  go build -o "$PLUGIN_DIR/plugin" "$REPO_ROOT/tests/sdk/plugin"
  ln -sf plugin "$PLUGIN_DIR/commands"

  local name
  for name in backup-schedule create destroy exists info list; do
    ln -sf ../plugin "$PLUGIN_DIR/subcommands/$name"
  done

  for name in cron-entries install pre-delete; do
    ln -sf plugin "$PLUGIN_DIR/$name"
  done

  "$PLUGIN_DIR/subcommands/create" datastore:create "$PLUGIN" "$SERVICE" \
    --backend "$BACKEND" --image "$IMAGE" --image-version "$IMAGE_VERSION"
}

teardown_file() {
  rm -rf "$PLUGIN_PATH/enabled/$PLUGIN"
  datastore_teardown_file "$SERVICE"
}

setup() {
  : >"$PLUGN_LOG"
}

# enables a per-datastore plugin for this definition's datastore, which is
# what a host that has not moved over yet looks like
enable_datastore_plugin() {
  mkdir -p "$PLUGIN_PATH/enabled/$PLUGIN"
}

disable_datastore_plugin() {
  rm -rf "$PLUGIN_PATH/enabled/$PLUGIN"
}

# schedules a backup through the plugin, skipping the test for a datastore with
# no backups
schedule_backup() {
  run "$PLUGIN_DIR/subcommands/backup-schedule" datastore:backup-schedule "$PLUGIN" "$SERVICE" "0 3 * * *" my-bucket
  if [[ "$status" -eq "$NOT_IMPLEMENTED_EXIT" ]]; then
    skip "$PLUGIN does not implement backup-schedule"
  fi
  assert_success
}

# install is a root step, as dokku runs it: it manages /etc/sudoers.d and
# /usr/local/bin. The environment is kept, and the path with it, so it works on
# this run's data root and reaches this run's fake plugn. The directories every
# datastore is installed into are made first, as a dokku install has them, so
# root does not own them and the data root can be removed afterwards
run_install() {
  mkdir -p "$DOKKU_LIB_ROOT/config" "$DOKKU_LIB_ROOT/data" "$DOKKU_LIB_ROOT/services"

  if [[ "$EUID" -eq 0 ]]; then
    run "$PLUGIN_DIR/install"
  else
    run sudo -n -E env PATH="$PATH" "$PLUGIN_DIR/install"
  fi
}

needs_root() {
  if [[ "$EUID" -ne 0 ]] && ! sudo -n true 2>/dev/null; then
    skip "install needs root"
  fi
}

@test "($DEFINITION) a service made through the plugin is the one the binary sees" {
  run "$BIN" exists "$PLUGIN" "$SERVICE"
  assert_success

  run "$PLUGIN_DIR/subcommands/list" datastore:list "$PLUGIN"
  assert_success
  assert_output --partial "$SERVICE"
}

@test "($DEFINITION) the plugin answers dokku's help" {
  run "$PLUGIN_DIR/commands" help
  assert_success
  assert_output "    datastore, Manage datastore services"

  run "$PLUGIN_DIR/commands" datastore:help "$PLUGIN"
  assert_success
  assert_output --partial "dokku datastore:list $PLUGIN"
  refute_output --partial "dokku $PLUGIN:"

  run "$PLUGIN_DIR/commands" datastore:help "$PLUGIN" create
  assert_success
  assert_output --partial "usage: dokku datastore:create $PLUGIN <service>"
}

@test "($DEFINITION) the plugin leaves another plugin's commands alone" {
  run "$PLUGIN_DIR/commands" "$PLUGIN:create" lollipop
  assert_failure "$NOT_IMPLEMENTED_EXIT"

  run "$PLUGIN_DIR/commands" nonsense
  assert_failure "$NOT_IMPLEMENTED_EXIT"
}

@test "($DEFINITION) a scheduled backup runs through the plugin" {
  schedule_backup

  run --separate-stderr "$PLUGIN_DIR/cron-entries" docker-local
  assert_success
  assert_output --partial "dokku datastore:backup $PLUGIN $SERVICE my-bucket --id default"
  refute_output --partial "dokku $PLUGIN:backup"
}

@test "($DEFINITION) a datastore with its own plugin enabled is left to it" {
  schedule_backup
  enable_datastore_plugin

  run --separate-stderr "$PLUGIN_DIR/cron-entries" docker-local
  assert_success
  refute_output --partial "$SERVICE"

  disable_datastore_plugin
}

@test "($DEFINITION) install takes a datastore over from its own plugin" {
  needs_root
  schedule_backup

  # the per-datastore plugin made the schedule, so its own command is the one
  # in the crontab until dokku writes it again
  enable_datastore_plugin
  run --separate-stderr "$BIN" trigger-cron-entries "$PLUGIN" docker-local
  assert_success
  assert_output --partial "dokku $PLUGIN:backup $SERVICE"

  # a datastore with no services, whose image is not on the host, is not pulled
  local unused="typesense" unused_image="typesense/typesense:30.2"
  if [[ "$PLUGIN" == "typesense" ]]; then
    unused="meilisearch"
    unused_image="getmeili/meilisearch:v1.56.0"
  fi
  local unused_was_present=0
  docker image inspect "$unused_image" >/dev/null 2>&1 && unused_was_present=1

  disable_datastore_plugin
  : >"$PLUGN_LOG"
  run_install
  assert_success

  run cat "$PLUGN_LOG"
  assert_line "trigger scheduler-cron-write"

  run --separate-stderr "$PLUGIN_DIR/cron-entries" docker-local
  assert_success
  assert_output --partial "dokku datastore:backup $PLUGIN $SERVICE my-bucket --id default"

  run "$PLUGIN_DIR/subcommands/list" datastore:list "$PLUGIN"
  assert_success
  assert_output --partial "$SERVICE"

  if [[ "$unused_was_present" -eq 0 ]]; then
    run docker image inspect "$unused_image"
    assert_failure
  fi

  # installed nonetheless, so a first service needs no root step
  [[ -d "$DOKKU_LIB_ROOT/config/$unused" ]]
}
