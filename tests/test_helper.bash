#!/usr/bin/env bash
# Shared by every integration test: the definition under test, where the binary
# keeps its services, and the checks more than one file makes.
#
# No dokku is installed. The binary needs a data root and a user to own what it
# writes, and nothing else, which is what keeps these per-definition checks
# rather than a dokku integration test.

bats_require_minimum_version 1.5.0
bats_load_library bats-support
bats_load_library bats-assert

# loaded from tests/ and tests/definition/ alike, so every path is built from
# here rather than from wherever bats was run
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -z "$DEFINITION" ]]; then
  echo "DEFINITION must name a directory in internal/registry/definitions" >&2
  exit 1
fi

DEFINITION_ROOT="$REPO_ROOT/internal/registry/definitions/$DEFINITION"
BIN="${BIN:-$REPO_ROOT/dokku-datastore}"
# the exit dokku expects of a plugin that does not handle a command
# shellcheck disable=SC2034
NOT_IMPLEMENTED_EXIT="${DOKKU_NOT_IMPLEMENTED_EXIT:-10}"

# one service per file, so two files never share a container or a service root.
# The hyphen is on purpose: a datastore's database name has it replaced, so the
# probes only find their record if they read the name the service recorded
SERVICE="${SERVICE:-ci-$(basename "$BATS_TEST_FILENAME" .bats)}"

# the command prefix rather than the directory: postgres-17 and postgres-18 are
# two definitions of one datastore, and the commands take the datastore
PLUGIN="$(awk '/^  plugin:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")"

# the version this definition pins, so a datastore split by major version runs
# the variant this run is for rather than whichever is newest
IMAGE_VERSION="$(awk -F: '/^FROM / { print $2; exit }' "$DEFINITION_ROOT/Dockerfile")"

# the directory its services live in, which is the plugin name unless the
# definition says otherwise. Graphite's are under the name of the image it runs
DATA_DIR="$(awk '/^  data_directory:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")"
[[ -n "$DATA_DIR" ]] || DATA_DIR="$PLUGIN"

# a data root of its own unless one is handed in, and the user and group that
# own what the binary writes there. Called from setup_file so that every test in
# a file shares one root
datastore_setup_file() {
  if [[ -z "$DOKKU_LIB_ROOT" ]]; then
    DOKKU_LIB_ROOT="$(mktemp -d)"
    export DATASTORE_OWNS_LIB_ROOT=1
  fi
  export DOKKU_LIB_ROOT
  export DOKKU_LIB_HOST_ROOT="${DOKKU_LIB_HOST_ROOT:-$DOKKU_LIB_ROOT}"
  export DOKKU_SYSTEM_USER="${DOKKU_SYSTEM_USER:-$(id -un)}"
  export DOKKU_SYSTEM_GROUP="${DOKKU_SYSTEM_GROUP:-$(id -gn)}"
}

# destroys the named services, and the data root only when it was made here: a
# root that was handed in belongs to whoever handed it in
datastore_teardown_file() {
  local service
  for service in "$@"; do
    "$BIN" destroy "$PLUGIN" "$service" --force >/dev/null 2>/dev/null || true
  done

  if [[ "$DATASTORE_OWNS_LIB_ROOT" == "1" ]]; then
    rm -rf "$DOKKU_LIB_ROOT"
  fi
}

# stands in for the dokku an app is linked through, with the named apps. The
# binary checks an app exists by its directory under the dokku root, and reads
# and writes its config through the installed dokku. Neither is here, so the
# apps are directories and dokku is a stand-in keeping the config of every app
# in a json file of its own. Called from setup_file
fake_dokku_setup() {
  export DOKKU_ROOT="$BATS_FILE_TMPDIR/dokku-root"
  export FAKE_CONFIG_ROOT="$BATS_FILE_TMPDIR/config"
  mkdir -p "$FAKE_CONFIG_ROOT" "$BATS_FILE_TMPDIR/bin"

  local app
  for app in "$@"; do
    mkdir -p "$DOKKU_ROOT/$app"
  done

  cat >"$BATS_FILE_TMPDIR/bin/dokku" <<'EOS'
#!/usr/bin/env bash
set -eo pipefail

subcommand="$1"
shift
[[ "$1" == "--format" ]] && shift 2
[[ "$1" == "--no-restart" ]] && shift
config="$FAKE_CONFIG_ROOT/$1.json"
[[ -f "$config" ]] || echo '{}' >"$config"
shift || true

case "$subcommand" in
  config:export)
    cat "$config"
    ;;
  config:set)
    for entry in "$@"; do
      jq --arg key "${entry%%=*}" --arg value "${entry#*=}" '.[$key] = $value' "$config" >"$config.tmp"
      mv "$config.tmp" "$config"
    done
    ;;
  config:unset)
    for key in "$@"; do
      jq --arg key "$key" 'del(.[$key])' "$config" >"$config.tmp"
      mv "$config.tmp" "$config"
    done
    ;;
esac
EOS
  chmod +x "$BATS_FILE_TMPDIR/bin/dokku"

  # checking an app name asks for a plugin path, and with one set the link
  # triggers are fired through plugn. No plugin is enabled, so there is nothing
  # for the stand-in to do
  export PLUGIN_PATH="$BATS_FILE_TMPDIR/plugins"
  mkdir -p "$PLUGIN_PATH/enabled"
  printf '#!/usr/bin/env bash\n' >"$BATS_FILE_TMPDIR/bin/plugn"
  chmod +x "$BATS_FILE_TMPDIR/bin/plugn"

  export PATH="$BATS_FILE_TMPDIR/bin:$PATH"
}

# creates a service on the version this definition pins
create_service() {
  "$BIN" create "$PLUGIN" "$1" --image-version "$IMAGE_VERSION"
}

service_root() {
  echo "$DOKKU_LIB_ROOT/services/$DATA_DIR/${1:-$SERVICE}"
}

service_container() {
  echo "dokku.$PLUGIN.${1:-$SERVICE}"
}

container_inspect() {
  docker container inspect "$1" --format "$2"
}

# the whole restart policy, retry count included, as docker's own syntax
restart_policy_of() {
  container_inspect "$1" '{{ .HostConfig.RestartPolicy.Name }}:{{ .HostConfig.RestartPolicy.MaximumRetryCount }}'
}

# the source of whatever a container has mounted at a path, and whether it is
# writable, as <source>:<rw>. Empty when nothing is mounted there
mount_of() {
  container_inspect "$1" "{{ range .Mounts }}{{ if eq .Destination \"$2\" }}{{ .Source }}:{{ .RW }}{{ end }}{{ end }}"
}

# a host directory to mount, under the data root so that it goes with it
mount_source() {
  local directory="$DOKKU_LIB_ROOT/mounts/$1"
  mkdir -p "$directory"
  echo "$directory"
}

# a stop removes the container and a start builds a new one, which is the only
# way a create-time setting reaches a service that is already running
rebuild_service() {
  "$BIN" stop "$PLUGIN" "${1:-$SERVICE}"
  "$BIN" start "$PLUGIN" "${1:-$SERVICE}"
}

# the mode of a file or folder, as octal permission bits
assert_mode() {
  local expected="$1" path="$2" mode
  mode="$(stat -c '%a' "$path" 2>/dev/null || stat -f '%Lp' "$path")"
  [[ "$mode" == "$expected" ]] || fail "expected $path to be $expected, got $mode"
}
