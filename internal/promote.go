package internal

import (
	"context"
	"errors"
	"fmt"
	"maps"
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

// JDBCPromotionEntries returns the config entries that promoting a service
// would write for its jdbc values, given the keys on the app that hold its url
// and its jdbc values, and the key the displaced url is preserved under.
//
// A service linked with --jdbc has its jdbc values copied onto the ones named
// after the default variable, so that they never point at a different service
// than the url does. The jdbc values they displace go with the displaced url
// when it is preserved, and are refused if the names they would move to are in
// use. A service linked without --jdbc has no entries, and the jdbc values on
// the app are left alone. It also returns whether the displaced jdbc values
// were preserved.
func JDBCPromotionEntries(defaultKey string, environment map[string]string, linkedKeys []string, jdbcKeys []string, preservedKey string) (map[string]string, bool, error) {
	entries := map[string]string{}

	// the jdbc values of the first key holding the url that has all three, as
	// the url itself is taken from the first key holding it
	var source *JDBCKeys
	for _, key := range linkedKeys {
		keys := JDBCConfigKeys(key)
		if !slices.ContainsFunc(keys.List(), func(jdbcKey string) bool { return !slices.Contains(jdbcKeys, jdbcKey) }) {
			source = &keys
			break
		}
	}
	if source == nil {
		return entries, false, nil
	}

	target := JDBCConfigKeys(defaultKey)
	preserved := false
	if preservedKey != "" {
		moved := JDBCConfigKeys(preservedKey)
		for _, pair := range [][2]string{{target.URL, moved.URL}, {target.Username, moved.Username}, {target.Password, moved.Password}} {
			value, ok := environment[pair[0]]
			if !ok {
				continue
			}

			if _, taken := environment[pair[1]]; taken {
				return nil, false, fmt.Errorf("Unable to preserve %s as %s, which is already in use", pair[0], pair[1]) //nolint:staticcheck // matches the other promote errors
			}

			entries[pair[1]] = value
			preserved = true
		}
	}

	entries[target.URL] = environment[source.URL]
	entries[target.Username] = environment[source.Username]
	entries[target.Password] = environment[source.Password]

	return entries, preserved, nil
}

// PromoteService makes a linked service the one exposed on the default config
// variable for an app
func PromoteService(ctx context.Context, input PromoteServiceInput) error {
	environment, err := AppEnvironment(ctx, input.AppName)
	if err != nil {
		return err
	}

	serviceURL := input.Datastore.LinkURL(input.ServiceName, input.AppName, SchemeForApp(input.Datastore, environment))
	linkedKeys := LinkedConfigKeys(environment, service.LinkConfigKeys(input.Datastore, input.ServiceName, input.AppName), serviceURL)
	entries, preservedKey, err := PromotionEntries(input, environment, linkedKeys)
	if err != nil {
		return err
	}

	defaultKey := fmt.Sprintf("%s_URL", input.Datastore.Properties().DefaultAlias)
	jdbcKeys := linkedJDBCKeys(input.Datastore, input.ServiceName, input.AppName, environment)
	jdbcEntries, jdbcPreserved, err := JDBCPromotionEntries(defaultKey, environment, linkedKeys, jdbcKeys, preservedKey)
	if err != nil {
		return err
	}
	maps.Copy(entries, jdbcEntries)

	if err := SetAppConfig(ctx, input.AppName, entries, true); err != nil {
		return fmt.Errorf("unable to set the config for app %s: %w", input.AppName, err)
	}

	if err := recordLinkConfigKeys(input.Logger, input.Datastore, input.ServiceName, input.AppName, append(linkedKeys, defaultKey)); err != nil {
		return err
	}

	jdbcPromoted := len(jdbcEntries) > 0
	defaultJDBC := JDBCConfigKeys(defaultKey)
	if jdbcPromoted {
		if err := recordLinkJDBCConfigKeys(input.Logger, input.Datastore, input.ServiceName, input.AppName, append(jdbcKeys, defaultJDBC.List()...)); err != nil {
			return err
		}
	} else if previous, ok := environment[defaultJDBC.URL]; ok {
		values, rendered := input.Datastore.LinkJDBC(input.ServiceName, input.AppName)
		if !rendered || !strings.Contains(previous, values.URL) {
			input.Logger.Warn(WarnInput{
				Warning: fmt.Sprintf("Service %s was not linked to app %s with --jdbc, so %s does not point at it", input.ServiceName, input.AppName, defaultJDBC.URL),
			})
		}
	}

	return releaseDefaultKey(ctx, input, environment, releaseDefaultKeyInput{
		defaultKey:    defaultKey,
		preservedKey:  preservedKey,
		jdbcPromoted:  jdbcPromoted,
		jdbcPreserved: jdbcPreserved,
	})
}

// releaseDefaultKeyInput is what releaseDefaultKey needs to know of a promotion
type releaseDefaultKeyInput struct {
	// defaultKey is the default variable, which now holds the promoted url
	defaultKey string

	// preservedKey is where the url it held was preserved, empty if nowhere
	preservedKey string

	// jdbcPromoted is whether the jdbc values named after the default variable
	// now hold the promoted service's
	jdbcPromoted bool

	// jdbcPreserved is whether the jdbc values they held were preserved, named
	// after preservedKey
	jdbcPreserved bool
}

// releaseDefaultKey takes the default variable out of the record of any other
// service of the datastore that had it on the app, since it now holds the
// promoted service's url. Where the url it held was that service's and was
// preserved under a generated alias, that alias is recorded in its place. The
// jdbc values named after the default variable are released the same way when
// the promoted service's were copied onto them.
func releaseDefaultKey(ctx context.Context, input PromoteServiceInput, environment map[string]string, release releaseDefaultKeyInput) error {
	services, err := ListServices(ctx, ListServicesInput{Datastore: input.Datastore})
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	defaultJDBC := JDBCConfigKeys(release.defaultKey)
	for _, serviceName := range services {
		if serviceName == input.ServiceName {
			continue
		}

		recorded := service.LinkConfigKeys(input.Datastore, serviceName, input.AppName)
		if slices.Contains(recorded, release.defaultKey) {
			keys := slices.DeleteFunc(slices.Clone(recorded), func(key string) bool {
				return key == release.defaultKey
			})
			if release.preservedKey != "" {
				serviceURL := urlAfterScheme(input.Datastore.LinkURL(serviceName, input.AppName, SchemeForApp(input.Datastore, environment)))
				if serviceURL != "" && strings.Contains(environment[release.defaultKey], serviceURL) {
					keys = append(keys, release.preservedKey)
				}
			}

			if err := recordLinkConfigKeys(input.Logger, input.Datastore, serviceName, input.AppName, keys); err != nil {
				return err
			}
		}

		if !release.jdbcPromoted {
			continue
		}

		recordedJDBC := service.LinkJDBCConfigKeys(input.Datastore, serviceName, input.AppName)
		if !slices.ContainsFunc(defaultJDBC.List(), func(key string) bool { return slices.Contains(recordedJDBC, key) }) {
			continue
		}

		jdbcKeys := slices.DeleteFunc(slices.Clone(recordedJDBC), func(key string) bool {
			return slices.Contains(defaultJDBC.List(), key)
		})
		if release.jdbcPreserved {
			values, ok := input.Datastore.LinkJDBC(serviceName, input.AppName)
			if ok && strings.Contains(environment[defaultJDBC.URL], values.URL) {
				jdbcKeys = append(jdbcKeys, JDBCConfigKeys(release.preservedKey).List()...)
			}
		}

		if err := recordLinkJDBCConfigKeys(input.Logger, input.Datastore, serviceName, input.AppName, jdbcKeys); err != nil {
			return err
		}
	}

	return nil
}
