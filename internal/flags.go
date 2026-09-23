package internal

import (
	"os"

	"github.com/dokku/dokku-datastore/internal/service"
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
	Datastore *service.Datastore
}

// UpdateFlagFromEnv fills the flags a command was not given from the
// environment, and stops there.
//
// The definition's defaults are deliberately not applied here. Applying them
// meant the resolver downstream was always handed two filled halves and could
// never tell an image an operator named from one it had supplied itself - which
// is how a custom image came to be run at the definition's version, at a tag
// that repository had never published. The default now belongs to resolveImage,
// which is the one place that knows the two halves are a pair.
func UpdateFlagFromEnv(input UpdateFlagFromEnvInput) (UpdateFlagFromEnvInput, error) {
	properties := input.Datastore.Properties()
	configVariable := properties.ConfigVariable
	envVariable := properties.EnvVariable

	if input.ConfigOptions == "" {
		input.ConfigOptions = os.Getenv(configVariable)
	}

	if input.CustomEnv == "" {
		input.CustomEnv = os.Getenv(envVariable)
	}

	if input.Image == "" {
		input.Image = ImageFromEnv(properties)
	}

	if input.ImageVersion == "" {
		input.ImageVersion = ImageVersionFromEnv(properties)
	}

	return input, nil
}
