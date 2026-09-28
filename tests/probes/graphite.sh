#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
#
# The record is a metric: known is a value of 1 and clobbered a value of 2, sent
# to carbon and read back from graphite's render api as the newest one it has.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.graphite.$SERVICE"

# the newest value the metric has in the last hour, or nothing
newest() {
  docker container run --rm --network "container:$CONTAINER" curlimages/curl:8.16.0 -fsS \
    "http://127.0.0.1:81/render?target=probe.value&format=json&from=-1h" |
    grep -o '\[[0-9.]*, [0-9]*\]' | tail -n 1 | sed 's/\[\([0-9]*\).*/\1/'
}

# when carbon last wrote the metric's file, as seconds since the epoch, or 0
written_at() {
  docker container exec "$CONTAINER" stat -c %Y /opt/graphite/storage/whisper/probe/value.wsp 2>/dev/null || echo 0
}

# sent in carbon's line protocol from a client beside the service, then waited
# on until carbon has written it to disk. The render api answers from carbon's
# cache as well, but carbon does not write that cache out when it is stopped,
# so a value it had only cached is lost with the container: the last seconds
# of metrics a graphite service took before a stop go with it
put() {
  local sent
  sent="$(date +%s)"
  echo "probe.value $1 $sent" |
    docker container run -i --rm --network "container:$CONTAINER" busybox:1.37.0-uclibc nc -w 2 127.0.0.1 2003

  for _ in $(seq 1 60); do
    [[ "$(newest)" == "$1" && "$(written_at)" -ge "$sent" ]] && return 0
    sleep 1
  done

  echo "carbon did not write the value $1" >&2
  return 1
}

case "$ACTION" in
write)
  put 1
  ;;
clobber)
  # a value sent in the same slot as the last replaces it, and one sent later
  # is newer than it, so either way it is the one read back
  put 2
  ;;
read)
  case "$(newest)" in
  1) echo known ;;
  2) echo clobbered ;;
  esac
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
