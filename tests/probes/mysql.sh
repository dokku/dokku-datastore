#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read|write-extra|clobber-extra|read-extra|write-as|read-as> <service> [<user> <password> <database>]}"
SERVICE="${2:?usage: $0 <write|clobber|read|write-extra|clobber-extra|read-extra|write-as|read-as> <service> [<user> <password> <database>]}"
CONTAINER="dokku.mysql.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/mysql/$SERVICE/ROOTPASSWORD")"
# the database the service was created with, which is the service name with
# anything a datastore would refuse in one replaced, rather than the name itself
DATABASE="$(cat "$DOKKU_LIB_ROOT/services/mysql/$SERVICE/DATABASE_NAME")"

# the -extra actions do the same in a second database the service holds, which
# only an export of every database carries
EXTRA_DATABASE="probe_extra"

sql() {
  docker container exec --env "MYSQL_PWD=$PASSWORD" -i "$CONTAINER" \
    mysql --user=root --skip-column-names --batch "$DATABASE" -e "$1"
}

# the -as actions do the same as a user, on a database, each named rather than
# the service's own, which is how a database or user made in the service is
# shown to be one a linked app can reach
AS_USER="${3:-}"
AS_PASSWORD="${4:-}"
AS_DATABASE="${5:-}"

as_sql() {
  docker container exec --env "MYSQL_PWD=$AS_PASSWORD" -i "$CONTAINER" \
    mysql --user="$AS_USER" --skip-column-names --batch "$AS_DATABASE" -e "$1"
}

case "$ACTION" in
write)
  sql "CREATE TABLE IF NOT EXISTS probe (value VARCHAR(32)); DELETE FROM probe; INSERT INTO probe VALUES ('known');" >/dev/null
  ;;
clobber)
  sql "UPDATE probe SET value = 'clobbered';" >/dev/null
  ;;
read)
  sql "SELECT value FROM probe;"
  ;;
write-extra)
  sql "CREATE DATABASE IF NOT EXISTS $EXTRA_DATABASE; CREATE TABLE IF NOT EXISTS $EXTRA_DATABASE.probe (value VARCHAR(32)); DELETE FROM $EXTRA_DATABASE.probe; INSERT INTO $EXTRA_DATABASE.probe VALUES ('known');" >/dev/null
  ;;
clobber-extra)
  sql "UPDATE $EXTRA_DATABASE.probe SET value = 'clobbered';" >/dev/null
  ;;
read-extra)
  sql "SELECT value FROM $EXTRA_DATABASE.probe;"
  ;;
write-as)
  as_sql "CREATE TABLE IF NOT EXISTS probe_account (value VARCHAR(32)); DELETE FROM probe_account; INSERT INTO probe_account VALUES ('known');" >/dev/null
  ;;
read-as)
  as_sql "SELECT value FROM probe_account;"
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
