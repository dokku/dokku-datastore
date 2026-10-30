// Package hostenv is what the binary knows about the machine it runs on: where
// dokku keeps its data, which user owns it, and the sidecar images the plugin
// pins. It is a leaf, so anything may use it without dragging in the datastore
// abstraction.
package hostenv

import (
	"os"
	"path/filepath"
	"strings"
)

// Sidecar images the plugin runs alongside a service. They are pinned rather
// than floating, because a service that silently changes its ambassador or its
// backup tool on a docker pull is a service nobody can reason about.
const (
	// AmbassadorImage publishes a service's ports, so that the service container
	// itself never has to
	AmbassadorImage = "dokku/ambassador:0.8.4"

	// S3BackupImage ships a dump to an s3 bucket
	S3BackupImage = "dokku/s3backup:0.20.0"

	// BusyboxImage is the neutral image used to touch a service's files as root
	BusyboxImage = "busybox:1.37.0-uclibc"

	// WaitImage probes a port until a service answers on it
	WaitImage = "dokku/wait:0.9.3"
)

// defaultLibRoot is where dokku keeps its data when nothing says otherwise.
const defaultLibRoot = "/var/lib/dokku"

// defaultRoot is dokku's own home when nothing says otherwise.
const defaultRoot = "/home/dokku"

// defaultSystemUser is the user and group dokku's files belong to.
const defaultSystemUser = "dokku"

// Root is dokku's home, where it keeps files such as the global domains. It is
// read with a default rather than required, since nothing the binary reads
// from it is needed to answer, and a host without dokku does not set it.
func Root() string {
	if root := os.Getenv("DOKKU_ROOT"); root != "" {
		return root
	}

	return defaultRoot
}

// LibRoot is the dokku library root as this process sees it.
func LibRoot() string {
	if root := os.Getenv("DOKKU_LIB_ROOT"); root != "" {
		return root
	}

	return defaultLibRoot
}

// LibHostRoot is the dokku library root as dockerd sees it. It differs from
// LibRoot on a docker-in-docker install, which is why a bind mount source must
// be built from this one and a file the tool reads must be built from LibRoot.
func LibHostRoot() string {
	if root := os.Getenv("DOKKU_LIB_HOST_ROOT"); root != "" {
		return root
	}

	return LibRoot()
}

// PluginPath is where dokku keeps its plugins.
func PluginPath() string {
	return filepath.Join(LibRoot(), "plugins")
}

// DataRoot is where every datastore's services live.
func DataRoot() string {
	return filepath.Join(LibRoot(), "services")
}

// HostDataRoot is DataRoot as dockerd sees it.
func HostDataRoot() string {
	return filepath.Join(LibHostRoot(), "services")
}

// SystemUser is the user dokku's files belong to.
func SystemUser() string {
	if user := os.Getenv("DOKKU_SYSTEM_USER"); user != "" {
		return user
	}

	return defaultSystemUser
}

// SystemGroup is the group dokku's files belong to.
func SystemGroup() string {
	if group := os.Getenv("DOKKU_SYSTEM_GROUP"); group != "" {
		return group
	}

	return defaultSystemUser
}

// PluginBasePath is the directory holding every enabled plugin, which is what
// dokku exports. It is a plugin's neighbours as much as itself, so a plugin's
// own directory is PluginCheckout.
func PluginBasePath() string {
	return os.Getenv("PLUGIN_BASE_PATH")
}

// PluginCommandPrefix is the datastore type the plugin running this binary
// serves. The plugin's config exports it, and its subcommands pass it as an
// argument as well.
func PluginCommandPrefix() string {
	return os.Getenv("PLUGIN_COMMAND_PREFIX")
}

// PluginCheckout is the directory of the plugin dokku is running this binary
// from. A definition found below it overrides the one compiled into the binary,
// which is how a plugin ships a datastore of its own.
//
// The plugin's own name has to be joined onto the base path, because that path
// is the directory every plugin sits in rather than any one of them. Without
// the name this resolved to a sibling called datastore, so an override was
// looked for somewhere no plugin installs anything and was never found.
//
// An empty result means there is nowhere to look, which is what running outside
// a dokku install looks like. It is also empty under a command namespace: the
// plugin serving every datastore ships no definitions of its own, and a
// PLUGIN_COMMAND_PREFIX it inherited from a per-datastore plugin's environment
// names a checkout that is not its own.
func PluginCheckout() string {
	if CommandNamespace() != "" {
		return ""
	}

	base, prefix := PluginBasePath(), PluginCommandPrefix()
	if base == "" || prefix == "" {
		return ""
	}

	return filepath.Join(base, prefix)
}

// PluginEnabledPath is the directory holding a symlink for every enabled
// plugin. Dokku exports it to everything it runs, and PLUGIN_PATH is where it
// keeps it when that export is missing.
func PluginEnabledPath() string {
	if enabled := os.Getenv("PLUGIN_ENABLED_PATH"); enabled != "" {
		return enabled
	}

	if base := os.Getenv("PLUGIN_PATH"); base != "" {
		return filepath.Join(base, "enabled")
	}

	return filepath.Join(PluginPath(), "enabled")
}

// commandNamespace is the dokku command every datastore is reached through,
// and empty when each datastore is a plugin of its own
var commandNamespace string

// SetCommandNamespace makes every datastore reachable through a single dokku
// command, as in `dokku datastore:create postgres lollipop`, rather than
// through a command named for the datastore, as in `dokku postgres:create
// lollipop`. It is set once, by whatever embeds the binary as such a plugin,
// before anything else runs.
func SetCommandNamespace(namespace string) {
	commandNamespace = namespace
}

// CommandNamespace is the dokku command every datastore is reached through, or
// empty when each datastore is a plugin of its own.
func CommandNamespace() string {
	return commandNamespace
}

// DokkuCommand is how a user runs one of a datastore's commands, so that a
// hint or a crontab line names a command that exists whichever way the
// datastore is installed.
func DokkuCommand(commandPrefix string, name string, args ...string) string {
	parts := append([]string{"dokku", Invocation(commandPrefix, name)}, args...)
	return strings.Join(parts, " ")
}

// Invocation is a datastore's command as dokku is asked for it, without the
// dokku in front: postgres:create for a plugin of its own, and datastore:create
// postgres under a command namespace.
func Invocation(commandPrefix string, name string) string {
	if namespace := CommandNamespace(); namespace != "" {
		return namespace + ":" + name + " " + commandPrefix
	}

	return commandPrefix + ":" + name
}
