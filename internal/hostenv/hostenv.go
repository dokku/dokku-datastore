// Package hostenv is what the binary knows about the machine it runs on: where
// dokku keeps its data, which user owns it, and the sidecar images the plugin
// pins. It is a leaf, so anything may use it without dragging in the datastore
// abstraction.
package hostenv

import (
	"os"
	"path/filepath"
)

// Sidecar images the plugin runs alongside a service. They are pinned rather
// than floating, because a service that silently changes its ambassador or its
// backup tool on a docker pull is a service nobody can reason about.
const (
	// AmbassadorImage publishes a service's ports, so that the service container
	// itself never has to
	AmbassadorImage = "dokku/ambassador:0.8.2"

	// S3BackupImage ships a dump to an s3 bucket
	S3BackupImage = "dokku/s3backup:0.18.0"

	// BusyboxImage is the neutral image used to touch a service's files as root
	BusyboxImage = "busybox:1.37.0-uclibc"

	// WaitImage probes a port until a service answers on it
	WaitImage = "dokku/wait:0.9.3"
)

// defaultLibRoot is where dokku keeps its data when nothing says otherwise.
const defaultLibRoot = "/var/lib/dokku"

// defaultSystemUser is the user and group dokku's files belong to.
const defaultSystemUser = "dokku"

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
