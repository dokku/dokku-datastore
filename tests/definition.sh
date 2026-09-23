#!/usr/bin/env bash
# Exercises one definition against a real docker daemon.
#
# This is the check a unit test cannot make. Everything else about a definition
# is proven without a container: that it parses, that it renders, that the argv
# is right. Whether the container it describes actually starts, answers, and
# gives its data back is only knowable by starting it, and before this job a bad
# definition could only be caught after a release, when a plugin repo picked it up.
#
# No dokku is installed. The binary needs a data root and a user to own what it
# writes, and nothing else, which is what keeps this a per-definition check
# rather than a dokku integration test.
set -eo pipefail

DEFINITION="${1:?usage: $0 <definition>}"
DEFINITION_ROOT="internal/registry/definitions/$DEFINITION"
BIN="${BIN:-$PWD/dokku-datastore}"
SERVICE="${SERVICE:-ci}"
# a second service, made without naming a version, for the one check that needs
# one. Destroyed as soon as it has been looked at, and again by the trap.
UNPINNED_SERVICE="$SERVICE-unpinned"

export DOKKU_LIB_ROOT="${DOKKU_LIB_ROOT:-$(mktemp -d)}"
export DOKKU_LIB_HOST_ROOT="$DOKKU_LIB_ROOT"
export DOKKU_SYSTEM_USER="${DOKKU_SYSTEM_USER:-$(id -un)}"
export DOKKU_SYSTEM_GROUP="${DOKKU_SYSTEM_GROUP:-$(id -gn)}"

# the command prefix rather than the directory: postgres-17 and postgres-18 are
# two definitions of one datastore, and the commands take the datastore
PLUGIN="$(awk '/^  plugin:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")"

# the version this definition pins, so a datastore split by major version runs
# the variant this leg is for rather than whichever is newest
IMAGE_VERSION="$(awk -F: '/^FROM / { print $2; exit }' "$DEFINITION_ROOT/Dockerfile")"

# the directory its services live in, which is the plugin name unless the
# definition says otherwise. Graphite's are under the name of the image it runs
DATA_DIR="$(awk '/^  data_directory:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")"
[[ -n "$DATA_DIR" ]] || DATA_DIR="$PLUGIN"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

cleanup() {
  "$BIN" destroy "$PLUGIN" "$SERVICE" --force >/dev/null 2>/dev/null || true
  "$BIN" destroy "$PLUGIN" "$UNPINNED_SERVICE" --force >/dev/null 2>/dev/null || true
  rm -rf "$DOKKU_LIB_ROOT"
}
trap cleanup EXIT

echo "==> $DEFINITION: a create naming an image with no version is refused"
# the version this definition pins belongs to the image it pins, so there is
# nothing to fall back to for another repository. Pasting it on anyway named a
# tag nobody ever built, and the create then failed saying that image could not
# be had. Nothing reaches docker here: the refusal lands before the pull and
# before the service root is made, so this costs the daemon nothing
refused_service="${SERVICE}-noversion"
refused_root="$DOKKU_LIB_ROOT/services/$DATA_DIR/$refused_service"
create_err="$(mktemp)"
if "$BIN" create "$PLUGIN" "$refused_service" --image example.invalid/not-the-definition-image 2>"$create_err"; then
  fail "expected a create naming an image with no version to be refused"
fi
grep -q -- "--image-version" "$create_err" || fail "expected the refusal to name the flag, got '$(cat "$create_err")'"
grep -q "example.invalid/not-the-definition-image" "$create_err" || fail "expected the refusal to name the image, got '$(cat "$create_err")'"
[[ ! -d "$refused_root" ]] || fail "a refused create left $refused_root behind"
rm -f "$create_err"

echo "==> $DEFINITION: create"
"$BIN" create "$PLUGIN" "$SERVICE" --image-version "$IMAGE_VERSION"

echo "==> $DEFINITION: the service is running and reports a connection string"
status="$("$BIN" info "$PLUGIN" "$SERVICE" --status)"
[[ "$status" == "running" ]] || fail "expected a running service, got '$status'"

dsn="$("$BIN" info "$PLUGIN" "$SERVICE" --dsn)"
[[ -n "$dsn" ]] || fail "expected a connection string"

