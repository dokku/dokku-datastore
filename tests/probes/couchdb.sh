#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.couchdb.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/couchdb/$SERVICE/PASSWORD")"
# the database the service was created with, which is the service name with
# anything a datastore would refuse in one replaced, rather than the name itself
DATABASE="$(cat "$DOKKU_LIB_ROOT/services/couchdb/$SERVICE/DATABASE_NAME")"

couch() {
  docker container exec "$CONTAINER" curl -s -u "$SERVICE:$PASSWORD" "$@"
}

case "$ACTION" in
write)
  couch -X PUT "http://127.0.0.1:5984/$DATABASE/probe" \
    -H 'Content-Type: application/json' -d '{"value":"known"}' >/dev/null
  ;;
clobber)
  rev="$(couch "http://127.0.0.1:5984/$DATABASE/probe" | sed 's/.*"_rev":"\([^"]*\)".*/\1/')"
  couch -X PUT "http://127.0.0.1:5984/$DATABASE/probe?rev=$rev" \
    -H 'Content-Type: application/json' -d '{"value":"clobbered"}' >/dev/null
  ;;
read)
  couch "http://127.0.0.1:5984/$DATABASE/probe" | sed 's/.*"value":"\([^"]*\)".*/\1/'
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
