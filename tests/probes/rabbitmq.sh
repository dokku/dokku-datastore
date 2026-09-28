#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
#
# The record is a persistent message in a durable queue, which is what rabbitmq
# keeps on disk across a restart.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.rabbitmq.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/rabbitmq/$SERVICE/PASSWORD")"
# the vhost the service was created with, which is named after its database
VHOST="$(cat "$DOKKU_LIB_ROOT/services/rabbitmq/$SERVICE/DATABASE_NAME")"
API="http://127.0.0.1:15672/api"

# through the management api, from a client beside the service in its network
# namespace, as the account the service was created with
rabbit() {
  docker container run --rm --network "container:$CONTAINER" curlimages/curl:8.16.0 -fsS \
    -u "$SERVICE:$PASSWORD" -H 'Content-Type: application/json' "$@"
}

# the management api starts after the broker, so every action waits for it
for _ in $(seq 1 60); do
  rabbit "$API/overview" >/dev/null 2>&1 && break
  sleep 1
done

# the queue holds the one message, so writing empties it first
put() {
  rabbit -X PUT "$API/queues/$VHOST/probe" -d '{"durable":true}' >/dev/null
  rabbit -X DELETE "$API/queues/$VHOST/probe/contents" >/dev/null
  rabbit -X POST "$API/exchanges/$VHOST/amq.default/publish" \
    -d "{\"properties\":{\"delivery_mode\":2},\"routing_key\":\"probe\",\"payload\":\"$1\",\"payload_encoding\":\"string\"}" >/dev/null
}

case "$ACTION" in
write)
  put known
  ;;
clobber)
  put clobbered
  ;;
read)
  # put back on the queue once read, so reading leaves it as it was
  rabbit -X POST "$API/queues/$VHOST/probe/get" -d '{"count":1,"ackmode":"ack_requeue_true","encoding":"auto"}' |
    sed -n 's/.*"payload":"\([^"]*\)".*/\1/p'
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
