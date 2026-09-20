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
  "$BIN" destroy "$PLUGIN" "$SERVICE" --force >/dev/null 2>&1 || true
  rm -rf "$DOKKU_LIB_ROOT"
}
trap cleanup EXIT

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

echo "==> $DEFINITION: destroy leaves nothing behind"
"$BIN" destroy "$PLUGIN" "$SERVICE" --force

containers="$(docker container ls -a --filter "name=^/dokku\.$PLUGIN\.$SERVICE$" --format '{{.Names}}')"
[[ -z "$containers" ]] || fail "destroy left a container: $containers"

[[ ! -d "$DOKKU_LIB_ROOT/services/$DATA_DIR/$SERVICE" ]] || fail "destroy left the service root behind"

echo "==> $DEFINITION: ok"
