#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read|write-extra|clobber-extra|read-extra|write-as|read-as> <service> [<user> <password> <database>]}"
SERVICE="${2:?usage: $0 <write|clobber|read|write-extra|clobber-extra|read-extra|write-as|read-as> <service> [<user> <password> <database>]}"
CONTAINER="dokku.clickhouse.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/clickhouse/$SERVICE/PASSWORD")"
# the database the service was created with, which is the service name with
# anything a datastore would refuse in one replaced, rather than the name itself
DATABASE="$(cat "$DOKKU_LIB_ROOT/services/clickhouse/$SERVICE/DATABASE_NAME")"

# the -extra actions do the same in a second database the service holds, which
# only an export of every database carries
EXTRA_DATABASE="probe_extra"

# the client reads the password from its environment, and the queries are sent
# as arguments rather than on stdin, which the client reads to the end first
sql() {
  docker container exec --env "CLICKHOUSE_PASSWORD=$PASSWORD" "$CONTAINER" \
    clickhouse client --user "$SERVICE" --database "$DATABASE" --multiquery --query "$1"
}

# the -as actions do the same as a user, on a database, each named rather than
# the service's own, which is how a database or user made in the service is
# shown to be one a linked app can reach
AS_USER="${3:-}"
AS_PASSWORD="${4:-}"
AS_DATABASE="${5:-}"

as_sql() {
  docker container exec --env "CLICKHOUSE_PASSWORD=$AS_PASSWORD" "$CONTAINER" \
    clickhouse client --user "$AS_USER" --database "$AS_DATABASE" --multiquery --query "$1"
}

case "$ACTION" in
write)
  sql "CREATE TABLE IF NOT EXISTS probe (value String) ENGINE = MergeTree ORDER BY value; TRUNCATE TABLE probe; INSERT INTO probe VALUES ('known');" >/dev/null
  ;;
clobber)
  sql "TRUNCATE TABLE probe; INSERT INTO probe VALUES ('clobbered');" >/dev/null
  ;;
read)
  sql "SELECT value FROM probe;"
  ;;
write-extra)
  sql "CREATE DATABASE IF NOT EXISTS $EXTRA_DATABASE; CREATE TABLE IF NOT EXISTS $EXTRA_DATABASE.probe (value String) ENGINE = MergeTree ORDER BY value; TRUNCATE TABLE $EXTRA_DATABASE.probe; INSERT INTO $EXTRA_DATABASE.probe VALUES ('known');" >/dev/null
  ;;
clobber-extra)
  sql "TRUNCATE TABLE $EXTRA_DATABASE.probe; INSERT INTO $EXTRA_DATABASE.probe VALUES ('clobbered');" >/dev/null
  ;;
read-extra)
  sql "SELECT value FROM $EXTRA_DATABASE.probe;"
  ;;
write-as)
  as_sql "CREATE TABLE IF NOT EXISTS probe_account (value String) ENGINE = MergeTree ORDER BY value; TRUNCATE TABLE probe_account; INSERT INTO probe_account VALUES ('known');" >/dev/null
  ;;
read-as)
  as_sql "SELECT value FROM probe_account;"
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
