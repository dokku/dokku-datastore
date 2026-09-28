#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.typesense.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/typesense/$SERVICE/PASSWORD")"

# from a client beside the service, in its network namespace, since the image
# ships none. The api key is the service password
typesense() {
  docker container run --rm --network "container:$CONTAINER" curlimages/curl:8.16.0 -sS \
    -H "X-TYPESENSE-API-KEY: $PASSWORD" "$@"
}

# the port answers before the server is ready, and a request made then is
# refused, so every action waits for it first
for _ in $(seq 1 60); do
  typesense "http://127.0.0.1:8108/health" | grep -q '"ok":true' && break
  sleep 1
done

# the collection is made on first use, and making it again is refused, which is
# left unchecked since it means it is already there
put() {
  typesense -X POST "http://127.0.0.1:8108/collections" -H 'Content-Type: application/json' \
    -d '{"name":"probe","fields":[{"name":"value","type":"string"}]}' >/dev/null
  typesense -f -X POST "http://127.0.0.1:8108/collections/probe/documents?action=upsert" \
    -H 'Content-Type: application/json' -d "{\"id\":\"probe\",\"value\":\"$1\"}" >/dev/null
}

case "$ACTION" in
write)
  put known
  ;;
clobber)
  put clobbered
  ;;
read)
  typesense -f "http://127.0.0.1:8108/collections/probe/documents/probe" | sed 's/.*"value":"\([^"]*\)".*/\1/'
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
