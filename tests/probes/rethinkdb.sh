#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
#
# The image ships no client, and a driver would have to be installed on every
# run, so the queries go through the endpoint the administrative web page runs
# its own queries through. It takes what a driver sends over the wire - an
# eight byte token and the query as json - and answers with the token, a length
# and the response.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.rethinkdb.$SERVICE"
# the database the service was created with, which is the service name with
# anything a datastore would refuse in one replaced, rather than the name itself
DATABASE="$(cat "$DOKKU_LIB_ROOT/services/rethinkdb/$SERVICE/DATABASE_NAME")"

# the terms the queries below use, as the protocol numbers them
START=1
DB=14
TABLE=15
GET=16
INSERT=56
DB_CREATE=57
TABLE_CREATE=60
WAIT=177

curl_() {
  docker container run -i --rm --network "container:$CONTAINER" curlimages/curl:8.16.0 -fsS "$@"
}

# runs one query and prints the response, less the token and length before it
query() {
  local connection
  connection="$(curl_ -X POST "http://127.0.0.1:8080/ajax/reql/open-new-connection")"
  {
    printf '\x01\x00\x00\x00\x00\x00\x00\x00'
    printf '[%s,%s,{}]' "$START" "$1"
  } | curl_ -X POST --data-binary @- "http://127.0.0.1:8080/ajax/reql/?conn_id=$connection" | tail -c +13
}

table="[$TABLE,[[$DB,[\"$DATABASE\"]],\"probe\"]]"

# the database and the table are made on first use. Making either again is
# refused, which is left unchecked since it means it is already there, and the
# table is waited on since one just made takes writes a moment later
put() {
  query "[$DB_CREATE,[\"$DATABASE\"]]" >/dev/null
  query "[$TABLE_CREATE,[[$DB,[\"$DATABASE\"]],\"probe\"]]" >/dev/null
  query "[$WAIT,[$table]]" >/dev/null

  local response
  response="$(query "[$INSERT,[$table,{\"id\":\"probe\",\"value\":\"$1\"}],{\"conflict\":\"replace\"}]")"
  if [[ "$response" != *'"errors":0'* ]]; then
    echo "unable to write the record: $response" >&2
    return 1
  fi
}

case "$ACTION" in
write)
  put known
  ;;
clobber)
  put clobbered
  ;;
read)
  # a table comes back a moment after the server does, so a read straight
  # after a start waits for it rather than finding nothing
  query "[$WAIT,[$table]]" >/dev/null
  query "[$GET,[$table,\"probe\"]]" | sed -n 's/.*"value":"\([^"]*\)".*/\1/p'
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
