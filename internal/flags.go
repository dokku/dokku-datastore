package internal

import (
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal/service"
)

// UpdateFlagFromEnvInput is the input for the UpdateFlagFromEnv function
type UpdateFlagFromEnvInput struct {
	// ConfigOptions is the configuration options to update from the environment
	ConfigOptions string
	// CustomEnv is the custom environment variables to update from the environment
	CustomEnv string
	// DatastoreType is the type of datastore to update the flags for
	DatastoreType string
	// Image is the image to update from the environment
	Image string
	// ImageVersion is the image version to update from the environment
	ImageVersion string
}

// UpdateFlagFromEnv updates the flags from the environment
func UpdateFlagFromEnv(input UpdateFlagFromEnvInput) (UpdateFlagFromEnvInput, error) {
	if input.DatastoreType == "" {
		return input, fmt.Errorf("datastore type is required")
	}

	serviceWrapper, ok := service.Services[input.DatastoreType]
	if !ok {
		return input, fmt.Errorf("datastore type %s is not supported", input.DatastoreType)
	}

	defaultImage := serviceWrapper.Properties().DefaultImage
	defaultImageVersion := serviceWrapper.Properties().DefaultImageVersion
	configVariable := serviceWrapper.Properties().ConfigVariable
	envVariable := serviceWrapper.Properties().EnvVariable

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
