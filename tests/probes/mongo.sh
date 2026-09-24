#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read> <service>}"
CONTAINER="dokku.mongo.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/mongo/$SERVICE/PASSWORD")"
# the database the service was created with, which is the service name with
# anything a datastore would refuse in one replaced, rather than the name itself
DATABASE="$(cat "$DOKKU_LIB_ROOT/services/mongo/$SERVICE/DATABASE_NAME")"

mongo_eval() {
  docker container exec -i "$CONTAINER" mongosh \
    -u "$SERVICE" -p "$PASSWORD" --authenticationDatabase "$DATABASE" \
    "$DATABASE" --quiet --eval "$1"
}

case "$ACTION" in
write)
  mongo_eval 'db.probe.deleteMany({}); db.probe.insertOne({value: "known"})' >/dev/null
  ;;
clobber)
  # $set is mongo's operator rather than a shell variable, so it stays unexpanded
  # shellcheck disable=SC2016
  mongo_eval 'db.probe.updateOne({}, {$set: {value: "clobbered"}})' >/dev/null
  ;;
read)
  mongo_eval 'db.probe.findOne().value'
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
