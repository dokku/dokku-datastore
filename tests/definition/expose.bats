#!/usr/bin/env bats
# An exposed service publishes its ports through a second container, the
# ambassador, which forwards to the service over a network they share. It used
# to be linked to the service container instead. Docker refuses to start a
# container linked to one that is not running, which is how an exposed service
# used to fail to come back after a stop and a start, and docker 29 no longer
# hands a linked container the environment the ambassador found the service by.

load ../test_helper

setup_file() {
  datastore_setup_file
  create_service "$SERVICE"
}

teardown_file() {
  datastore_teardown_file "$SERVICE"
}

setup() {
  AMBASSADOR="$(service_container).ambassador"
  PORT_FILE="$(service_root)/PORT"

  # the ports the first expose landed on, kept for the tests that need ports
  # known to be free
  EXPOSED_PORTS_FILE="$BATS_FILE_TMPDIR/exposed-ports"
}

# the ambassador is up, was made by docker-port-forward rather than with a
# legacy link, restarts the way its service does, fronts the container the
# service has now, and publishes the port the service was exposed on
assert_ambassador() {
  local step="$1" expected_restart="${2:-always}" state managed links restart fronted service_id published host_port
  state="$(container_inspect "$AMBASSADOR" '{{ .State.Status }}' 2>/dev/null || true)"
  [[ "$state" == "running" ]] || fail "$step: expected the ambassador to be running, got '$state'"

  managed="$(container_inspect "$AMBASSADOR" '{{ index .Config.Labels "com.dokku.port-forward" }}')"
  [[ "$managed" == "true" ]] || fail "$step: expected the ambassador to be made by docker-port-forward, got '$managed'"

  links="$(container_inspect "$AMBASSADOR" '{{ len .HostConfig.Links }}')"
  [[ "$links" == "0" ]] || fail "$step: expected the ambassador to have no links, got $links"

  restart="$(restart_policy_of "$AMBASSADOR")"
  [[ "${restart%:0}" == "$expected_restart" ]] || fail "$step: expected the ambassador to restart $expected_restart, got '$restart'"

  fronted="$(container_inspect "$AMBASSADOR" '{{ index .Config.Labels "dokku.ambassador.container-id" }}')"
  service_id="$(container_inspect "$(service_container)" '{{ .Id }}')"
  [[ "$fronted" == "$service_id" ]] || fail "$step: expected the ambassador to front $service_id, got '$fronted'"

  # docker's own view of what is published rather than a connection to it: the
  # userland proxy accepts a connection on a published port whether or not
  # anything answers behind it. The port file may name an address as well as a
  # port, and only the port is looked for here
  published="$(docker container port "$AMBASSADOR")"
  host_port="$(awk '{ print $1 }' "$PORT_FILE")"
  host_port="${host_port##*:}"
  [[ "$published" == *":$host_port"* ]] || fail "$step: expected port $host_port to be published, got '$published'"

  assert_protocols "$step"
}

