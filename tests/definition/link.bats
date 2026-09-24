#!/usr/bin/env bats
# Exercises whether link, unlink and destroy agree on an app being linked.

load ../test_helper

APP="ci-link-app"
UNLINKED_APP="ci-link-unlinked-app"

# the config variable a link sets by default
ALIAS="$(awk '/^  alias:/ { print $2; exit }' "$DEFINITION_ROOT/docker-compose.yml")_URL"

setup_file() {
  datastore_setup_file

  # the binary checks an app exists by its directory under the dokku root, and
  # reads and writes its config through the installed dokku. Neither is here,
  # so the apps are directories and dokku is a stand-in keeping the config of
  # every app in a json file of its own
  export DOKKU_ROOT="$BATS_FILE_TMPDIR/dokku-root"
  export FAKE_CONFIG_ROOT="$BATS_FILE_TMPDIR/config"
  mkdir -p "$DOKKU_ROOT/$APP" "$DOKKU_ROOT/$UNLINKED_APP" "$FAKE_CONFIG_ROOT" "$BATS_FILE_TMPDIR/bin"

  cat >"$BATS_FILE_TMPDIR/bin/dokku" <<'EOF'
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
EOF
  chmod +x "$BATS_FILE_TMPDIR/bin/dokku"

  # checking an app name asks for a plugin path, and with one set the link
  # triggers are fired through plugn. No plugin is enabled, so there is nothing
  # for the stand-in to do
  export PLUGIN_PATH="$BATS_FILE_TMPDIR/plugins"
  mkdir -p "$PLUGIN_PATH/enabled"
  printf '#!/usr/bin/env bash\n' >"$BATS_FILE_TMPDIR/bin/plugn"
  chmod +x "$BATS_FILE_TMPDIR/bin/plugn"

  export PATH="$BATS_FILE_TMPDIR/bin:$PATH"

  create_service "$SERVICE"
}

teardown_file() {
  # a failed test can leave the app linked, and a linked service is not
  # destroyed, which would leave its container behind
  "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart >/dev/null 2>/dev/null || true
  datastore_teardown_file "$SERVICE"
}

@test "($DEFINITION) unlink of an app that was never linked fails and changes nothing" {
  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$UNLINKED_APP" --no-restart
  assert_failure
  assert_output --partial "Not linked to app $UNLINKED_APP"

  run "$BIN" links "$PLUGIN" "$SERVICE"
  assert_success
  refute_output --partial "$UNLINKED_APP"
}

@test "($DEFINITION) unlink and destroy agree once the app's url is repointed" {
  run "$BIN" link "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success

  run jq -r --arg key "$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  refute_output "null"

  # the bug this closes: an app pointed at another datastore was still linked
  # according to destroy, but not according to unlink
  dokku config:set --no-restart "$APP" "$ALIAS=scheme://user:pass@elsewhere:1234/db"

  run "$BIN" destroy "$PLUGIN" "$SERVICE" --force
  assert_failure
  assert_output --partial "Cannot delete linked service"
  assert_output --partial "Linked to app(s): $APP"

  run "$BIN" linked "$PLUGIN" "$SERVICE" "$APP"
  assert_success

  run "$BIN" unlink "$PLUGIN" "$SERVICE" "$APP" --no-restart
  assert_success
  assert_output --partial "none was unset"

  # the url the app was repointed at is not the service's, so it is left alone
  run jq -r --arg key "$ALIAS" '.[$key]' "$FAKE_CONFIG_ROOT/$APP.json"
  assert_success
  assert_output "scheme://user:pass@elsewhere:1234/db"

  run "$BIN" linked "$PLUGIN" "$SERVICE" "$APP"
  assert_failure

  run "$BIN" destroy "$PLUGIN" "$SERVICE" --force
  assert_success
}
