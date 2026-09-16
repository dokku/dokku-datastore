#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.postgres.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/postgres/$SERVICE/PASSWORD")"

sql() {
  docker container exec --env "PGPASSWORD=$PASSWORD" -i "$CONTAINER" \
    psql -qtAX -h localhost -U postgres -d "$SERVICE" -c "$1"
}

case "$ACTION" in
write)
  sql "CREATE TABLE IF NOT EXISTS probe (value text); DELETE FROM probe; INSERT INTO probe VALUES ('known');" >/dev/null
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
