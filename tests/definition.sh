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

echo "==> $DEFINITION: info reports every service when none is named"
every_service="$("$BIN" info "$PLUGIN")"
[[ "$every_service" == *"$SERVICE"* ]] || fail "expected $SERVICE to be reported when no service is named"

echo "==> $DEFINITION: the rendered compose file is valid"
compose="$DOKKU_LIB_ROOT/services/$DATA_DIR/$SERVICE/docker-compose.yml"
[[ -f "$compose" ]] || fail "no compose file was written"
docker compose --file "$compose" config --quiet || fail "the rendered compose file is not valid"

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

# The version a service runs is what it recorded, and only an upgrade changes
# that. These run last because two of them deliberately leave the service down,
# and everything before this point needs it up.
SERVICE_ROOT="$DOKKU_LIB_ROOT/services/$DATA_DIR/$SERVICE"
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
