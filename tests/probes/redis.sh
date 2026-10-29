#!/usr/bin/env bash
# Writes and reads back a known record, so that the export and import round trip
# proves the data survived rather than only that the service came back up. An
# import that silently truncated would leave a running service either way.
#
# One of these per definition, named for it. A definition without one still gets
# every other check.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read|write-as|read-as> <service> [<user> <password> <database>]}"
SERVICE="${2:?usage: $0 <write|clobber|read|write-as|read-as> <service> [<user> <password> <database>]}"
CONTAINER="dokku.redis.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/redis/$SERVICE/PASSWORD")"

cli() {
  docker container exec --env "REDISCLI_AUTH=$PASSWORD" "$CONTAINER" redis-cli --no-auth-warning "$@"
}

# the -as actions do the same as a user, on a database, each named rather than
# the service's own, which is how a database or user made in the service is
# shown to be one a linked app can reach
AS_USER="${3:-}"
AS_PASSWORD="${4:-}"
AS_DATABASE="${5:-}"

cli_as() {
  docker container exec --env "REDISCLI_AUTH=$AS_PASSWORD" "$CONTAINER" redis-cli --no-auth-warning --user "$AS_USER" -n "$AS_DATABASE" "$@"
}

case "$ACTION" in
write)
  cli SET probe known >/dev/null
  ;;
clobber)
  cli SET probe clobbered >/dev/null
  ;;
read)
  cli GET probe
  ;;
write-as)
  cli_as SET probe_account known >/dev/null
  ;;
read-as)
  cli_as GET probe_account
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
