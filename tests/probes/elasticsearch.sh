#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
#
# Shared by elasticsearch-7, -8 and -9, whose document api is the same.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.elasticsearch.$SERVICE"

# from a client beside the service, in its network namespace, since the image
# is not one to rely on for a client of its own
es() {
  docker container run --rm --network "container:$CONTAINER" curlimages/curl:8.16.0 -fsS "$@"
}

# the port answers before the cluster does, and a document written then is
# refused, so every action waits for the cluster to take one first
es "http://127.0.0.1:9200/_cluster/health?wait_for_status=yellow&timeout=60s" >/dev/null

# refreshed before it returns, so that a read straight after sees it
put() {
  es -X PUT "http://127.0.0.1:9200/probe/_doc/probe?refresh=true" \
    -H 'Content-Type: application/json' -d "{\"value\":\"$1\"}" >/dev/null
}

case "$ACTION" in
write)
  put known
  ;;
clobber)
  put clobbered
  ;;
read)
  es "http://127.0.0.1:9200/probe/_doc/probe" | sed 's/.*"value":"\([^"]*\)".*/\1/'
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
