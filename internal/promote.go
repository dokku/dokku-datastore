package internal

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/dokku/dokku-datastore/internal/datastores"
)

// PromoteServiceInput is the input for the PromoteService function
type PromoteServiceInput struct {
	// AppName is the name of the app to promote the service on
	AppName string

	// Datastore is the datastore the service belongs to
	Datastore datastores.Datastore

	// ServiceName is the name of the service to promote
	ServiceName string
}

// PromotionEntries returns the config entries that promoting a service would
// write, or an error explaining why the service cannot be promoted
func PromotionEntries(input PromoteServiceInput, environment map[string]string, serviceURL string) (map[string]string, error) {
	defaultKey := fmt.Sprintf("%s_URL", input.Datastore.Properties().DefaultAlias)
	linkedKeys := ConfigKeysForURL(environment, serviceURL)

	if len(linkedKeys) == 0 {
		return nil, fmt.Errorf("Not linked to app %s", input.AppName) //nolint:staticcheck // matches the bash datastore plugins
	}

	if slices.Contains(linkedKeys, defaultKey) {
		return nil, fmt.Errorf("Service %s already promoted as %s", input.ServiceName, defaultKey) //nolint:staticcheck // matches the bash datastore plugins
	}

	entries := map[string]string{}

	// the url currently on the default variable is about to be displaced, so it
	// is preserved under a generated alias unless something else already points
	// at it
	if previousURL := environment[defaultKey]; previousURL != "" {
		holders := slices.DeleteFunc(ConfigKeysForURL(environment, previousURL), func(key string) bool {
			return key == defaultKey
		})

		if len(holders) == 0 {
			alias := AlternateAlias(input.Datastore, environment)
			if alias == "" {
				return nil, errors.New("Unable to use default or generated URL alias") //nolint:staticcheck // matches the bash datastore plugins
			}

			entries[fmt.Sprintf("%s_URL", alias)] = previousURL
		}
	}

	entries[defaultKey] = environment[linkedKeys[0]]

	return entries, nil
}

// PromoteService makes a linked service the one exposed on the default config
// variable for an app
func PromoteService(ctx context.Context, input PromoteServiceInput) error {
	environment, err := AppEnvironment(ctx, input.AppName)
	if err != nil {
		return err
	}

	serviceURL := input.Datastore.URL(input.ServiceName, SchemeForApp(input.Datastore, environment))
	entries, err := PromotionEntries(input, environment, serviceURL)
	if err != nil {
		return err
	}

	return SetAppConfig(ctx, input.AppName, entries, true)
}
