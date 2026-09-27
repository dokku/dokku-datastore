package internal

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dokku/dokku-datastore/internal/service"
)

// PromoteServiceInput is the input for the PromoteService function
type PromoteServiceInput struct {
	// AppName is the name of the app to promote the service on
	AppName string

	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

	// Logger reports progress
	Logger Ui

	// ServiceName is the name of the service to promote
	ServiceName string
}

// PromotionEntries returns the config entries that promoting a service would
// write, given the keys on the app that hold its url, or an error explaining why
// the service cannot be promoted. It also returns the key the url displaced from
// the default variable is preserved under, empty when it is not preserved.
func PromotionEntries(input PromoteServiceInput, environment map[string]string, linkedKeys []string) (map[string]string, string, error) {
	defaultKey := fmt.Sprintf("%s_URL", input.Datastore.Properties().DefaultAlias)

	if len(linkedKeys) == 0 {
		return nil, "", fmt.Errorf("Not linked to app %s", input.AppName) //nolint:staticcheck // matches the bash datastore plugins
	}

	if slices.Contains(linkedKeys, defaultKey) {
		return nil, "", fmt.Errorf("Service %s already promoted as %s", input.ServiceName, defaultKey) //nolint:staticcheck // matches the bash datastore plugins
	}

	entries := map[string]string{}
	preservedKey := ""

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
				return nil, "", errors.New("Unable to use default or generated URL alias") //nolint:staticcheck // matches the bash datastore plugins
			}

			preservedKey = fmt.Sprintf("%s_URL", alias)
			entries[preservedKey] = previousURL
		}
	}

	entries[defaultKey] = environment[linkedKeys[0]]

	return entries, preservedKey, nil
}

// PromoteService makes a linked service the one exposed on the default config
// variable for an app
func PromoteService(ctx context.Context, input PromoteServiceInput) error {
	environment, err := AppEnvironment(ctx, input.AppName)
	if err != nil {
		return err
	}

	serviceURL := input.Datastore.URL(input.ServiceName, SchemeForApp(input.Datastore, environment))
	linkedKeys := LinkedConfigKeys(environment, service.LinkConfigKeys(input.Datastore, input.ServiceName, input.AppName), serviceURL)
	entries, preservedKey, err := PromotionEntries(input, environment, linkedKeys)
	if err != nil {
		return err
	}

	if err := SetAppConfig(ctx, input.AppName, entries, true); err != nil {
		return fmt.Errorf("unable to set the config for app %s: %w", input.AppName, err)
	}

	defaultKey := fmt.Sprintf("%s_URL", input.Datastore.Properties().DefaultAlias)
	if err := recordLinkConfigKeys(input.Logger, input.Datastore, input.ServiceName, input.AppName, append(linkedKeys, defaultKey)); err != nil {
		return err
	}

	return releaseDefaultKey(ctx, input, environment, defaultKey, preservedKey)
}

// releaseDefaultKey takes the default variable out of the record of any other
// service of the datastore that had it on the app, since it now holds the
// promoted service's url. Where the url it held was that service's and was
// preserved under a generated alias, that alias is recorded in its place.
func releaseDefaultKey(ctx context.Context, input PromoteServiceInput, environment map[string]string, defaultKey string, preservedKey string) error {
	services, err := ListServices(ctx, ListServicesInput{Datastore: input.Datastore})
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	for _, serviceName := range services {
		if serviceName == input.ServiceName {
			continue
		}

		recorded := service.LinkConfigKeys(input.Datastore, serviceName, input.AppName)
		if !slices.Contains(recorded, defaultKey) {
			continue
		}

		keys := slices.DeleteFunc(slices.Clone(recorded), func(key string) bool {
			return key == defaultKey
		})
		if preservedKey != "" {
			serviceURL := urlAfterScheme(input.Datastore.URL(serviceName, SchemeForApp(input.Datastore, environment)))
			if serviceURL != "" && strings.Contains(environment[defaultKey], serviceURL) {
				keys = append(keys, preservedKey)
			}
		}

		if err := recordLinkConfigKeys(input.Logger, input.Datastore, serviceName, input.AppName, keys); err != nil {
			return err
		}
	}

	return nil
}