scheme="$(awk '/^  scheme:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")"
[[ "$dsn" == "$scheme://"* ]] || fail "expected a $scheme connection string, got '$dsn'"

echo "==> $DEFINITION: the service records the definition it was created with"
pinned="$(cat "$DOKKU_LIB_ROOT/services/$DATA_DIR/$SERVICE/DEFINITION")"
[[ "$pinned" == "$DEFINITION" ]] || fail "expected the service to be pinned to $DEFINITION, got '$pinned'"

echo "==> $DEFINITION: info reports the state the service was created with"
reported_definition="$("$BIN" info "$PLUGIN" "$SERVICE" --definition)"
[[ "$reported_definition" == "$DEFINITION" ]] || fail "expected info to report the definition $DEFINITION, got '$reported_definition'"

reported_version="$("$BIN" info "$PLUGIN" "$SERVICE" --image-version)"
[[ "$reported_version" == "$IMAGE_VERSION" ]] || fail "expected info to report the image version $IMAGE_VERSION, got '$reported_version'"

echo "==> $DEFINITION: a property set on the service is read back by info"
# the keyserver is the property to set here: it needs no network to exist and
# is only ever read when a backup runs, so setting it changes nothing else
"$BIN" set "$PLUGIN" "$SERVICE" backup-keyserver keys.example.com
keyserver="$("$BIN" info "$PLUGIN" "$SERVICE" --backup-keyserver)"
[[ "$keyserver" == "keys.example.com" ]] || fail "expected the keyserver to be read back, got '$keyserver'"

# the whole report has to carry it too, since that is what a machine reads.
# captured rather than piped into grep, which would close the pipe early and
# leave the report killed by SIGPIPE for pipefail to trip over
report="$("$BIN" info "$PLUGIN" "$SERVICE" --format json)"
[[ "$report" == *'"backup-keyserver":"keys.example.com"'* ]] || fail "expected the json report to carry the keyserver"

"$BIN" set "$PLUGIN" "$SERVICE" backup-keyserver
keyserver="$("$BIN" info "$PLUGIN" "$SERVICE" --backup-keyserver)"
[[ -z "$keyserver" ]] || fail "expected an unset keyserver to read back empty, got '$keyserver'"

echo "==> $DEFINITION: the container log is bounded"
# the bug this closes: a container was made with nothing to say how large its log
# was allowed to get, and on the default driver it grew until the host ran out of
# room. Only json-file and local take a max-size, so a daemon logging any other
# way would refuse one and there is nothing here to check
daemon_driver="$(docker system info --format '{{ .LoggingDriver }}')"
if [[ "$daemon_driver" == "json-file" || "$daemon_driver" == "local" ]]; then
  max_size="$(docker container inspect "dokku.$PLUGIN.$SERVICE" --format '{{ index .HostConfig.LogConfig.Config "max-size" }}')"
  # no dokku is installed, so there is no global to inherit and the built-in
  # default is what a service lands on
  [[ "$max_size" == "10m" ]] || fail "expected the container log to be capped at 10m, got '$max_size'"

  echo "==> $DEFINITION: a log setting reaches the container it is rebuilt with"
  "$BIN" set "$PLUGIN" "$SERVICE" log-opt max-size=15m,max-file=3
  reported="$("$BIN" info "$PLUGIN" "$SERVICE" --log-opt)"
  [[ "$reported" == "max-size=15m,max-file=3" ]] || fail "expected the log options to be read back, got '$reported'"

  # a stop removes the container and a start builds a new one, which is the only
  # way a create-time setting reaches a service that is already running
  "$BIN" stop "$PLUGIN" "$SERVICE"
  "$BIN" start "$PLUGIN" "$SERVICE"
  log_config="$(docker container inspect "dokku.$PLUGIN.$SERVICE" --format '{{ .HostConfig.LogConfig.Config }}')"
  [[ "$log_config" == *"max-size:15m"* ]] || fail "expected the new cap to reach the container, got '$log_config'"
  [[ "$log_config" == *"max-file:3"* ]] || fail "expected the new option to reach the container, got '$log_config'"

  echo "==> $DEFINITION: unlimited is how a service opts out"
  # what is asserted is that the plugin stops asking for a cap, not that the
  # container ends up with none: a daemon configured with log-opts of its own
  # still applies them, which is docker's business rather than this plugin's
  "$BIN" set "$PLUGIN" "$SERVICE" log-opt max-size=unlimited
  "$BIN" stop "$PLUGIN" "$SERVICE"
  "$BIN" start "$PLUGIN" "$SERVICE"
  log_config="$(docker container inspect "dokku.$PLUGIN.$SERVICE" --format '{{ .HostConfig.LogConfig.Config }}')"
  [[ "$log_config" != *"max-size:15m"* ]] || fail "expected the cap to be dropped after opting out, got '$log_config'"

  # and back to what every other check expects of this service
  "$BIN" set "$PLUGIN" "$SERVICE" log-opt
