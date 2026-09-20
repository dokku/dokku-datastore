#!/usr/bin/env bash
# Creates one definition's service through both execution backends and compares
# the containers.
#
# The claim the compose backend rests on is that it produces the same container
# the docker cli does, because both are handed one set of resolved values. This
# is what turns that claim into a check: a difference is a bug in the renderer
# rather than in either backend.
set -eo pipefail

DEFINITION="${1:?usage: $0 <definition>}"
DEFINITION_ROOT="internal/registry/definitions/$DEFINITION"
BIN="${BIN:-$PWD/dokku-datastore}"

export DOKKU_LIB_ROOT="${DOKKU_LIB_ROOT:-$(mktemp -d)}"
export DOKKU_LIB_HOST_ROOT="$DOKKU_LIB_ROOT"
export DOKKU_SYSTEM_USER="${DOKKU_SYSTEM_USER:-$(id -un)}"
export DOKKU_SYSTEM_GROUP="${DOKKU_SYSTEM_GROUP:-$(id -gn)}"

PLUGIN="$(awk '/^  plugin:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")"
IMAGE_VERSION="$(awk -F: '/^FROM / { print $2; exit }' "$DEFINITION_ROOT/Dockerfile")"

# the directory its services live in, which is the plugin name unless the
# definition says otherwise. Graphite's are under the name of the image it runs
DATA_DIR="$(awk '/^  data_directory:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")"
[[ -n "$DATA_DIR" ]] || DATA_DIR="$PLUGIN"

cleanup() {
  for service in viadocker viacompose; do
    "$BIN" destroy "$PLUGIN" "$service" --force >/dev/null 2>&1 || true
  done
  rm -rf "$DOKKU_LIB_ROOT"
}
trap cleanup EXIT

echo "==> $DEFINITION: create through each backend"
DOKKU_DATASTORE_BACKEND=docker "$BIN" create "$PLUGIN" viadocker --image-version "$IMAGE_VERSION" >/dev/null
DOKKU_DATASTORE_BACKEND=compose "$BIN" create "$PLUGIN" viacompose --image-version "$IMAGE_VERSION" >/dev/null

# the record is what stops a later change of the host default addressing a
# service the other way, so it has to say what actually made it
for service in viadocker viacompose; do
  expected="${service#via}"
  recorded="$(cat "$DOKKU_LIB_ROOT/services/$DATA_DIR/$service/BACKEND")"
  [[ "$recorded" == "$expected" ]] || {
    echo "FAIL: $service recorded backend '$recorded', expected '$expected'" >&2
    exit 1
  }
done

echo "==> $DEFINITION: the two containers agree"

# a generated secret differs between the two by design, and a datastore that
# authenticates on its command line carries it into the container, so the values
# are handed over to be replaced rather than compared
secrets=()
for service in viadocker viacompose; do
  for file in "$DOKKU_LIB_ROOT/services/$DATA_DIR/$service"/*PASSWORD; do
    [[ -f "$file" ]] && secrets+=("$(cat "$file")")
  done
done

./tests/inspect-diff.sh "dokku.$PLUGIN.viadocker" "dokku.$PLUGIN.viacompose" "${secrets[@]}"
