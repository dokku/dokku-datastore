package internal

import "fmt"

// triggerTemplate is the file a plugin ships for a dokku trigger its datastore
// implements.
//
// dokku finds a trigger by looking for a file named after it in the plugin
// directory, so the file exists whatever else happens. Generating it is what
// keeps the work in the definition: this dispatches and does nothing else.
const triggerTemplate = `#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/config"
set -eo pipefail
[[ $DOKKU_TRACE ]] && set -x

# generated from the datastore definition. do not edit: it is rewritten by
# make generate.
"${DOKKU_LIB_ROOT}/data/${PLUGIN_COMMAND_PREFIX}/dokku-datastore" trigger %[1]s "$PLUGIN_COMMAND_PREFIX" "$@"
`

// PluginTrigger renders the file a plugin ships for one dokku trigger.
func PluginTrigger(name string) string {
	return fmt.Sprintf(triggerTemplate, name)
}