else
  echo "    skipped: the daemon logs with $daemon_driver, which takes no max-size"
fi

echo "==> $DEFINITION: a log option docker would refuse is refused first"
set_err="$(mktemp)"
if "$BIN" set "$PLUGIN" "$SERVICE" log-opt max-size=20 2>"$set_err"; then
  fail "expected a malformed log option to be refused"
fi
grep -q "max-size" "$set_err" || fail "expected the refusal to name the option, got '$(cat "$set_err")'"
[[ -z "$("$BIN" info "$PLUGIN" "$SERVICE" --log-opt)" ]] || fail "a refused set wrote the value anyway"
rm -f "$set_err"

echo "==> $DEFINITION: a restart policy reaches the container it is rebuilt with"
restart="$(docker container inspect "dokku.$PLUGIN.$SERVICE" --format '{{ .HostConfig.RestartPolicy.Name }}')"
[[ "$restart" == "always" ]] || fail "expected a service that names no policy to restart always, got '$restart'"

"$BIN" set "$PLUGIN" "$SERVICE" restart-policy unless-stopped
reported="$("$BIN" info "$PLUGIN" "$SERVICE" --restart-policy)"
[[ "$reported" == "unless-stopped" ]] || fail "expected the restart policy to be read back, got '$reported'"

# nothing reaches the running container: like a log setting, it is read when a
# container is made
restart="$(docker container inspect "dokku.$PLUGIN.$SERVICE" --format '{{ .HostConfig.RestartPolicy.Name }}')"
[[ "$restart" == "always" ]] || fail "expected set to leave the running container alone, got '$restart'"

"$BIN" stop "$PLUGIN" "$SERVICE"
"$BIN" start "$PLUGIN" "$SERVICE"
restart="$(docker container inspect "dokku.$PLUGIN.$SERVICE" --format '{{ .HostConfig.RestartPolicy.Name }}')"
[[ "$restart" == "unless-stopped" ]] || fail "expected the new policy to reach the container, got '$restart'"

# and back to what every other check expects of this service
"$BIN" set "$PLUGIN" "$SERVICE" restart-policy
"$BIN" stop "$PLUGIN" "$SERVICE"
"$BIN" start "$PLUGIN" "$SERVICE"
restart="$(docker container inspect "dokku.$PLUGIN.$SERVICE" --format '{{ .HostConfig.RestartPolicy.Name }}')"
[[ "$restart" == "always" ]] || fail "expected an unset policy to go back to always, got '$restart'"

echo "==> $DEFINITION: a restart policy docker would refuse is refused first"
set_err="$(mktemp)"
if "$BIN" set "$PLUGIN" "$SERVICE" restart-policy on-failure:abc 2>"$set_err"; then
  fail "expected a malformed restart policy to be refused"
fi
grep -q "restart-policy" "$set_err" || fail "expected the refusal to name the property, got '$(cat "$set_err")'"
[[ -z "$("$BIN" info "$PLUGIN" "$SERVICE" --restart-policy)" ]] || fail "a refused set wrote the value anyway"
rm -f "$set_err"

echo "==> $DEFINITION: info reports every service when none is named"
every_service="$("$BIN" info "$PLUGIN")"
[[ "$every_service" == *"$SERVICE"* ]] || fail "expected $SERVICE to be reported when no service is named"

echo "==> $DEFINITION: the rendered compose file is valid"
compose="$DOKKU_LIB_ROOT/services/$DATA_DIR/$SERVICE/docker-compose.yml"
[[ -f "$compose" ]] || fail "no compose file was written"
docker compose --file "$compose" config --quiet || fail "the rendered compose file is not valid"

