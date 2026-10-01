#!/usr/bin/env bats
# A rabbitmq service made before rabbitmq served tls has no certificate, no tls
# settings and a port file holding the four ports it was exposed on. These make
# one the way it was made then, on the definition as it was, and check that it
# is served tls once its container is made again, keeping its data and the
# ports it was exposed on.

load ../test_helper

RESTARTED="$SERVICE-restarted"

setup_file() {
  datastore_setup_file
  [[ "$DEFINITION" == "rabbitmq" ]] || return 0

  # a plugin checkout holding the definition as it was, which the binary runs
  # in place of its own while the checkout is named. The image is this
  # definition's, so nothing is fetched that the rest of the run does not
  export BEFORE_TLS_BASE="$BATS_FILE_TMPDIR/plugin-base"
  local checkout="$BEFORE_TLS_BASE/rabbitmq/datastore/rabbitmq"
  mkdir -p "$checkout"
  cp "$REPO_ROOT/tests/fixtures/rabbitmq-pre-tls/docker-compose.yml" "$checkout/docker-compose.yml"
  cp "$DEFINITION_ROOT/Dockerfile" "$checkout/Dockerfile"
}

teardown_file() {
  datastore_teardown_file "$SERVICE" "$RESTARTED"
}

setup() {
  if [[ "$DEFINITION" != "rabbitmq" ]]; then
    skip "$DEFINITION has no definition from before it served tls"
  fi
}

# runs the binary on the definition from before rabbitmq served tls. The
# service keeps the definition name it is pinned to, so the binary's own
# definition is what it runs on once the checkout is no longer named
before_tls() {
  PLUGIN_BASE_PATH="$BEFORE_TLS_BASE" PLUGIN_COMMAND_PREFIX=rabbitmq "$BIN" "$@"
}

ports_in() {
  wc -w <"$(service_root "$1")/PORT" | tr -d ' '
}

@test "($DEFINITION) a service made before tls is served it once its container is made again" {
  local port
  run before_tls create "$PLUGIN" "$SERVICE" --image "$IMAGE" --image-version "$IMAGE_VERSION"
  assert_success
  run before_tls expose "$PLUGIN" "$SERVICE"
  assert_success
  run "$(probe_path)" write "$SERVICE"
  assert_success

  # made the way a service was before tls
  [[ ! -e "$(service_root)/certs" ]] || fail "expected no certs directory before tls"
  [[ ! -e "$(service_root)/config/tls.conf" ]] || fail "expected no tls settings before tls"
  assert_equal "$(ports_in "$SERVICE")" 4
  run --separate-stderr tls_handshake 5671
  assert_failure

  # a stop takes the container away, and the start makes it on the definition
  # the binary ships
  run rebuild_service
  assert_success

  [[ -f "$(service_root)/certs/server.crt" ]] || fail "expected the certificate to be made"
  [[ -f "$(service_root)/config/tls.conf" ]] || fail "expected the tls settings to be seeded"

  run --separate-stderr "$BIN" invoke "$PLUGIN" certificate "$SERVICE"
  assert_success
  assert_output "$(cat "$(service_root)/certs/server.crt")"

  "$BIN" invoke "$PLUGIN" certificate "$SERVICE" | trust
  for port in 5671 15671; do
    run --separate-stderr tls_handshake "$port"
    assert_success
    assert_output --partial "Verify return code: 0 (ok)"
  done

  # the plain listeners are still there, and so is what was written to them
  run --separate-stderr amqp_greeting 5672
  assert_success
  assert_output --partial "RabbitMQ"
  run --separate-stderr http_status http://127.0.0.1:15672/
  assert_success
  assert_output "200"
  run --separate-stderr "$(probe_path)" read "$SERVICE"
  assert_success
  assert_output "known"

  # still exposed on the four ports it was, with its tls ports left unexposed
  assert_equal "$(ports_in "$SERVICE")" 4
  run container_inspect "$(service_container).ambassador" '{{ len .HostConfig.PortBindings }}'
  assert_success
  assert_output "4"

  run "$BIN" set "$PLUGIN" "$SERVICE" expose-host dsn.example.com
  assert_success
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --exposed-dsn
  assert_success
  assert_output --partial "@dsn.example.com:$(awk '{ print $1 }' "$(service_root)/PORT")/"
}

@test "($DEFINITION) a restart keeps the container a service made before tls was made with" {
  run before_tls create "$PLUGIN" "$RESTARTED" --image "$IMAGE" --image-version "$IMAGE_VERSION"
  assert_success

  # a restart starts the container it has, which was made without tls
  run "$BIN" restart "$PLUGIN" "$RESTARTED"
  assert_success
  [[ ! -e "$(service_root "$RESTARTED")/certs" ]] || fail "expected a restart to make no certificate"
  run --separate-stderr tls_handshake 5671 "$RESTARTED"
  assert_failure

  # which is why the readme says to stop and start it
  run rebuild_service "$RESTARTED"
  assert_success
  "$BIN" invoke "$PLUGIN" certificate "$RESTARTED" | trust "$RESTARTED"
  run --separate-stderr tls_handshake 5671 "$RESTARTED"
  assert_success
  assert_output --partial "Verify return code: 0 (ok)"
}
