#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.couchdb.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/couchdb/$SERVICE/PASSWORD")"

couch() {
  docker container exec "$CONTAINER" curl -s -u "$SERVICE:$PASSWORD" "$@"
}

case "$ACTION" in
write)
  couch -X PUT "http://127.0.0.1:5984/$SERVICE/probe" \
    -H 'Content-Type: application/json' -d '{"value":"known"}' >/dev/null
  ;;
clobber)
  rev="$(couch "http://127.0.0.1:5984/$SERVICE/probe" | sed 's/.*"_rev":"\([^"]*\)".*/\1/')"
  couch -X PUT "http://127.0.0.1:5984/$SERVICE/probe?rev=$rev" \
    -H 'Content-Type: application/json' -d '{"value":"clobbered"}' >/dev/null
  ;;
read)
  couch "http://127.0.0.1:5984/$SERVICE/probe" | sed 's/.*"value":"\([^"]*\)".*/\1/'
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