# the mode of a file or folder, as octal permission bits
assert_mode() {
  local expected="$1" path="$2" mode
  mode="$(stat -c '%a' "$path")"
  [[ "$mode" == "$expected" ]] || fail "expected $path to be $expected, got $mode"
}

echo "==> $DEFINITION: the files holding secrets are unreadable by other users"
# the compose file carries the service's password in the clear, and the custom
# environment and config options can carry credentials of their own
SERVICE_ROOT="$DOKKU_LIB_ROOT/services/$DATA_DIR/$SERVICE"
assert_mode 640 "$compose"
assert_mode 640 "$SERVICE_ROOT/ENV"
assert_mode 640 "$SERVICE_ROOT/CONFIG_OPTIONS"

echo "==> $DEFINITION: backup credentials are unreadable by other users"
status=0
"$BIN" backup-auth "$PLUGIN" "$SERVICE" AKIAEXAMPLE wJalrXUtnFEMI us-east-1 s3v4 http://127.0.0.1:9000 || status=$?
if [[ "$status" -eq "${DOKKU_NOT_IMPLEMENTED_EXIT:-10}" ]]; then
  echo "    skipped: $PLUGIN does not implement backup-auth"
elif [[ "$status" -ne 0 ]]; then
  fail "backup-auth failed with status $status"
else
  assert_mode 750 "$SERVICE_ROOT/backup"
  for name in AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION AWS_SIGNATURE_VERSION ENDPOINT_URL; do
    assert_mode 640 "$SERVICE_ROOT/backup/$name"
  done

  "$BIN" backup-deauth "$PLUGIN" "$SERVICE"
  [[ ! -d "$SERVICE_ROOT/backup" ]] || fail "backup-deauth left the credentials behind"
fi

echo "==> $DEFINITION: export and import round trip"
probe="tests/probes/$DEFINITION.sh"
if [[ -x "$probe" ]]; then
  "$probe" write "$SERVICE"
fi

dump="$(mktemp)"
status=0
round_trip=1
"$BIN" export "$PLUGIN" "$SERVICE" >"$dump" 2>"$dump.err" || status=$?
if [[ "$status" -ne 0 ]]; then
  # a datastore that declares no export exits the way dokku expects of a plugin
  # that does not handle a command, and there is no round trip to make. Every
  # other check still applies: a cache is still created and still destroyed.
  if [[ "$status" -eq "${DOKKU_NOT_IMPLEMENTED_EXIT:-10}" ]]; then
    echo "    skipped: $PLUGIN does not implement export"
    round_trip=0
  else
    cat "$dump.err" >&2
    fail "export failed with status $status"
  fi
fi

if [[ "$round_trip" -eq 1 ]]; then
  [[ -s "$dump" ]] || fail "export produced nothing"

  # overwritten first, so that finding the record afterwards means the import
  # put it back rather than that it was never gone
  if [[ -x "$probe" ]]; then
    "$probe" clobber "$SERVICE"
  fi

  if ! "$BIN" import "$PLUGIN" "$SERVICE" <"$dump"; then
    fail "import failed"
  fi

  if [[ -x "$probe" ]]; then
    restored="$("$probe" read "$SERVICE")"
    [[ "$restored" == "known" ]] || fail "the round trip lost the record, read '$restored'"
  fi

  running="$("$BIN" info "$PLUGIN" "$SERVICE" --status)"
  [[ "$running" == "running" ]] || fail "expected the service to be running after an import, got '$running'"
fi

rm -f "$dump" "$dump.err"

# An exposed service publishes its ports through a second container, the
# ambassador, which forwards to the service over a network they share. It used
# to be linked to the service container instead. Docker refuses to start a
# container linked to one that is not running, which is how an exposed service
# used to fail to come back after a stop and a start, and docker 29 no longer
# hands a linked container the environment the ambassador found the service by.
AMBASSADOR="dokku.$PLUGIN.$SERVICE.ambassador"
PORT_FILE="$DOKKU_LIB_ROOT/services/$DATA_DIR/$SERVICE/PORT"

# the ambassador image, read from the source rather than repeated here so a bump
# cannot leave this making an ambassador out of an image nothing runs
AMBASSADOR_IMAGE="$(awk -F'"' '/AmbassadorImage = / { print $2; exit }' internal/hostenv/hostenv.go)"

