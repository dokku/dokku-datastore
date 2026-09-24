#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.mysql.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/mysql/$SERVICE/ROOTPASSWORD")"
# the database the service was created with, which is the service name with
# anything a datastore would refuse in one replaced, rather than the name itself
DATABASE="$(cat "$DOKKU_LIB_ROOT/services/mysql/$SERVICE/DATABASE_NAME")"

sql() {
  docker container exec --env "MYSQL_PWD=$PASSWORD" -i "$CONTAINER" \
    mysql --user=root --skip-column-names --batch "$DATABASE" -e "$1"
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
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
