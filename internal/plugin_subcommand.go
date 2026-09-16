package internal

import (
	"fmt"

	"github.com/dokku/dokku-datastore/internal/definition"
)

// subcommandTemplate is the script a plugin ships for one of its datastore's
// own commands. It is the same shape as the scripts the plugin ships for the
// tool's commands, differing only in naming the command it dispatches.
const subcommandTemplate = `#!/usr/bin/env bash
source "$(dirname "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)")/config"
set -eo pipefail
[[ $DOKKU_TRACE ]] && set -x
source "$PLUGIN_CORE_AVAILABLE_PATH/common/functions"

# generated from the datastore definition. do not edit: it is rewritten by
# make generate.
service-%[1]s-cmd() {
  declare desc=%[2]q
  local cmd="$PLUGIN_COMMAND_PREFIX:%[3]s" argv=("$@")
  [[ ${argv[0]} == "$cmd" ]] && shift 1

  "${DOKKU_LIB_ROOT}/data/${PLUGIN_COMMAND_PREFIX}/dokku-datastore" invoke "$PLUGIN_COMMAND_PREFIX" %[3]s "$@"
}

service-%[1]s-cmd "$@"
`

// PluginSubcommand renders the script a plugin ships for one custom command.
func PluginSubcommand(name string, declared definition.Command, data DocumentationData) (string, error) {
	description, err := RenderDocumentation(declared.Description, data)
	if err != nil {
		return "", fmt.Errorf("unable to render the description of %s: %w", name, err)
	}

	return fmt.Sprintf(subcommandTemplate, name, description, name), nil
}
