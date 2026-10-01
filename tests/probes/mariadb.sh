#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read|write-extra|clobber-extra|read-extra> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read|write-extra|clobber-extra|read-extra> <service>}"
CONTAINER="dokku.mariadb.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/mariadb/$SERVICE/ROOTPASSWORD")"
# the database the service was created with, which is the service name with
# anything a datastore would refuse in one replaced, rather than the name itself
DATABASE="$(cat "$DOKKU_LIB_ROOT/services/mariadb/$SERVICE/DATABASE_NAME")"

# the -extra actions do the same in a second database the service holds, which
# only an export of every database carries
EXTRA_DATABASE="probe_extra"

sql() {
  docker container exec --env "MYSQL_PWD=$PASSWORD" -i "$CONTAINER" \
    dokku-mariadb-client --user=root --skip-column-names --batch "$DATABASE" -e "$1"
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
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
