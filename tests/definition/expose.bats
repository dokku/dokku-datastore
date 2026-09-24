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

@test "($DEFINITION) expose publishes the service" {
  run "$BIN" expose "$PLUGIN" "$SERVICE"
  assert_success
  assert_ambassador "expose"
  assert_bound_on "expose" ""

  "$BIN" info "$PLUGIN" "$SERVICE" --exposed-ports >"$EXPOSED_PORTS_FILE"
  [[ -s "$EXPOSED_PORTS_FILE" ]] || fail "expected the exposed ports to be reported"
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

@test "($DEFINITION) unexpose takes the ambassador away" {
  run "$BIN" unexpose "$PLUGIN" "$SERVICE"
  assert_success
  assert_no_ambassador "unexpose"

  run --separate-stderr "$BIN" info "$PLUGIN" "$SERVICE" --exposed-ports
  assert_success
  assert_output -- "-"
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