# the ambassador is up, was made by docker-port-forward rather than with a
# legacy link, restarts the way its service does, fronts the container the
# service has now, and publishes the port the service was exposed on
assert_ambassador() {
  local step="$1" expected_restart="${2:-always}" state managed links restart fronted service_id published host_port
  state="$(docker container inspect "$AMBASSADOR" --format '{{ .State.Status }}' 2>/dev/null || true)"
  [[ "$state" == "running" ]] || fail "$step: expected the ambassador to be running, got '$state'"

  managed="$(docker container inspect "$AMBASSADOR" --format '{{ index .Config.Labels "com.dokku.port-forward" }}')"
  [[ "$managed" == "true" ]] || fail "$step: expected the ambassador to be made by docker-port-forward, got '$managed'"

  links="$(docker container inspect "$AMBASSADOR" --format '{{ len .HostConfig.Links }}')"
  [[ "$links" == "0" ]] || fail "$step: expected the ambassador to have no links, got $links"

  restart="$(docker container inspect "$AMBASSADOR" --format '{{ .HostConfig.RestartPolicy.Name }}:{{ .HostConfig.RestartPolicy.MaximumRetryCount }}')"
  [[ "${restart%:0}" == "$expected_restart" ]] || fail "$step: expected the ambassador to restart $expected_restart, got '$restart'"

  fronted="$(docker container inspect "$AMBASSADOR" --format '{{ index .Config.Labels "dokku.ambassador.container-id" }}')"
  service_id="$(docker container inspect "dokku.$PLUGIN.$SERVICE" --format '{{ .Id }}')"
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
  bound="$(docker container inspect "$AMBASSADOR" --format '{{ range $port, $bindings := .HostConfig.PortBindings }}{{ range $bindings }}[{{ .HostIp }}]{{ end }}{{ end }}')"
  [[ -n "$bound" && -z "${bound//"[$expected]"/}" ]] || fail "$step: expected every port to be bound on '$expected', got '$bound'"
}

assert_no_ambassador() {
  if docker container inspect "$AMBASSADOR" >/dev/null 2>/dev/null; then
    fail "$1: expected no ambassador, found one"
  fi
}

echo "==> $DEFINITION: expose publishes the service"
"$BIN" expose "$PLUGIN" "$SERVICE"
assert_ambassador "expose"
assert_bound_on "expose" ""
exposed_ports="$("$BIN" info "$PLUGIN" "$SERVICE" --exposed-ports)"

echo "==> $DEFINITION: an exposed service survives a stop and a start"
"$BIN" stop "$PLUGIN" "$SERVICE"
assert_no_ambassador "stop"
"$BIN" start "$PLUGIN" "$SERVICE"
assert_ambassador "stop and start"
[[ "$("$BIN" info "$PLUGIN" "$SERVICE" --exposed-ports)" == "$exposed_ports" ]] || fail "a stop and a start changed the exposed ports"

echo "==> $DEFINITION: an exposed service survives a pause and a start"
"$BIN" pause "$PLUGIN" "$SERVICE"
"$BIN" start "$PLUGIN" "$SERVICE"
assert_ambassador "pause and start"

echo "==> $DEFINITION: start puts back an ambassador a running service lost"
docker container stop "$AMBASSADOR" >/dev/null
"$BIN" start "$PLUGIN" "$SERVICE"
assert_ambassador "start of a running service"

echo "==> $DEFINITION: stop takes away an ambassador whose service is gone"
docker container rm --force "dokku.$PLUGIN.$SERVICE" >/dev/null
"$BIN" stop "$PLUGIN" "$SERVICE"
assert_no_ambassador "stop of a service with no container"
"$BIN" start "$PLUGIN" "$SERVICE"
assert_ambassador "start after the service container was removed"

echo "==> $DEFINITION: start replaces an ambassador made with a legacy link"
# made the way older versions of the plugin made it. On docker 29 it restarts
# forever, since the link no longer hands it the environment it reads the
# service's address from, and on older versions it runs. Either way it is
# replaced rather than kept
service_id="$(docker container inspect "dokku.$PLUGIN.$SERVICE" --format '{{ .Id }}')"
legacy_publish=()
for mapping in $exposed_ports; do
  legacy_publish+=("--publish=${mapping#*->}:${mapping%%->*}")
