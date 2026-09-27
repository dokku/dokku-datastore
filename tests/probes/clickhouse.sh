#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.clickhouse.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/clickhouse/$SERVICE/PASSWORD")"
# the database the service was created with, which is the service name with
# anything a datastore would refuse in one replaced, rather than the name itself
DATABASE="$(cat "$DOKKU_LIB_ROOT/services/clickhouse/$SERVICE/DATABASE_NAME")"

# the client reads the password from its environment, and the queries are sent
# as arguments rather than on stdin, which the client reads to the end first
sql() {
  docker container exec --env "CLICKHOUSE_PASSWORD=$PASSWORD" "$CONTAINER" \
    clickhouse client --user "$SERVICE" --database "$DATABASE" --multiquery --query "$1"
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
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
