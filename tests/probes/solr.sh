#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
#
# Shared by solr-7 and solr-8, whose update and get apis are the same.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.solr.$SERVICE"
# the core the service was created with, which is named after its database
CORE="$(cat "$DOKKU_LIB_ROOT/services/solr/$SERVICE/DATABASE_NAME")"

# from a client beside the service, in its network namespace, since the image
# is not one to rely on for a client of its own
solr() {
  docker container run --rm --network "container:$CONTAINER" curlimages/curl:8.16.0 -fsS "$@"
}

# the port answers before the core is loaded, and a request made then is
# refused, so every action waits for the core first
for _ in $(seq 1 60); do
  solr "http://127.0.0.1:8983/solr/$CORE/admin/ping" >/dev/null 2>&1 && break
  sleep 1
done

# committed before it returns, which is what puts it on disk rather than only
# in the update log
put() {
  solr -X POST "http://127.0.0.1:8983/solr/$CORE/update?commit=true" \
    -H 'Content-Type: application/json' -d "[{\"id\":\"probe\",\"value_s\":\"$1\"}]" >/dev/null
}

case "$ACTION" in
write)
  put known
  ;;
clobber)
  put clobbered
  ;;
read)
  solr "http://127.0.0.1:8983/solr/$CORE/get?id=probe&wt=json" | tr -d ' \n' | sed -n 's/.*"value_s":"\([^"]*\)".*/\1/p'
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
