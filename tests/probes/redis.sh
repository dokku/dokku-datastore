#!/usr/bin/env bash
# Writes and reads back a known record, so that the export and import round trip
# proves the data survived rather than only that the service came back up. An
# import that silently truncated would leave a running service either way.
#
# One of these per definition, named for it. A definition without one still gets
# every other check.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.redis.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/redis/$SERVICE/PASSWORD")"

cli() {
  docker container exec --env "REDISCLI_AUTH=$PASSWORD" "$CONTAINER" redis-cli --no-auth-warning "$@"
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
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
