#!/usr/bin/env bats
# Every postgres and rabbitmq service encrypts the connections its clients ask
# it to, with a certificate made when the service was created. These check that
# a client can insist on it, can verify the server with the certificate the
# plugin prints, and can swap in a certificate of its own the way the readme
# says to.

load ../test_helper

setup_file() {
  datastore_setup_file
  if serves_tls; then
    create_service "$SERVICE"
  fi
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

setup() {
  if ! serves_tls; then
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

# whether a client verifying the server with the certificate it was handed to
# trust gets an encrypted connection: postgres negotiates tls inside its own
# protocol, and rabbitmq serves it from the first byte on its tls ports
verified() {
  case "$PLUGIN" in
  postgres)
    run --separate-stderr ssl_of "sslmode=verify-ca sslrootcert=/tmp/trusted.crt"
    assert_success
    assert_output "t"
    ;;
  rabbitmq)
    local port
    for port in 5671 15671; do
      run --separate-stderr tls_handshake "$port"
      assert_success
      assert_output --partial "Verify return code: 0 (ok)"
    done
    ;;
  esac
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
  [[ "$PLUGIN" == "postgres" ]] || skip "$PLUGIN has no client setting to require tls with"

  run --separate-stderr ssl_of "sslmode=require"
  assert_success
  assert_output "t"
}

@test "($DEFINITION) tls is served beside the plain listeners" {
  [[ "$PLUGIN" == "rabbitmq" ]] || skip "$PLUGIN serves tls on the port it always listened on"

  # amqps and the management interface over https
  print_certificate | trust
  run --separate-stderr tls_handshake 5671
  assert_success
  run --separate-stderr http_status https://127.0.0.1:15671/
  assert_success
  assert_output "200"

  # and the plain listeners a linked app and the management probe still use
  run --separate-stderr amqp_greeting 5672
  assert_success
  assert_output --partial "RabbitMQ"
  run --separate-stderr http_status http://127.0.0.1:15672/
  assert_success
  assert_output "200"
}

@test "($DEFINITION) certificate prints the certificate the service was created with" {
  run --separate-stderr print_certificate
  assert_success
  assert_output "$(cat "$(service_root)/certs/server.crt")"
  assert_line --index 0 "-----BEGIN CERTIFICATE-----"
}

@test "($DEFINITION) a client verifies the server with the certificate it printed" {
  print_certificate | trust

  verified

  case "$PLUGIN" in
  postgres)
    # the certificate names no host, which is why the readme says verify-ca
    run --separate-stderr ssl_of "sslmode=verify-full sslrootcert=/tmp/trusted.crt"
    assert_failure
    ;;
  rabbitmq)
    # and a certificate that is not the server's is refused, so the handshake
    # above verified something
    docker container exec "$(service_container)" sh -c \
      'openssl req -new -newkey rsa:2048 -x509 -days 1 -nodes -subj /CN=other -out /tmp/trusted.crt -keyout /tmp/other.key >/dev/null 2>&1'
    run --separate-stderr tls_handshake 5671
    assert_failure
    ;;
  esac
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
  verified
}

@test "($DEFINITION) certificate needs the service to be running" {
  "$BIN" stop "$PLUGIN" "$SERVICE"

  run --separate-stderr print_certificate
  assert_failure

  "$BIN" start "$PLUGIN" "$SERVICE"
}
