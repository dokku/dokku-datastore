#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read|write-extra|clobber-extra|read-extra> <service>}"
SERVICE="${2:?usage: $0 <write|clobber|read|write-extra|clobber-extra|read-extra> <service>}"
CONTAINER="dokku.mongo.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/mongo/$SERVICE/PASSWORD")"
# the database the service was created with, which is the service name with
# anything a datastore would refuse in one replaced, rather than the name itself
DATABASE="$(cat "$DOKKU_LIB_ROOT/services/mongo/$SERVICE/DATABASE_NAME")"

# the -extra actions do the same in a second database the service holds, which
# only an export of every database carries
# the service's account can only reach its own database, so the second one is
# reached as the admin account
EXTRA_DATABASE="probe_extra"
ROOT_PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/mongo/$SERVICE/ROOTPASSWORD")"

mongo_admin_eval() {
  docker container exec -i "$CONTAINER" mongosh \
    -u admin -p "$ROOT_PASSWORD" --authenticationDatabase admin \
    "$EXTRA_DATABASE" --quiet --eval "$1"
}

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
write-extra)
  mongo_admin_eval 'db.probe.deleteMany({}); db.probe.insertOne({value: "known"})' >/dev/null
  ;;
clobber-extra)
  # shellcheck disable=SC2016
  mongo_admin_eval 'db.probe.updateOne({}, {$set: {value: "clobbered"}})' >/dev/null
  ;;
read-extra)
  mongo_admin_eval 'db.probe.findOne().value'
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