done
docker container rm --force "$AMBASSADOR" >/dev/null
docker container run -d --link "dokku.$PLUGIN.$SERVICE:$PLUGIN" --name "$AMBASSADOR" --restart=always \
  --label dokku=ambassador --label "dokku.ambassador=$PLUGIN" --label "dokku.ambassador.container-id=$service_id" \
  "${legacy_publish[@]}" "$AMBASSADOR_IMAGE" >/dev/null
"$BIN" start "$PLUGIN" "$SERVICE"
assert_ambassador "start over a legacy ambassador"

echo "==> $DEFINITION: the ambassador restarts the way its service does"
# a retry count, so that what is checked is the whole policy rather than its name
"$BIN" set "$PLUGIN" "$SERVICE" restart-policy on-failure:3
"$BIN" stop "$PLUGIN" "$SERVICE"
"$BIN" start "$PLUGIN" "$SERVICE"
assert_ambassador "a restart policy of the service's own" "on-failure:3"
restart="$(docker container inspect "dokku.$PLUGIN.$SERVICE" --format '{{ .HostConfig.RestartPolicy.Name }}:{{ .HostConfig.RestartPolicy.MaximumRetryCount }}')"
[[ "$restart" == "on-failure:3" ]] || fail "expected the service container to restart on-failure:3, got '$restart'"

"$BIN" set "$PLUGIN" "$SERVICE" restart-policy
"$BIN" stop "$PLUGIN" "$SERVICE"
"$BIN" start "$PLUGIN" "$SERVICE"
assert_ambassador "an unset restart policy"

echo "==> $DEFINITION: unexpose takes the ambassador away"
"$BIN" unexpose "$PLUGIN" "$SERVICE"
assert_no_ambassador "unexpose"
unexposed_ports="$("$BIN" info "$PLUGIN" "$SERVICE" --exposed-ports)"
[[ "$unexposed_ports" == "-" ]] || fail "expected no exposed ports after an unexpose, got '$unexposed_ports'"

echo "==> $DEFINITION: expose on an address publishes on it alone"
# the host ports the service was just exposed on, since they are known to be free
local_ports=()
for mapping in $exposed_ports; do
  local_ports+=("127.0.0.1:${mapping#*->}")
done
"$BIN" expose "$PLUGIN" "$SERVICE" "${local_ports[@]}"
assert_ambassador "expose on an address"
assert_bound_on "expose on an address" "127.0.0.1"
"$BIN" unexpose "$PLUGIN" "$SERVICE"
assert_no_ambassador "unexpose after an expose on an address"

echo "==> $DEFINITION: expose refuses a port it cannot publish"
# docker publishes on an address rather than a name, so a hostname would leave
# the service reported as exposed with nothing published
hostname_ports=()
for mapping in $exposed_ports; do
  hostname_ports+=("localhost:${mapping#*->}")
done
if "$BIN" expose "$PLUGIN" "$SERVICE" "${hostname_ports[@]}" 2>/dev/null; then
  fail "expected an expose on a hostname to be refused"
fi
[[ ! -f "$PORT_FILE" ]] || fail "a refused expose left $PORT_FILE behind"
assert_no_ambassador "refused expose"

# The version a service runs is what it recorded, and only an upgrade changes
# that. These run last because two of them deliberately leave the service down,
# and everything before this point needs it up.
CONTAINER="dokku.$PLUGIN.$SERVICE"
recorded_image="$("$BIN" info "$PLUGIN" "$SERVICE" --image)"
recorded_version="$("$BIN" info "$PLUGIN" "$SERVICE" --image-version)"
recorded="$recorded_image:$recorded_version"
start_err="$(mktemp)"
PULL_VARIABLE="$(echo "$PLUGIN" | tr '[:lower:]' '[:upper:]')_DISABLE_PULL"

# the readiness probe, read from the source rather than repeated here so a bump
# cannot leave this asserting on a version nothing runs
WAIT_IMAGE="$(awk -F'"' '/WaitImage = / { print $2; exit }' internal/hostenv/hostenv.go)"

echo "==> $DEFINITION: start brings the service back on the version it recorded"
"$BIN" stop "$PLUGIN" "$SERVICE"
"$BIN" start "$PLUGIN" "$SERVICE"
running="$("$BIN" info "$PLUGIN" "$SERVICE" --version)"
[[ "$running" == "$recorded" ]] || fail "expected start to come back on $recorded, got '$running'"

