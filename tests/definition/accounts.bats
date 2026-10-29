#!/usr/bin/env bats
# Exercises the databases and users a service holds beside its own, and the apps
# linked to them.

load ../test_helper

APP="ci-accounts-app"

# the config variable a link sets by default
ALIAS="$(awk '/^  alias:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")_URL"

# whether the definition makes databases, the number it has from the start
# when it does not, and whether it holds users. Read from the blocks at the top
# of x-dokku, which no other key is indented the same as
MAKES_DATABASES="$(awk '/^  databases:/ { found = 1; next } found && /^  [a-z_]+:/ { exit } found && /^    create:/ { print "true"; exit }' "$DEFINITION_ROOT/docker-compose.yml")"
FIXED_DATABASES="$(awk '/^  databases:/ { found = 1; next } found && /^  [a-z_]+:/ { exit } found && /^    fixed:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")"
HAS_USERS="$(awk '/^  users:/ { print "true"; exit }' "$DEFINITION_ROOT/docker-compose.yml")"

setup_file() {
  datastore_setup_file
  fake_dokku_setup "$APP"
  create_service "$SERVICE"
}

teardown_file() {
  # a failed test can leave the app linked, and a linked service is not
  # destroyed, which would leave its container behind
  "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart >/dev/null 2>/dev/null || true
  datastore_teardown_file "$SERVICE-copy" "$SERVICE"
}

setup() {
  echo '{}' >"$FAKE_CONFIG_ROOT/$APP.json"
}

accounts_or_skip() {
  [[ -n "$MAKES_DATABASES$FIXED_DATABASES$HAS_USERS" ]] || skip "$DEFINITION holds no databases or users beside its own"
}

makes_databases_or_skip() {
  [[ "$MAKES_DATABASES" == "true" ]] || skip "$DEFINITION makes no databases"
}

users_or_skip() {
  [[ "$HAS_USERS" == "true" ]] || skip "$DEFINITION holds no users"
}

# the database a probe addresses for the service's own: the one named for it,
# or the default index of a fixed set
default_database() {
  if [[ -n "$FIXED_DATABASES" ]]; then
    echo 0
    return
  fi

  cat "$(service_root "${1:-$SERVICE}")/DATABASE_NAME"
}

# the user and password a probe connects as for the service's own account, and
# a placeholder for a datastore with no users
default_user() {
  if [[ "$HAS_USERS" != "true" ]]; then
    echo -
    return
  fi

  "$BIN" info "$PLUGIN" "${1:-$SERVICE}" --users 2>/dev/null | awk '{ print $1 }'
}

default_password() {
  if [[ "$HAS_USERS" != "true" ]]; then
    echo -
    return
  fi

  cat "$(service_root "${1:-$SERVICE}")/PASSWORD"
}

user_password() {
  cat "$(service_root "${2:-$SERVICE}")/auth/$1"
}

app_url() {
  jq -r --arg key "$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
}

@test "($DEFINITION) a datastore without them has no databases or users commands" {
  if [[ "$MAKES_DATABASES" != "true" ]]; then
    run "$BIN" create-database "$PLUGIN" "$SERVICE" paintshop
    assert_failure "$NOT_IMPLEMENTED_EXIT"

    run "$BIN" destroy-database "$PLUGIN" "$SERVICE" paintshop --force
    assert_failure "$NOT_IMPLEMENTED_EXIT"
  fi

  if [[ "$HAS_USERS" != "true" ]]; then
    run "$BIN" create-user "$PLUGIN" "$SERVICE" painter
    assert_failure "$NOT_IMPLEMENTED_EXIT"

    run "$BIN" set-user-password "$PLUGIN" "$SERVICE" painter
    assert_failure "$NOT_IMPLEMENTED_EXIT"

    run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --user painter
    assert_failure
    assert_output --partial "does not support linking or addressing a user"
  fi

  if [[ -z "$MAKES_DATABASES$FIXED_DATABASES" ]]; then
    run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --database paintshop
    assert_failure
    assert_output --partial "does not support linking or addressing a database"
  fi
}

