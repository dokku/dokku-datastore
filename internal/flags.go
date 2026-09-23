package internal

import (
	"os"
	"strings"

	"github.com/dokku/dokku-datastore/internal/service"
)

// CheckLogConfig reports whether the log driver and options a command was given
// are ones docker will take.
//
// Checked where the command starts rather than where the container is made: a
// malformed option is refused by docker, so a create that only found out at the
// end would have written the service's directories, its credentials and its
// config files before saying so, and an upgrade would have taken the old
// container away first.
func CheckLogConfig(driver string, options []string) error {
	if err := service.ValidateLogDriver(driver); err != nil {
		return err
	}

	return service.ValidateLogOptions(strings.Join(options, ","))
}

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