echo "==> $DEFINITION: start records the image of a service that never did"
"$BIN" pause "$PLUGIN" "$SERVICE"
rm -f "$SERVICE_ROOT/IMAGE" "$SERVICE_ROOT/IMAGE_VERSION"
"$BIN" start "$PLUGIN" "$SERVICE"
[[ "$(cat "$SERVICE_ROOT/IMAGE")" == "$recorded_image" ]] || fail "start did not write down the image the container runs"
[[ "$(cat "$SERVICE_ROOT/IMAGE_VERSION")" == "$recorded_version" ]] || fail "start did not write down the version the container runs"

echo "==> $DEFINITION: a pause and a start is still a restart"
before="$(docker container inspect "$CONTAINER" --format '{{ .Id }}')"
"$BIN" pause "$PLUGIN" "$SERVICE"
"$BIN" start "$PLUGIN" "$SERVICE"
after="$(docker container inspect "$CONTAINER" --format '{{ .Id }}')"
[[ "$before" == "$after" ]] || fail "start recreated the container instead of starting the one it had"

echo "==> $DEFINITION: the record wins over a container that disagrees with it"
"$BIN" pause "$PLUGIN" "$SERVICE"
echo "0.0.0-nonexistent" >"$SERVICE_ROOT/IMAGE_VERSION"
if "$BIN" start "$PLUGIN" "$SERVICE" 2>"$start_err"; then
  fail "expected start to go looking for the version the record names"
fi
grep -q "0.0.0-nonexistent" "$start_err" || fail "expected start to name the recorded version, got '$(cat "$start_err")'"
# the image is fetched before the stale container is taken away, so a start that
# cannot get that far leaves the service with the container it already had
docker container inspect "$CONTAINER" >/dev/null 2>/dev/null || fail "a failed start removed the container it could not replace"
echo "$recorded_version" >"$SERVICE_ROOT/IMAGE_VERSION"
"$BIN" start "$PLUGIN" "$SERVICE"

echo "==> $DEFINITION: start refuses a service it cannot place"
"$BIN" stop "$PLUGIN" "$SERVICE"
rm -f "$SERVICE_ROOT/IMAGE" "$SERVICE_ROOT/IMAGE_VERSION"
if "$BIN" start "$PLUGIN" "$SERVICE" 2>"$start_err"; then
  fail "expected start to refuse a service with no record and no container"
fi
grep -q "upgrade" "$start_err" || fail "expected start to say what to run instead, got '$(cat "$start_err")'"
echo "$recorded_image" >"$SERVICE_ROOT/IMAGE"
echo "$recorded_version" >"$SERVICE_ROOT/IMAGE_VERSION"

# the container is gone by now, so the image can go too. Skipped rather than
# forced if anything else on the host still holds it.
if docker image rm --force "$recorded" >/dev/null 2>/dev/null && ! docker image inspect "$recorded" >/dev/null 2>/dev/null; then
  echo "==> $DEFINITION: start explains an image it is not allowed to fetch"
  if env "$PULL_VARIABLE=true" "$BIN" start "$PLUGIN" "$SERVICE" 2>"$start_err"; then
    fail "expected start to refuse with pulling turned off"
  fi
  grep -q "docker image pull $recorded" "$start_err" || fail "expected start to say what to pull, got '$(cat "$start_err")'"

  echo "==> $DEFINITION: start fetches the version the service recorded"
  "$BIN" start "$PLUGIN" "$SERVICE"
  running="$("$BIN" info "$PLUGIN" "$SERVICE" --version)"
  [[ "$running" == "$recorded" ]] || fail "expected start to pull back $recorded, got '$running'"
else
  echo "    skipped: $recorded is still in use and cannot be removed"
  "$BIN" start "$PLUGIN" "$SERVICE"
fi