# every port is published and forwarded over the protocol its definition
# declares. Graphite's statsd is the only udp port shipped; the rest are tcp
assert_protocols() {
  local step="$1" expected_udp="" bindings forwarded label port
  [[ "$DEFINITION" == "graphite" ]] && expected_udp="8125"

  label="$(container_inspect "$AMBASSADOR" '{{ index .Config.Labels "dokku.ambassador.udp-ports" }}')"
  [[ "$label" == "$expected_udp" ]] || fail "$step: expected the ambassador to record udp ports '$expected_udp', got '$label'"

  # shellcheck disable=SC2016
  bindings="$(container_inspect "$AMBASSADOR" '{{ range $port, $_ := .HostConfig.PortBindings }}{{ $port }} {{ end }}')"
  forwarded="$(container_inspect "$AMBASSADOR" '{{ index .Config.Labels "com.dokku.port-forward.ports" }}')"

  if [[ -z "$expected_udp" ]]; then
    [[ "$bindings" != *"/udp"* ]] || fail "$step: expected nothing published over udp, got '$bindings'"
    [[ "$forwarded" != *"/udp"* ]] || fail "$step: expected nothing forwarded over udp, got '$forwarded'"
    return
  fi

  for port in ${expected_udp//,/ }; do
    [[ " $bindings" == *" $port/udp "* ]] || fail "$step: expected $port to be published over udp, got '$bindings'"
    [[ " $bindings" != *" $port/tcp "* ]] || fail "$step: expected $port not to be published over tcp, got '$bindings'"
    [[ ",$forwarded," == *":$port/udp,"* ]] || fail "$step: expected $port to be forwarded over udp, got '$forwarded'"
  done
}

# every port the ambassador publishes is bound on one host address, empty for
# every interface, which is what a plain docker --publish binds
assert_bound_on() {
  local step="$1" expected="$2" bound
  # shellcheck disable=SC2016
  bound="$(container_inspect "$AMBASSADOR" '{{ range $port, $bindings := .HostConfig.PortBindings }}{{ range $bindings }}[{{ .HostIp }}]{{ end }}{{ end }}')"
  [[ -n "$bound" && -z "${bound//"[$expected]"/}" ]] || fail "$step: expected every port to be bound on '$expected', got '$bound'"
}

assert_no_ambassador() {
  if docker container inspect "$AMBASSADOR" >/dev/null 2>/dev/null; then
    fail "$1: expected no ambassador, found one"
  fi
}

# the ambassador's id, which changes only when it is replaced
ambassador_id() {
  container_inspect "$AMBASSADOR" '{{ .Id }}'
}

# the service container's id, which a reexpose leaves alone
service_id() {
  container_inspect "$(service_container)" '{{ .Id }}'
}

# the ambassador's range of client addresses, both as it records the property
# and as the socat command it runs enforces it; empty when it has none
assert_source_range() {
  local step="$1" expected="$2" label command
  label="$(container_inspect "$AMBASSADOR" '{{ index .Config.Labels "dokku.ambassador.source-range" }}')"
  [[ "$label" == "$expected" ]] || fail "$step: expected the ambassador to record source range '$expected', got '$label'"

  command="$(container_inspect "$AMBASSADOR" '{{ index .Config.Cmd 0 }}')"
  if [[ -z "$expected" ]]; then
    [[ "$command" != *"range="* ]] || fail "$step: expected no source range in '$command'"
  else
    [[ "$command" == *",range=$expected "* ]] || fail "$step: expected source range $expected in '$command'"
  fi
}

# the dsn a linked app is handed, pointed at a host and at the host port the
# port it names is exposed on: the same scheme, credentials and path, so a
# client off the host authenticates the way the app does
expected_exposed_dsn() {
  local host="$1" dsn rest host_and_port container_port prefix path mapping host_port=""
  dsn="$("$BIN" info "$PLUGIN" "$SERVICE" --dsn)"

  rest="${dsn#*://}"
  prefix="${dsn%%://*}://"
  if [[ "$rest" == *@* ]]; then
    prefix="${dsn%%@*}@"
    rest="${rest#*@}"
  fi

  host_and_port="${rest%%/*}"
  container_port="${host_and_port##*:}"
  path=""
  [[ "$rest" == */* ]] && path="/${rest#*/}"

  for mapping in $("$BIN" info "$PLUGIN" "$SERVICE" --exposed-ports); do
    if [[ "${mapping%%->*}" == "$container_port" ]]; then
      host_port="${mapping#*->}"
      host_port="${host_port##*:}"
    fi
  done
  [[ -n "$host_port" ]] || fail "expected port $container_port to be exposed"

  echo "${prefix}${host}:${host_port}${path}"
}

@test "($DEFINITION) expose publishes the service" {
  run "$BIN" expose "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "expose"
  assert_bound_on "expose" ""

  "$BIN" info "$PLUGIN" "$SERVICE" --exposed-ports >"$EXPOSED_PORTS_FILE"
  [[ -s "$EXPOSED_PORTS_FILE" ]] || fail "expected the exposed ports to be reported"
}

@test "($DEFINITION) an exposed udp port takes udp from off the host" {
  [[ "$DEFINITION" == "graphite" ]] || skip "$PLUGIN has no udp port"

  # statsd, and its admin interface, which lists the counters it has been sent.
  # Sent again on every attempt, since a datagram that arrives before socat is
  # listening is gone rather than refused
  local statsd admin metric="dokku_datastore_expose_$RANDOM" counters=""
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

@test "($DEFINITION) the exposed dsn names the expose-host and the exposed port" {
  run "$BIN" set "$PLUGIN" "$SERVICE" expose-host dsn.example.com
  assert_success

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --exposed-dsn
  assert_success
  assert_output "$(expected_exposed_dsn dsn.example.com)"

  # and back to what every other check expects of this service
  run "$BIN" set "$PLUGIN" "$SERVICE" expose-host
  assert_success
}

@test "($DEFINITION) the exposed dsn names the global domain without an expose-host" {
  echo "dokku.me other.me" >"$BATS_TEST_TMPDIR/VHOST"

  run --separate-stderr env DOKKU_ROOT="$BATS_TEST_TMPDIR" "$BIN" info "$PLUGIN" "$SERVICE" --exposed-dsn
  assert_success
  assert_output "$(expected_exposed_dsn dokku.me)"

  # a host without dokku sets no DOKKU_ROOT, and has no domain to name
  run --separate-stderr env -u DOKKU_ROOT "$BIN" info "$PLUGIN" "$SERVICE" --exposed-dsn
  assert_success
  assert_output ""
}

@test "($DEFINITION) an exposed service survives a stop and a start" {
  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success
  assert_no_ambassador "stop"

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "stop and start"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --exposed-ports
  assert_success
  assert_output "$(cat "$EXPOSED_PORTS_FILE")"
}

@test "($DEFINITION) an exposed service survives a pause and a start" {
  run "$BIN" pause "$PLUGIN" "$SERVICE"
  assert_success
  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "pause and start"
}

@test "($DEFINITION) start puts back an ambassador a running service lost" {
  docker container stop "$AMBASSADOR" >/dev/null
  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "start of a running service"
}

@test "($DEFINITION) stop takes away an ambassador whose service is gone" {
  docker container rm --force "$(service_container)" >/dev/null
  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success
  assert_no_ambassador "stop of a service with no container"

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "start after the service container was removed"
}

@test "($DEFINITION) start replaces an ambassador made with a legacy link" {
  # made the way older versions of the plugin made it. On docker 29 it restarts
  # forever, since the link no longer hands it the environment it reads the
  # service's address from, and on older versions it runs. Either way it is
  # replaced rather than kept

  # the ambassador image, read from the source rather than repeated here so a bump
  # cannot leave this making an ambassador out of an image nothing runs
  local ambassador_image service_id mapping mappings
  ambassador_image="$(awk -F'"' '/AmbassadorImage = / { print $2; exit }' "$REPO_ROOT/internal/hostenv/hostenv.go")"
  service_id="$(container_inspect "$(service_container)" '{{ .Id }}')"

  local legacy_publish=()
  read -r -a mappings <"$EXPOSED_PORTS_FILE"
  for mapping in "${mappings[@]}"; do
    legacy_publish+=("--publish=${mapping#*->}:${mapping%%->*}")
  done

  docker container rm --force "$AMBASSADOR" >/dev/null
  docker container run -d --link "$(service_container):$PLUGIN" --name "$AMBASSADOR" --restart=always \
    --label dokku=ambassador --label "dokku.ambassador=$PLUGIN" --label "dokku.ambassador.container-id=$service_id" \
    "${legacy_publish[@]}" "$ambassador_image" >/dev/null

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "start over a legacy ambassador"
}

@test "($DEFINITION) the ambassador restarts the way its service does" {
  # a retry count, so that what is checked is the whole policy rather than its name
  run "$BIN" set "$PLUGIN" "$SERVICE" restart-policy on-failure:3
  assert_success
  run rebuild_service
  assert_success
  assert_ambassador "a restart policy of the service's own" "on-failure:3"

  run restart_policy_of "$(service_container)"
  assert_success
  assert_output "on-failure:3"

  # and back to what every other check expects of this service
  run "$BIN" set "$PLUGIN" "$SERVICE" restart-policy
  assert_success
  run rebuild_service
  assert_success
  assert_ambassador "an unset restart policy"
}

@test "($DEFINITION) reexpose publishes on the port-bind-address without restarting the service" {
  local service_before ambassador_before ports_before
  service_before="$(service_id)"
  ambassador_before="$(ambassador_id)"
  ports_before="$(cat "$PORT_FILE")"

  # setting it changes nothing until it is applied
  run "$BIN" set "$PLUGIN" "$SERVICE" port-bind-address 127.0.0.1
  assert_success
  [[ "$(ambassador_id)" == "$ambassador_before" ]] || fail "expected set to leave the ambassador alone"

  run "$BIN" reexpose "$PLUGIN" "$SERVICE"
  assert_success
  assert_output --partial "reexposed on port(s)"
  assert_ambassador "reexpose on the port-bind-address"
  assert_bound_on "reexpose on the port-bind-address" "127.0.0.1"
  [[ "$(ambassador_id)" != "$ambassador_before" ]] || fail "expected reexpose to replace the ambassador"
  [[ "$(service_id)" == "$service_before" ]] || fail "expected reexpose to leave the service container alone"
  [[ "$(cat "$PORT_FILE")" == "$ports_before" ]] || fail "expected reexpose to keep the ports the service was exposed on"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --exposed-ports
  assert_success
  assert_output --partial "->127.0.0.1:"

  # where the ports are bound is not where a client elsewhere connects
  run "$BIN" set "$PLUGIN" "$SERVICE" expose-host dsn.example.com
  assert_success
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --exposed-dsn
  assert_success
  assert_output "$(expected_exposed_dsn dsn.example.com)"
  refute_output --partial "127.0.0.1"

  # and back to every interface
  run "$BIN" set "$PLUGIN" "$SERVICE" port-bind-address
  assert_success
  run "$BIN" set "$PLUGIN" "$SERVICE" expose-host
  assert_success
  run "$BIN" reexpose "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "reexpose with no port-bind-address"
  assert_bound_on "reexpose with no port-bind-address" ""
  [[ "$(service_id)" == "$service_before" ]] || fail "expected reexpose to leave the service container alone"
}

@test "($DEFINITION) reexpose limits the service to the port-source-range" {
  local service_before
  service_before="$(service_id)"

  run "$BIN" set "$PLUGIN" "$SERVICE" port-source-range 192.0.2.0/24
  assert_success
  run "$BIN" reexpose "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "reexpose with a source range"
  assert_source_range "reexpose with a source range" "192.0.2.0/24"
  [[ "$(service_id)" == "$service_before" ]] || fail "expected reexpose to leave the service container alone"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --port-source-range
  assert_success
  assert_output "192.0.2.0/24"

  run "$BIN" set "$PLUGIN" "$SERVICE" port-source-range
  assert_success
  run "$BIN" reexpose "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "reexpose with no source range"
  assert_source_range "reexpose with no source range" ""
}

@test "($DEFINITION) reexpose leaves a correctly exposed service alone" {
  local service_before ambassador_before
  service_before="$(service_id)"
  ambassador_before="$(ambassador_id)"

  run "$BIN" reexpose "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "reexpose with nothing changed"
  [[ "$(ambassador_id)" == "$ambassador_before" ]] || fail "expected reexpose to keep an ambassador that already matches"
  [[ "$(service_id)" == "$service_before" ]] || fail "expected reexpose to leave the service container alone"
}

@test "($DEFINITION) reexpose replaces an ambassador that stopped publishing" {
  local ambassador_before
  ambassador_before="$(ambassador_id)"

  docker container stop "$AMBASSADOR"

  run "$BIN" reexpose "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "reexpose of a stopped ambassador"
  [[ "$(ambassador_id)" != "$ambassador_before" ]] || fail "expected reexpose to replace a stopped ambassador"
}

@test "($DEFINITION) start applies a changed expose setting to a running service" {
  local service_before
  service_before="$(service_id)"

  run "$BIN" set "$PLUGIN" "$SERVICE" port-source-range 192.0.2.0/24
  assert_success
  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "start with a changed source range"
  assert_source_range "start with a changed source range" "192.0.2.0/24"
  [[ "$(service_id)" == "$service_before" ]] || fail "expected start to leave the running service container alone"

  run "$BIN" set "$PLUGIN" "$SERVICE" port-source-range
  assert_success
  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
  assert_source_range "start with the source range unset" ""
}

@test "($DEFINITION) reexpose refuses a stopped service" {
  run "$BIN" stop "$PLUGIN" "$SERVICE"
  assert_success

  run "$BIN" reexpose "$PLUGIN" "$SERVICE"
  assert_failure
  assert_output --partial "its container is not running"
  assert_no_ambassador "reexpose of a stopped service"

  run "$BIN" start "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "start after a refused reexpose"
}

@test "($DEFINITION) unexpose takes the ambassador away" {
  run "$BIN" unexpose "$PLUGIN" "$SERVICE"
  assert_success
  assert_no_ambassador "unexpose"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --exposed-ports
  assert_success
  assert_output -- "-"

  # with a host to name, there is still no port to name it with
  run "$BIN" set "$PLUGIN" "$SERVICE" expose-host dsn.example.com
  assert_success
  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --exposed-dsn
  assert_success
  assert_output ""
  run "$BIN" set "$PLUGIN" "$SERVICE" expose-host
  assert_success
}

@test "($DEFINITION) reexpose refuses a service that is not exposed" {
  run "$BIN" reexpose "$PLUGIN" "$SERVICE"
  assert_failure
  assert_output --partial "Service $SERVICE is not exposed"
  assert_no_ambassador "reexpose of a service that is not exposed"
}

@test "($DEFINITION) expose on an address publishes on it alone" {
  # the host ports the service was first exposed on, since they are known to be free
  local local_ports=() mapping mappings
  read -r -a mappings <"$EXPOSED_PORTS_FILE"
  for mapping in "${mappings[@]}"; do
    local_ports+=("127.0.0.1:${mapping#*->}")
  done

  run "$BIN" expose "$PLUGIN" "$SERVICE" "${local_ports[@]}"
  assert_success
  assert_ambassador "expose on an address"
  assert_bound_on "expose on an address" "127.0.0.1"

  run "$BIN" unexpose "$PLUGIN" "$SERVICE"
  assert_success
  assert_no_ambassador "unexpose after an expose on an address"
}

@test "($DEFINITION) expose picks random ports on the port-bind-address" {
  run "$BIN" set "$PLUGIN" "$SERVICE" port-bind-address 127.0.0.1
  assert_success

  run "$BIN" expose "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "a random expose on the port-bind-address"
  assert_bound_on "a random expose on the port-bind-address" "127.0.0.1"

  # written without the address, so a later port-bind-address moves them
  [[ "$(cat "$PORT_FILE")" != *":"* ]] || fail "expected the port file to hold bare ports, got '$(cat "$PORT_FILE")'"

  run "$BIN" unexpose "$PLUGIN" "$SERVICE"
  assert_success
  run "$BIN" set "$PLUGIN" "$SERVICE" port-bind-address
  assert_success
}

@test "($DEFINITION) expose refuses a port it cannot publish" {
  # docker publishes on an address rather than a name, so a hostname would leave
  # the service reported as exposed with nothing published
  local hostname_ports=() mapping mappings
  read -r -a mappings <"$EXPOSED_PORTS_FILE"
  for mapping in "${mappings[@]}"; do
    hostname_ports+=("localhost:${mapping#*->}")
  done

  run "$BIN" expose "$PLUGIN" "$SERVICE" "${hostname_ports[@]}"
  assert_failure
  [[ ! -f "$PORT_FILE" ]] || fail "a refused expose left $PORT_FILE behind"
  assert_no_ambassador "refused expose"
}
