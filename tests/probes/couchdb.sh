#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read|write-extra|clobber-extra|read-extra|write-as|read-as> <service> [<user> <password> <database>]}"
SERVICE="${2:?usage: $0 <write|clobber|read|write-extra|clobber-extra|read-extra|write-as|read-as> <service> [<user> <password> <database>]}"
CONTAINER="dokku.couchdb.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/couchdb/$SERVICE/PASSWORD")"
# the database the service was created with, which is the service name with
# anything a datastore would refuse in one replaced, rather than the name itself
DATABASE="$(cat "$DOKKU_LIB_ROOT/services/couchdb/$SERVICE/DATABASE_NAME")"

# the -extra actions do the same in a second database the service holds, which
# only an export of every database carries
EXTRA_DATABASE="probe_extra"

couch() {
  docker container exec "$CONTAINER" curl -s -u "$SERVICE:$PASSWORD" "$@"
}

# the -as actions do the same as a user, on a database, each named rather than
# the service's own, which is how a database or user made in the service is
# shown to be one a linked app can reach
AS_USER="${3:-}"
AS_PASSWORD="${4:-}"
AS_DATABASE="${5:-}"

couch_as() {
  docker container exec "$CONTAINER" curl -s -u "$AS_USER:$AS_PASSWORD" "$@"
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
write-extra)
  # made again rather than written over, so the record has no revision to name
  couch -X DELETE "http://127.0.0.1:5984/$EXTRA_DATABASE" >/dev/null
  couch -X PUT "http://127.0.0.1:5984/$EXTRA_DATABASE" >/dev/null
  couch -X PUT "http://127.0.0.1:5984/$EXTRA_DATABASE/probe" \
    -H 'Content-Type: application/json' -d '{"value":"known"}' >/dev/null
  ;;
clobber-extra)
  rev="$(couch "http://127.0.0.1:5984/$EXTRA_DATABASE/probe" | sed 's/.*"_rev":"\([^"]*\)".*/\1/')"
  couch -X PUT "http://127.0.0.1:5984/$EXTRA_DATABASE/probe?rev=$rev" \
    -H 'Content-Type: application/json' -d '{"value":"clobbered"}' >/dev/null
  ;;
read-extra)
  couch "http://127.0.0.1:5984/$EXTRA_DATABASE/probe" | sed 's/.*"value":"\([^"]*\)".*/\1/'
  ;;
write-as)
  # written over the record if there is one, which has to be named by revision
  rev="$(couch_as "http://127.0.0.1:5984/$AS_DATABASE/probe_account" | sed -n 's/.*"_rev":"\([^"]*\)".*/\1/p')"
  couch_as -X PUT "http://127.0.0.1:5984/$AS_DATABASE/probe_account${rev:+?rev=$rev}" \
    -H 'Content-Type: application/json' -d '{"value":"known"}' >/dev/null
  ;;
read-as)
  couch_as "http://127.0.0.1:5984/$AS_DATABASE/probe_account" | sed -n 's/.*"value":"\([^"]*\)".*/\1/p'
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