# The images the plugin runs beside a service are pulled once, when the plugin is
# installed, and then only ever run - so a host pruned since has none of them and
# nothing used to fetch them back. The readiness probe is the one to prove it on,
# because it runs on every start and the start path is where a missing image is
# felt.
#
# Removed without --force on purpose. A non-forced removal fails while a
# container still holds the image, so a second run probing on this daemon makes
# this skip rather than pull the image out from under it. A probe that has not
# started yet is not covered, and does not need to be: the refusal below is
# scoped to one invocation with env rather than exported, so the worst another
# run sees is one extra pull.
if docker image rm "$WAIT_IMAGE" >/dev/null 2>/dev/null && ! docker image inspect "$WAIT_IMAGE" >/dev/null 2>/dev/null; then
  echo "==> $DEFINITION: start explains a sidecar image it is not allowed to fetch"
  if env "$PULL_VARIABLE=true" "$BIN" start "$PLUGIN" "$SERVICE" 2>"$start_err"; then
    fail "expected start to refuse with pulling turned off and $WAIT_IMAGE gone"
  fi
  grep -q "docker image pull $WAIT_IMAGE" "$start_err" || fail "expected start to say what to pull, got '$(cat "$start_err")'"

  echo "==> $DEFINITION: start fetches a sidecar image the host no longer has"
  "$BIN" start "$PLUGIN" "$SERVICE"
  docker image inspect "$WAIT_IMAGE" >/dev/null 2>/dev/null || fail "start did not fetch $WAIT_IMAGE back"
else
  echo "    skipped: $WAIT_IMAGE is still in use and cannot be removed"
fi

echo "==> $DEFINITION: start thaws a frozen container rather than replacing it"
# docker refuses to start a container it froze, and nothing in the plugin ever
# freezes one - its own pause is a stop - so this is the state a hand-run
# docker pause leaves behind, and start used to see straight past it and try to
# build a second container of the same name
frozen="$(docker container inspect "$CONTAINER" --format '{{ .Id }}')"
docker container pause "$CONTAINER" >/dev/null
"$BIN" start "$PLUGIN" "$SERVICE"
thawed="$(docker container inspect "$CONTAINER" --format '{{ .Id }}')"
[[ "$frozen" == "$thawed" ]] || fail "start replaced the frozen container instead of thawing it"
frozen_status="$("$BIN" info "$PLUGIN" "$SERVICE" --status)"
[[ "$frozen_status" == "running" ]] || fail "expected a running service after start, got '$frozen_status'"

rm -f "$start_err"

echo "==> $DEFINITION: a bare upgrade stays on the definition the service was created with"
# the service was created on the version this definition pins, so a bare upgrade
# lands on the one it is already running. What is being checked is that it did
# not reach for a newer definition to get there
"$BIN" upgrade "$PLUGIN" "$SERVICE"
upgraded_version="$("$BIN" info "$PLUGIN" "$SERVICE" --image-version)"
[[ "$upgraded_version" == "$recorded_version" ]] || fail "a bare upgrade moved the version from $recorded_version to '$upgraded_version'"
upgraded_definition="$("$BIN" info "$PLUGIN" "$SERVICE" --definition)"
[[ "$upgraded_definition" == "$DEFINITION" ]] || fail "a bare upgrade moved the service from $DEFINITION to '$upgraded_definition'"

echo "==> $DEFINITION: a create with no version still records one"
# only that both halves are there, not which version they name: with no version
# given, a datastore split by major version lands on its newest definition
# rather than on the one this run is for
"$BIN" create "$PLUGIN" "$UNPINNED_SERVICE"
unpinned_image="$("$BIN" info "$PLUGIN" "$UNPINNED_SERVICE" --image)"
unpinned_version="$("$BIN" info "$PLUGIN" "$UNPINNED_SERVICE" --image-version)"
[[ -n "$unpinned_image" ]] || fail "a create with no version left IMAGE empty"
[[ -n "$unpinned_version" ]] || fail "a create with no version left IMAGE_VERSION empty"
"$BIN" destroy "$PLUGIN" "$UNPINNED_SERVICE" --force

echo "==> $DEFINITION: destroy leaves nothing behind"
"$BIN" destroy "$PLUGIN" "$SERVICE" --force

containers="$(docker container ls -a --filter "name=^/dokku\.$PLUGIN\.$SERVICE$" --format '{{.Names}}')"
[[ -z "$containers" ]] || fail "destroy left a container: $containers"

[[ ! -d "$DOKKU_LIB_ROOT/services/$DATA_DIR/$SERVICE" ]] || fail "destroy left the service root behind"

echo "==> $DEFINITION: ok"
