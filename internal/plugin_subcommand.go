package internal

import (
	"fmt"
	"strings"
)

// subcommandTemplate is the script a plugin ships for each of its commands,
// the tool's own and the ones its datastore adds alike. It is the script the
// plugins used to keep by hand, so a plugin regenerated from it changes only
// where the tool has changed. The second verb is the dispatch, which is the
// only part that differs from one command to the next.
const subcommandTemplate = `#!/usr/bin/env bash
source "$(dirname "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)")/config"
set -eo pipefail
[[ $DOKKU_TRACE ]] && set -x

service-%[1]s-cmd() {
  declare desc="%[2]s"
  local cmd="$PLUGIN_COMMAND_PREFIX:%[1]s" argv=("$@")
  [[ ${argv[0]} == "$cmd" ]] && shift 1

%[3]s
}

service-%[1]s-cmd "$@"
`

// binaryPath is where a plugin's scripts find the binary
const binaryPath = `"${DOKKU_LIB_ROOT}/data/${PLUGIN_COMMAND_PREFIX}/dokku-datastore"`

// enterDispatch is the dispatch for enter. Everything after the service name is
// the command run in the container, and a flag in it such as mariabackup's
// --backup would otherwise be read as one of the binary's own.
const enterDispatch = `  # everything after the service name belongs to the command run in the
  # container, so it is fenced off from the binary's own flag and help parsing
  local args=()
  [[ $# -gt 0 ]] && args=("$1" -- "${@:2}")

  ` + binaryPath + ` enter "$PLUGIN_COMMAND_PREFIX" "${args[@]}"`

// subcommandDescriptionData is what a script's description is rendered
// against. The fields a plugin's config exports are left for the shell to
// expand, as the scripts kept by hand did, so the description reads what the
// plugin says about itself.
func subcommandDescriptionData(data DocumentationData) DocumentationData {
	data.CommandPrefix = "$PLUGIN_COMMAND_PREFIX"
	data.DefaultAlias = "${PLUGIN_DEFAULT_ALIAS}"
	data.Title = "$PLUGIN_SERVICE"
	return data
}

// descriptionEscaper escapes what would end or change a double quoted bash
// string, other than the $ the shell is meant to expand.
var descriptionEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`")

// PluginSubcommandInput is the input for PluginSubcommand
type PluginSubcommandInput struct {
	// Command is the command the script runs
	Command PluginCommand

	// Custom is whether the datastore declares the command for itself, which
	// the binary runs through invoke rather than under its own name
	Custom bool

	// Data is the template data the description is rendered against
	Data DocumentationData
}

// PluginSubcommand renders the script a plugin ships for one of its commands.
func PluginSubcommand(input PluginSubcommandInput) (string, error) {
	name := input.Command.Name()
	description, err := RenderDocumentation(input.Command.Description(), subcommandDescriptionData(input.Data))
	if err != nil {
		return "", fmt.Errorf("unable to render the description of %s: %w", name, err)
	}

	dispatch := fmt.Sprintf(`  %s %s "$PLUGIN_COMMAND_PREFIX" "$@"`, binaryPath, name)
	switch {
	case input.Custom:
		dispatch = fmt.Sprintf(`  %s invoke "$PLUGIN_COMMAND_PREFIX" %s "$@"`, binaryPath, name)
	case name == "enter":
		dispatch = enterDispatch
	}

	return fmt.Sprintf(subcommandTemplate, name, descriptionEscaper.Replace(description), dispatch), nil
}
