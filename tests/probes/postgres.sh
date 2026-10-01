#!/usr/bin/env bash
# Writes and reads back a known record, so the round trip proves the data
# survived rather than only that the service came back up.
set -eo pipefail

ACTION="${1:?usage: $0 <write|clobber|read|cluster|read-cluster|templates|extension|has-extension|hypertable> <service> [extension]}"
SERVICE="${2:?usage: $0 <write|clobber|read|cluster|read-cluster|templates|extension|has-extension|hypertable> <service> [extension]}"
CONTAINER="dokku.postgres.$SERVICE"
PASSWORD="$(cat "$DOKKU_LIB_ROOT/services/postgres/$SERVICE/PASSWORD")"
# the database the service was created with, which is the service name with
# anything a datastore would refuse in one replaced, rather than the name itself
DATABASE="$(cat "$DOKKU_LIB_ROOT/services/postgres/$SERVICE/DATABASE_NAME")"

sql() {
  docker container exec --env "PGPASSWORD=$PASSWORD" -i "$CONTAINER" \
    psql -qtAX -h localhost -U postgres -d "$DATABASE" -c "$1"
}

case "$ACTION" in
write)
  sql "CREATE TABLE IF NOT EXISTS probe (value text); DELETE FROM probe; INSERT INTO probe VALUES ('known');" >/dev/null
  ;;
clobber)
  sql "UPDATE probe SET value = 'clobbered';" >/dev/null
  ;;
read)
  sql "SELECT value FROM probe;"
  ;;
extension)
  # the extension a flavor's image exists to ship, created and then reported
  # back by name, so a flavor running the plain postgres image fails here
  EXTENSION="${3:?usage: $0 extension <service> <extension>}"
  sql "CREATE EXTENSION IF NOT EXISTS $EXTENSION;" >/dev/null
  sql "SELECT extname FROM pg_extension WHERE extname = '$EXTENSION';"
  ;;
has-extension)
  # the same report without creating it first, so that finding it means the
  # database already had it
  EXTENSION="${3:?usage: $0 has-extension <service> <extension>}"
  sql "SELECT extname FROM pg_extension WHERE extname = '$EXTENSION';"
  ;;
cluster)
  # a role and a database of its own beside the service's, holding a known
  # record, which only a migration carrying the whole cluster brings along
  sql "DO \$\$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'probe_role') THEN CREATE ROLE probe_role LOGIN; END IF; END \$\$;" >/dev/null
  if [[ -z "$(sql "SELECT 1 FROM pg_database WHERE datname = 'probe_other';")" ]]; then
    sql "CREATE DATABASE probe_other OWNER probe_role;" >/dev/null
  fi
  DATABASE=probe_other sql "CREATE TABLE IF NOT EXISTS probe (value text); DELETE FROM probe; INSERT INTO probe VALUES ('other');" >/dev/null
  ;;
read-cluster)
  # the database beside the service's, its owner, and the record it holds
  owner="$(sql "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'probe_other';")"
  value="$(DATABASE=probe_other sql "SELECT value FROM probe;")"
  echo "$owner:$value"
  ;;
templates)
  # the databases new ones can be copied from, which an image may add to,
  # one per line. template0 is the cluster's own and never changes
  sql "SELECT datname FROM pg_database WHERE datistemplate AND datname <> 'template0' ORDER BY datname;"
  ;;
hypertable)
  # a table timescaledb partitions by time, which only works when the
  # extension's catalog is whole, reported back by name
  sql "CREATE TABLE IF NOT EXISTS probe_metrics (time timestamptz NOT NULL, value double precision);" >/dev/null
  sql "SELECT create_hypertable('probe_metrics', 'time', if_not_exists => true);" >/dev/null
  sql "SELECT hypertable_name FROM timescaledb_information.hypertables WHERE hypertable_name = 'probe_metrics';"
  ;;
*)
  echo "unknown action $ACTION" >&2
  exit 1
  ;;
esac
