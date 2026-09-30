#!/usr/bin/env bats
# Every postgres service encrypts the connections its clients ask it to, with a
# certificate made when the service was created. These check that a client can
# insist on it, can verify the server with the certificate the plugin prints,
# and can swap in a certificate of its own the way the readme says to.

load ../test_helper

setup_file() {
  datastore_setup_file
  if [[ "$PLUGIN" == "postgres" ]]; then
    create_service "$SERVICE"
  fi
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

setup() {
  if [[ "$PLUGIN" != "postgres" ]]; then
    skip "$PLUGIN does not encrypt its connections"
  fi
}

# whether the session a client opens with these connection options is
# encrypted, asked over tcp since the unix socket is never encrypted
ssl_of() {
  local password database
  password="$(cat "$(service_root)/PASSWORD")"
  database="$(cat "$(service_root)/DATABASE_NAME")"
  docker container exec --env "PGPASSWORD=$password" "$(service_container)" \
    psql -qtAX "host=localhost user=postgres dbname=$database $1" \
    -c "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid();"
}

# puts the certificate on stdin where a client in the container can verify the
# server with it
trust() {
  docker container exec -i "$(service_container)" sh -c 'cat > /tmp/trusted.crt'
}

print_certificate() {
  "$BIN" invoke "$PLUGIN" certificate "$SERVICE"
}

# the image the definition makes its certificate in, which is the service's own
# unless its hook names another: the timescaledb image ships no openssl
openssl_image() {
  local image
  image="$(awk '/^      image:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")"
  echo "${image:-$IMAGE:$IMAGE_VERSION}"
}

@test "($DEFINITION) a client that requires tls is given an encrypted connection" {
  run --separate-stderr ssl_of "sslmode=require"
  assert_success
  assert_output "t"
}

@test "($DEFINITION) certificate prints the certificate the service was created with" {
  run --separate-stderr print_certificate
  assert_success
  assert_output "$(cat "$(service_root)/certs/server.crt")"
  assert_line --index 0 "-----BEGIN CERTIFICATE-----"
}

@test "($DEFINITION) a client verifies the server with the certificate it printed" {
  print_certificate | trust

  run --separate-stderr ssl_of "sslmode=verify-ca sslrootcert=/tmp/trusted.crt"
  assert_success
  assert_output "t"

  # the certificate names no host, which is why the readme says verify-ca
  run --separate-stderr ssl_of "sslmode=verify-full sslrootcert=/tmp/trusted.crt"
  assert_failure
}

@test "($DEFINITION) the certificate is kept when the service is rebuilt" {
  local before
  before="$(cat "$(service_root)/certs/server.crt")"

  rebuild_service

  assert_equal "$(cat "$(service_root)/certs/server.crt")" "$before"
  run --separate-stderr print_certificate
  assert_output "$before"
}

@test "($DEFINITION) a certificate written over the service's own is served after a restart" {
  local before
  before="$(cat "$(service_root)/certs/server.crt")"

  # as root, since the key belongs to the server's user, and written over the
  # files rather than replacing them, which keeps the owner and mode it needs
  docker container run --rm --user 0 --entrypoint sh \
    --volume "$(host_service_root)/certs:/certs" "$(openssl_image)" -c '
      openssl req -new -newkey rsa:2048 -x509 -days 1 -nodes -subj /CN=replaced \
        -out /tmp/new.crt -keyout /tmp/new.key >/dev/null 2>&1 &&
      cat /tmp/new.crt >/certs/server.crt &&
      cat /tmp/new.key >/certs/server.key'

  "$BIN" restart "$PLUGIN" "$SERVICE"

  run --separate-stderr print_certificate
  assert_success
  refute_output "$before"
  assert_output "$(cat "$(service_root)/certs/server.crt")"

  print_certificate | trust
  run --separate-stderr ssl_of "sslmode=verify-ca sslrootcert=/tmp/trusted.crt"
  assert_success
  assert_output "t"
}

@test "($DEFINITION) certificate needs the service to be running" {
  "$BIN" stop "$PLUGIN" "$SERVICE"

  run --separate-stderr print_certificate
  assert_failure

  "$BIN" start "$PLUGIN" "$SERVICE"
}