@test "($DEFINITION) an app is linked to a database made in the service" {
  makes_databases_or_skip

  run "$BIN" create-database "$PLUGIN" "$SERVICE" paintshop
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --databases
  assert_success
  assert_output "$(default_database) paintshop"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --database paintshop
  assert_success

  run app_url
  assert_success
  assert_output --partial "/paintshop"

  run "$(probe_path)" write-as "$SERVICE" "$(default_user)" "$(default_password)" paintshop
  assert_success
  run "$(probe_path)" read-as "$SERVICE" "$(default_user)" "$(default_password)" paintshop
  assert_success
  assert_output "known"

  # a database an app is linked to is not destroyed out from under it
  run "$BIN" destroy-database "$PLUGIN" "$SERVICE" paintshop --force
  assert_failure
  assert_output --partial "linked to app(s): $APP"

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success

  run app_url
  assert_output "null"
}

@test "($DEFINITION) a database made in the service is exported, reset and imported on its own" {
  makes_databases_or_skip
  run "$BIN" info "$PLUGIN" "$SERVICE" --databases
  [[ "$output" == *paintshop* ]] || skip "the previous test made no database"

  run "$(probe_path)" write-as "$SERVICE" "$(default_user)" "$(default_password)" paintshop
  assert_success

  local exported="$BATS_FILE_TMPDIR/paintshop.dump"
  if [[ -n "$(awk '/^    export:/ { print "true"; exit }' "$DEFINITION_ROOT/docker-compose.yml")" ]]; then
    "$BIN" export "$PLUGIN" "$SERVICE" --database paintshop >"$exported"
    [[ -s "$exported" ]]
  fi

  if [[ -n "$(awk '/^    reset:/ { print "true"; exit }' "$DEFINITION_ROOT/docker-compose.yml")" ]]; then
    run "$BIN" reset "$PLUGIN" "$SERVICE" --database paintshop --force
    assert_success

    run "$(probe_path)" read-as "$SERVICE" "$(default_user)" "$(default_password)" paintshop
    refute_output "known"

    # the service's own database is left alone by a reset of another
    run "$(probe_path)" write "$SERVICE"
    assert_success
    run "$BIN" reset "$PLUGIN" "$SERVICE" --force
    assert_success
    run "$BIN" info "$PLUGIN" "$SERVICE" --databases
    assert_output --partial "paintshop"
  fi

  if [[ -s "$exported" ]]; then
    run "$BIN" import "$PLUGIN" "$SERVICE" --database paintshop <"$exported"
    assert_success

    run "$(probe_path)" read-as "$SERVICE" "$(default_user)" "$(default_password)" paintshop
    assert_success
    assert_output "known"
  fi
}

@test "($DEFINITION) an app is linked as a user made in the service" {
  users_or_skip

  run "$BIN" create-user "$PLUGIN" "$SERVICE" painter
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --users
  assert_success
  assert_output "$(default_user) painter"
  refute_output --partial "$(user_password painter)"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --user painter
  assert_success

  run app_url
  assert_success
  assert_output --partial "painter:$(user_password painter)@"

  run "$(probe_path)" write-as "$SERVICE" painter "$(user_password painter)" "$(default_database)"
  assert_success
  run "$(probe_path)" read-as "$SERVICE" painter "$(user_password painter)" "$(default_database)"
  assert_success
  assert_output "known"

  # a new password reaches the app linked as the user
  run "$BIN" set-user-password "$PLUGIN" "$SERVICE" painter easel_2 --no-restart
  assert_success

  run app_url
  assert_output --partial "painter:easel_2@"

  run "$(probe_path)" read-as "$SERVICE" painter easel_2 "$(default_database)"
  assert_success
  assert_output "known"

  # a user an app is linked as is not destroyed out from under it
  run "$BIN" destroy-user "$PLUGIN" "$SERVICE" painter --force
  assert_failure
  assert_output --partial "linked to app(s): $APP"

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
}

