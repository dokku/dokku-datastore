package internal

import (
	"os"

	"github.com/dokku/dokku-datastore/internal/datastores"
)

// UpdateFlagFromEnvInput is the input for the UpdateFlagFromEnv function
type UpdateFlagFromEnvInput struct {
	// ConfigOptions is the configuration options to update from the environment
	ConfigOptions string
	// CustomEnv is the custom environment variables to update from the environment
	CustomEnv string
	// Image is the image to update from the environment
	Image string
	// ImageVersion is the image version to update from the environment
	ImageVersion string
	// Datastore is the service to update the flags for
	Datastore datastores.Datastore
}

// UpdateFlagFromEnv updates the flags from the environment
func UpdateFlagFromEnv(input UpdateFlagFromEnvInput) (UpdateFlagFromEnvInput, error) {
	properties := input.Datastore.Properties()
	defaultImage := properties.DefaultImage
	defaultImageVersion := properties.DefaultImageVersion
	configVariable := properties.ConfigVariable
	envVariable := properties.EnvVariable

	if input.ConfigOptions == "" {
		input.ConfigOptions = os.Getenv(configVariable)
	}

	if input.CustomEnv == "" {
		input.CustomEnv = os.Getenv(envVariable)
	}

	if input.Image == "" {
		input.Image = os.Getenv("PLUGIN_IMAGE")
	}

	if input.Image == "" {
		input.Image = defaultImage
	}

	if input.ImageVersion == "" {
		input.ImageVersion = os.Getenv("PLUGIN_IMAGE_VERSION")
	}

	if input.ImageVersion == "" {
		input.ImageVersion = defaultImageVersion
	}

	return input, nil
}
