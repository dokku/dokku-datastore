#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.meilisearch.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/meilisearch/$SERVICE/PASSWORD")"

# from a client beside the service, in its network namespace, since the image
# ships none. The master key is the service password
meili() {
  docker container run --rm --network "container:$CONTAINER" curlimages/curl:8.16.0 -fsS \
    -H "Authorization: Bearer $PASSWORD" "$@"
}

# a document is written by a task the server runs later, so a write waits for
# its task to finish, or a read straight after would find nothing
put() {
  local task status
  task="$(meili -X POST "http://127.0.0.1:7700/indexes/probe/documents?primaryKey=id" \
    -H 'Content-Type: application/json' -d "[{\"id\":\"probe\",\"value\":\"$1\"}]" |
    sed 's/.*"taskUid":\([0-9]*\).*/\1/')"

  for _ in $(seq 1 60); do
    status="$(meili "http://127.0.0.1:7700/tasks/$task" | sed 's/.*"status":"\([^"]*\)".*/\1/')"
    case "$status" in
    succeeded) return 0 ;;
    failed | canceled)
      echo "task $task $status" >&2
      return 1
      ;;
    esac
    sleep 1
  done

  echo "task $task did not finish" >&2
  return 1
}

case "$ACTION" in
write)
  put known
  ;;
clobber)
  put clobbered
  ;;
read)
  meili "http://127.0.0.1:7700/indexes/probe/documents/probe" | sed 's/.*"value":"\([^"]*\)".*/\1/'
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