@test "($DEFINITION) a user made in the service can use a database made after it" {
  users_or_skip
  makes_databases_or_skip

  run "$BIN" info "$PLUGIN" "$SERVICE" --users
  [[ "$output" == *painter* ]] || skip "the previous test made no user"

  run "$BIN" create-database "$PLUGIN" "$SERVICE" atelier
  assert_success

  # written as the service's own account and read as the user, which only
  # works if what one makes the other can reach
  run "$(probe_path)" write-as "$SERVICE" "$(default_user)" "$(default_password)" atelier
  assert_success
  run "$(probe_path)" read-as "$SERVICE" painter "$(user_password painter)" atelier
  assert_success
  assert_output "known"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --database atelier --user painter
  assert_success

  run app_url
  assert_output --partial "painter:$(user_password painter)@"
  assert_output --partial "/atelier"

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
}

@test "($DEFINITION) connect runs as the user and on the database it is given" {
  users_or_skip
  makes_databases_or_skip

  local statement expected
  case "$PLUGIN" in
  postgres) statement="SELECT current_user || ':' || current_database();" expected="painter:atelier" ;;
  mysql | mariadb) statement="SELECT CONCAT(SUBSTRING_INDEX(CURRENT_USER(), '@', 1), ':', DATABASE());" expected="painter:atelier" ;;
  clickhouse) statement="SELECT concat(currentUser(), ':', currentDatabase())" expected="painter:atelier" ;;
  mongo) statement="db.runCommand({connectionStatus: 1}).authInfo.authenticatedUsers[0].user + ':' + db.getName()" expected="painter:atelier" ;;
  *) skip "no statement reports the account $PLUGIN connects as" ;;
  esac

  run "$BIN" info "$PLUGIN" "$SERVICE" --databases
  [[ "$output" == *atelier* ]] || skip "the previous tests made no database"

  run bash -c "printf '%s\n' \"\$1\" | \"$BIN\" connect \"$PLUGIN\" \"$SERVICE\" --database atelier --user painter" _ "$statement"
  assert_success
  assert_output --partial "$expected"
}

@test "($DEFINITION) databases and users made in the service go once nothing is linked to them" {
  accounts_or_skip

  if [[ "$MAKES_DATABASES" == "true" ]]; then
    for database in paintshop atelier; do
      run "$BIN" info "$PLUGIN" "$SERVICE" --databases
      [[ "$output" == *"$database"* ]] || continue

      run "$BIN" destroy-database "$PLUGIN" "$SERVICE" "$database" --force
      assert_success
    done

    run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --databases
    assert_output "$(default_database)"

    # the service's own is only removed with the service
    run "$BIN" destroy-database "$PLUGIN" "$SERVICE" "$(default_database)" --force
    assert_failure
    assert_output --partial "only removed with the service"
  fi

  if [[ "$HAS_USERS" == "true" ]]; then
    run "$BIN" info "$PLUGIN" "$SERVICE" --users
    if [[ "$output" == *painter* ]]; then
      run "$BIN" destroy-user "$PLUGIN" "$SERVICE" painter --force
      assert_success
    fi

    run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --users
    assert_output "$(default_user)"

    run "$BIN" destroy-user "$PLUGIN" "$SERVICE" "$(default_user)" --force
    assert_failure
    assert_output --partial "only removed with the service"
  fi
}

