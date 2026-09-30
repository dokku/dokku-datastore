#!/usr/bin/env bats
# A postgres service's database is made by the image the first time it starts,
# with the encoding and locale of the environment it starts in. The bash plugin
# made it itself with a fixed encoding, so a custom env that set another locale
# left the service with no database at all. These check that such a service
# still has its database, and that the readme's way of choosing the encoding
# and locale is the one the database is made with.

load ../test_helper

setup_file() {
  datastore_setup_file
}

teardown_file() {
  datastore_teardown_file "$SERVICE-c" "$SERVICE-utf8"
}

setup() {
  if [[ "$PLUGIN" != "postgres" ]]; then
    skip "$PLUGIN does not make its database with initdb"
  fi
}

# the encoding, collation and ctype of the named service's database, read from
# the catalog over the database the image always makes, so a missing database
# is an empty result rather than a failure to connect
database_of() {
  local password database
  password="$(cat "$(service_root "$1")/PASSWORD")"
  database="$(cat "$(service_root "$1")/DATABASE_NAME")"
  docker container exec --env "PGPASSWORD=$password" "$(service_container "$1")" \
    psql -qtAX -h localhost -U postgres -d postgres \
    -c "SELECT pg_encoding_to_char(encoding), datcollate, datctype FROM pg_database WHERE datname = '$database';"
}

@test "($DEFINITION) a service created with LC_ALL=C still has its database" {
  run "$BIN" create "$PLUGIN" "$SERVICE-c" --image "$IMAGE" --image-version "$IMAGE_VERSION" --custom-env "LC_ALL=C"
  assert_success

  # the locale the readme warns about: the database exists, in the encoding
  # the cluster was made with rather than the utf8 the bash plugin asked for
  run --separate-stderr database_of "$SERVICE-c"
  assert_success
  assert_output "SQL_ASCII|C|C"
}

@test "($DEFINITION) POSTGRES_INITDB_ARGS chooses the database encoding and locale" {
  run "$BIN" create "$PLUGIN" "$SERVICE-utf8" --image "$IMAGE" --image-version "$IMAGE_VERSION" --custom-env "POSTGRES_INITDB_ARGS=--encoding=UTF8 --locale=C"
  assert_success

  run --separate-stderr database_of "$SERVICE-utf8"
  assert_success
  assert_output "UTF8|C|C"

  # read back whole, since the arguments hold a space and the custom env is
  # only split on semicolons
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE-utf8" --custom-env
  assert_success
  assert_output "POSTGRES_INITDB_ARGS=--encoding=UTF8 --locale=C"
}
