#!/usr/bin/env bats
# A service exposed directly publishes its ports on its own container rather
# than through an ambassador, so nothing relays a client's connections. Docker
# cannot change what a container publishes, so the container is made again
# whenever that changes, and a running service is only stopped and started for
# it once that has been agreed to.

load ../test_helper

setup_file() {
  datastore_setup_file
  create_service "$SERVICE"
  "$BIN" set "$PLUGIN" "$SERVICE" expose-mode direct
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

setup() {
  AMBASSADOR="$(service_container).ambassador"
  PORT_FILE="$(service_root)/PORT"
}

# the ports the service container publishes, as docker bound them
bindings_of_service() {
  # shellcheck disable=SC2016
  container_inspect "$(service_container)" '{{ range $port, $_ := .HostConfig.PortBindings }}{{ $port }} {{ end }}'
}

# the addresses every port the service container publishes is bound on
addresses_of_service() {
  # shellcheck disable=SC2016
  container_inspect "$(service_container)" '{{ range $port, $bindings := .HostConfig.PortBindings }}{{ range $bindings }}[{{ .HostIp }}]{{ end }}{{ end }}'
}

published_label() {
  container_inspect "$(service_container)" '{{ index .Config.Labels "dokku.service.published-ports" }}'
}

service_id() {
  container_inspect "$(service_container)" '{{ .Id }}'
}

assert_no_ambassador() {
  if docker container inspect "$AMBASSADOR" >/dev/null 2>/dev/null; then
    fail "$1: expected no ambassador, found one"
  fi
}

assert_ambassador_running() {
  local state
  state="$(container_inspect "$AMBASSADOR" '{{ .State.Status }}' 2>/dev/null || true)"
  [[ "$state" == "running" ]] || fail "$1: expected the ambassador to be running, got '$state'"
}

# the service container publishes the port it was exposed on, over the
# protocol its definition declares, says so on its label, and no ambassador is
# fighting it for the port
assert_published() {
  local step="$1" bindings label published host_port
  label="$(published_label)"
  [[ -n "$label" ]] || fail "$step: expected the service container to record the ports it publishes"

  published="$(docker container port "$(service_container)")"
  host_port="$(awk '{ print $1 }' "$PORT_FILE")"
  host_port="${host_port##*:}"
  [[ "$published" == *":$host_port"* ]] || fail "$step: expected port $host_port to be published, got '$published'"

  bindings="$(bindings_of_service)"
  if [[ "$DEFINITION" == "graphite" ]]; then
    [[ " $bindings" == *" 8125/udp "* ]] || fail "$step: expected 8125 to be published over udp, got '$bindings'"
    [[ " $bindings" != *" 8125/tcp "* ]] || fail "$step: expected 8125 not to be published over tcp, got '$bindings'"
  else
    [[ "$bindings" != *"/udp"* ]] || fail "$step: expected nothing published over udp, got '$bindings'"
  fi

  assert_no_ambassador "$step"
}

assert_not_published() {
  local step="$1" label bindings
  label="$(published_label)"
  [[ -z "$label" ]] || fail "$step: expected the service container to publish nothing, got '$label'"

  bindings="$(bindings_of_service)"
  [[ -z "$bindings" ]] || fail "$step: expected no port bindings, got '$bindings'"
}

@test "($DEFINITION) expose asks before stopping and starting a running service" {
  local service_before
  service_before="$(service_id)"

  run "$BIN" expose "$PLUGIN" "$SERVICE" <<<"n"
  assert_failure
  assert_output --partial "Stop and start $SERVICE now?"
  assert_output --partial "nothing was changed"
  [[ ! -e "$PORT_FILE" ]] || fail "expected no port file after the answer was no"
  [[ "$(service_id)" == "$service_before" ]] || fail "expected the service container to be left alone"
  assert_not_published "expose answered no"
  assert_no_ambassador "expose answered no"
}

@test "($DEFINITION) expose --force publishes on the service container" {
  local service_before
  service_before="$(service_id)"

  run "$BIN" expose "$PLUGIN" "$SERVICE" --force </dev/null
  assert_success
  refute_output --partial "Stop and start"
  assert_published "expose --force"
  [[ "$(addresses_of_service)" != *"[127.0.0.1]"* ]] || fail "expected every interface, got '$(addresses_of_service)'"
  [[ "$(service_id)" != "$service_before" ]] || fail "expected the service container to be made again"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --expose-mode
  assert_success
  assert_output "direct"
}

@test "($DEFINITION) a udp port exposed directly takes udp from off the host" {
  [[ "$DEFINITION" == "graphite" ]] || skip "$PLUGIN has no udp port"

  local statsd admin metric="dokku_datastore_direct_$RANDOM" counters=""
  statsd="$(awk '{ print $1 }' "$PORT_FILE")"
  admin="$(awk '{ print $2 }' "$PORT_FILE")"
  statsd="${statsd##*:}"
  admin="${admin##*:}"

  for _ in $(seq 1 30); do
    printf '%s:1|c' "$metric" >"/dev/udp/127.0.0.1/$statsd"
    counters="$(printf 'counters\n' | nc -w 2 127.0.0.1 "$admin" 2>/dev/null || true)"
    [[ "$counters" == *"$metric"* ]] && return 0
    sleep 1
  done

  fail "expected statsd to count $metric sent over udp to port $statsd, got '$counters'"
}

@test "($DEFINITION) reexpose leaves a service exposed directly alone" {
  local service_before
  service_before="$(service_id)"

  # nothing to answer with, so a question would be a refusal
  run "$BIN" reexpose "$PLUGIN" "$SERVICE" </dev/null
  assert_success
  refute_output --partial "Stop and start"
  assert_published "reexpose with nothing changed"
  [[ "$(service_id)" == "$service_before" ]] || fail "expected the service container to be left alone"
}

@test "($DEFINITION) a port-bind-address reaches a service exposed directly once agreed to" {
  local service_before
  service_before="$(service_id)"

  run "$BIN" set "$PLUGIN" "$SERVICE" port-bind-address 127.0.0.1
  assert_success

  run "$BIN" reexpose "$PLUGIN" "$SERVICE" <<<"n"
  assert_failure
  assert_output --partial "nothing was changed"
  [[ "$(service_id)" == "$service_before" ]] || fail "expected the service container to be left alone"
  [[ "$(addresses_of_service)" != *"[127.0.0.1]"* ]] || fail "expected the ports to stay where they were, got '$(addresses_of_service)'"

  run "$BIN" reexpose "$PLUGIN" "$SERVICE" <<<"y"
  assert_success
  assert_published "reexpose on the port-bind-address"
  [[ -z "$(addresses_of_service | sed 's/\[127.0.0.1\]//g')" ]] || fail "expected every port on 127.0.0.1, got '$(addresses_of_service)'"
  [[ "$(service_id)" != "$service_before" ]] || fail "expected the service container to be made again"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --exposed-ports
  assert_success
  assert_output --partial "->127.0.0.1:"

  # and back to every interface
  run "$BIN" set "$PLUGIN" "$SERVICE" port-bind-address
  assert_success
  run "$BIN" reexpose "$PLUGIN" "$SERVICE" --force
  assert_success
  assert_published "reexpose with no port-bind-address"
  [[ "$(addresses_of_service)" != *"[127.0.0.1]"* ]] || fail "expected every interface, got '$(addresses_of_service)'"
}

@test "($DEFINITION) a port-source-range is refused for a service exposed directly" {
  run --separate-stderr "$BIN" set "$PLUGIN" "$SERVICE" port-source-range 10.0.0.0/8
  assert_failure
  assert_stderr --partial "cannot be enforced"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --port-source-range
  assert_success
  assert_output ""
}

@test "($DEFINITION) a service exposed directly survives a stop and a start" {
  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success
  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
  assert_published "stop and start"
}

@test "($DEFINITION) a service exposed directly survives a restart" {
  run "$BIN" restart "$PLUGIN" "$SERVICE"
  assert_success
  assert_published "restart"
}

@test "($DEFINITION) moving between expose modes is only done once agreed to" {
  local service_before
  service_before="$(service_id)"

  run "$BIN" set "$PLUGIN" "$SERVICE" expose-mode ambassador
  assert_success

  # an answer of no leaves the service published the way it was
  run "$BIN" reexpose "$PLUGIN" "$SERVICE" <<<"n"
  assert_failure
  assert_output --partial "nothing was changed"
  [[ "$(service_id)" == "$service_before" ]] || fail "expected the service container to be left alone"
  assert_published "to an ambassador, answered no"

  run "$BIN" reexpose "$PLUGIN" "$SERVICE" --force
  assert_success
  assert_not_published "to an ambassador"
  assert_ambassador_running "to an ambassador"

  run "$BIN" set "$PLUGIN" "$SERVICE" expose-mode direct
  assert_success

  # the ambassador goes on publishing the service until it is agreed to
  run "$BIN" reexpose "$PLUGIN" "$SERVICE" <<<"n"
  assert_failure
  assert_output --partial "nothing was changed"
  assert_not_published "back to direct, answered no"
  assert_ambassador_running "back to direct, answered no"

  # and a start of the running service keeps it too, rather than leaving the
  # service unreachable until it is made again
  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador_running "back to direct, started while running"

  run "$BIN" reexpose "$PLUGIN" "$SERVICE" --force
  assert_success
  assert_published "back to direct"
}

@test "($DEFINITION) a restart applies a change of expose mode" {
  run "$BIN" set "$PLUGIN" "$SERVICE" expose-mode ambassador
  assert_success
  run "$BIN" restart "$PLUGIN" "$SERVICE"
  assert_success
  assert_not_published "restart to an ambassador"
  assert_ambassador_running "restart to an ambassador"

  run "$BIN" set "$PLUGIN" "$SERVICE" expose-mode direct
  assert_success
  run "$BIN" restart "$PLUGIN" "$SERVICE"
  assert_success
  assert_published "restart back to direct"
}

@test "($DEFINITION) unexpose asks before stopping and starting a running service" {
  run "$BIN" unexpose "$PLUGIN" "$SERVICE" <<<"n"
  assert_failure
  assert_output --partial "nothing was changed"
  [[ -s "$PORT_FILE" ]] || fail "expected the port file to be kept after the answer was no"
  assert_published "unexpose answered no"

  run "$BIN" unexpose "$PLUGIN" "$SERVICE" --force
  assert_success
  [[ ! -e "$PORT_FILE" ]] || fail "expected no port file after unexpose"
  assert_not_published "unexpose --force"
  assert_no_ambassador "unexpose --force"
}

@test "($DEFINITION) expose publishes on a stopped service without asking" {
  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success

  run "$BIN" expose "$PLUGIN" "$SERVICE" </dev/null
  assert_success
  refute_output --partial "Stop and start"
  assert_published "expose of a stopped service"

  run "$BIN" unexpose "$PLUGIN" "$SERVICE" --force
  assert_success
  assert_not_published "cleanup"
}
