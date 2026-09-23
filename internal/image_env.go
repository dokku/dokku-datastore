package internal

import (
	"os"

	"github.com/dokku/dokku-datastore/internal/service"
)

// LegacyImageVariable and LegacyImageVersionVariable are the names the bash
// datastore plugins used internally. Their config.sh derived them from the
// documented pair below, and this binary has no config.sh, so reading only
// these was reading names nothing sets.
//
// They are still read, second, because a host carried over from those plugins
// may export them. Second rather than first because neither is scoped to a
// datastore: a host that exports one means it for every plugin on the box,
// which is a coarser statement than naming redis.
const (
	LegacyImageVariable        = "PLUGIN_IMAGE"
	LegacyImageVersionVariable = "PLUGIN_IMAGE_VERSION"
)

// ImageFromEnv is the image an operator asked a plugin to run, under the name
// the plugin's own readme documents - REDIS_IMAGE for redis.
//
// One function rather than one per caller. The readme is generated from the
// same answer create acts on, so an operator who follows it gets the service
// they were shown rather than one on the definition's own image.
func ImageFromEnv(properties service.ServiceStruct) string {
	if image := os.Getenv(properties.PluginVariable + "_IMAGE"); image != "" {
		return image
	}

	return os.Getenv(LegacyImageVariable)
}

// ImageVersionFromEnv is the version half of the same answer.
func ImageVersionFromEnv(properties service.ServiceStruct) string {
	if imageVersion := os.Getenv(properties.PluginVariable + "_IMAGE_VERSION"); imageVersion != "" {
		return imageVersion
	}

	return os.Getenv(LegacyImageVersionVariable)
}
