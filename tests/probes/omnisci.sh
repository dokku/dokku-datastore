#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read|write-as|read-as> <service> [<user> <password> <database>]}"
SERVICE="${2:?usage: $0 <write|clobber|read|write-as|read-as> <service> [<user> <password> <database>]}"
CONTAINER="dokku.omnisci.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/omnisci/$SERVICE/PASSWORD")"
# the database the service was created with, which is the service name with
# anything a datastore would refuse in one replaced, rather than the name itself
DATABASE="$(cat "$DOKKU_LIB_ROOT/services/omnisci/$SERVICE/DATABASE_NAME")"

# as the account the dsn names, through the client the image ships, which reads
# its statements from stdin, one to a line, and prints only the rows with -q
sql() {
  docker container exec -i "$CONTAINER" /omnisci/bin/omnisql --db="$DATABASE" --user=omnisci -p "$PASSWORD" -q <<<"$1"
}

# the -as actions do the same as a user, on a database, each named rather than
# the service's own, which is how a database or user made in the service is
# shown to be one a linked app can reach
AS_USER="${3:-}"
AS_PASSWORD="${4:-}"
AS_DATABASE="${5:-}"

as_sql() {
  docker container exec -i "$CONTAINER" /omnisci/bin/omnisql --db="$AS_DATABASE" --user="$AS_USER" -p "$AS_PASSWORD" -q <<<"$1"
}

case "$ACTION" in
write)
  sql $'CREATE TABLE IF NOT EXISTS probe (v TEXT);\nDELETE FROM probe;\nINSERT INTO probe VALUES (\'known\');' >/dev/null
  ;;
clobber)
  sql $'DELETE FROM probe;\nINSERT INTO probe VALUES (\'clobbered\');' >/dev/null
  ;;
read)
  sql "SELECT v FROM probe;"
  ;;
write-as)
  as_sql $'CREATE TABLE IF NOT EXISTS probe_account (v TEXT);\nDELETE FROM probe_account;\nINSERT INTO probe_account VALUES (\'known\');' >/dev/null
  ;;
read-as)
  as_sql "SELECT v FROM probe_account;"
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