@test "($DEFINITION) a database or user under a name the service cannot hold is refused" {
  if [[ "$MAKES_DATABASES" == "true" ]]; then
    run "$BIN" create-database "$PLUGIN" "$SERVICE" Paint-Shop
    assert_failure
    assert_output --partial "invalid database name"

    run "$BIN" create-database "$PLUGIN" "$SERVICE" "$(default_database)"
    assert_failure
    assert_output --partial "already has a database"

    local reserved
    reserved="$(reserved_name)"
    if [[ -n "$reserved" ]]; then
      run "$BIN" create-database "$PLUGIN" "$SERVICE" "$reserved"
      assert_failure
    fi

    run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --database nowhere
    assert_failure
    assert_output --partial "has no database nowhere"
  fi

  if [[ "$HAS_USERS" == "true" ]]; then
    run "$BIN" create-user "$PLUGIN" "$SERVICE" "the painter"
    assert_failure
    assert_output --partial "invalid user name"

    run "$BIN" create-user "$PLUGIN" "$SERVICE" sculptor "it's"
    assert_failure
    assert_output --partial "invalid password value"

    run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --user nobody
    assert_failure
    assert_output --partial "has no user nobody"
  fi
}

@test "($DEFINITION) an app is linked to one of a fixed set of databases by index" {
  [[ -n "$FIXED_DATABASES" ]] || skip "$DEFINITION has no fixed set of databases"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --databases
  assert_output "0-$((FIXED_DATABASES - 1))"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --database "$FIXED_DATABASES"
  assert_failure
  assert_output --partial "numbered from 0"

  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart --database 3
  assert_success

  run app_url
  assert_output --regexp "/3$"

  run "$(probe_path)" write-as "$SERVICE" "$(default_user)" "$(default_password)" 3
  assert_success
  run "$(probe_path)" read-as "$SERVICE" "$(default_user)" "$(default_password)" 3
  assert_output "known"

  # every other database is another, which the earlier tests left alone
  run "$(probe_path)" read-as "$SERVICE" "$(default_user)" "$(default_password)" 4
  refute_output "known"

  # a whole-service dump has no one database to name
  run "$BIN" export "$PLUGIN" "$SERVICE" --database 3
  assert_failure
  assert_output --partial "cannot be given --database"

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
}

@test "($DEFINITION) a user made in the service outlives a restart, as the service's own password does" {
  users_or_skip

  run "$BIN" create-user "$PLUGIN" "$SERVICE" restorer
  assert_success
  run "$(probe_path)" write-as "$SERVICE" restorer "$(user_password restorer)" "$(default_database)"
  assert_success

  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success
  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success

  run "$(probe_path)" read-as "$SERVICE" restorer "$(user_password restorer)" "$(default_database)"
  assert_success
  assert_output "known"

  run "$(probe_path)" read-as "$SERVICE" "$(default_user)" "$(default_password)" "$(default_database)"
  assert_success

  run "$BIN" destroy-user "$PLUGIN" "$SERVICE" restorer --force
  assert_success
}

@test "($DEFINITION) a clone carries the databases and users made in its source" {
  accounts_or_skip
  [[ -n "$(awk '/^    export:/ { print "true"; exit }' "$DEFINITION_ROOT/docker-compose.yml")" ]] || skip "$DEFINITION cannot be cloned"

  local database user password
  database="$(default_database)"
  user="$(default_user)"
  password="$(default_password)"

  if [[ "$MAKES_DATABASES" == "true" ]]; then
    run "$BIN" create-database "$PLUGIN" "$SERVICE" gallery
    assert_success
    database=gallery
  fi

  if [[ "$HAS_USERS" == "true" ]]; then
    run "$BIN" create-user "$PLUGIN" "$SERVICE" curator
    assert_success
    user=curator
    password="$(user_password curator)"
  fi

  run "$(probe_path)" write-as "$SERVICE" "$user" "$password" "$database"
  assert_success

  run "$BIN" clone "$PLUGIN" "$SERVICE" "$SERVICE-copy"
  assert_success

  # the user keeps its password, so an app relinked to the clone as it keeps
  # working with what it already holds
  if [[ "$HAS_USERS" == "true" ]]; then
    run cat "$(service_root "$SERVICE-copy")/auth/curator"
    assert_output "$password"
  fi

  if [[ "$MAKES_DATABASES" != "true" ]]; then
    database="$(default_database "$SERVICE-copy")"
  fi

  run "$(probe_path)" read-as "$SERVICE-copy" "$user" "$password" "$database"
  assert_success
  assert_output "known"
}
