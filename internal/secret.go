package internal

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dokku/dokku-datastore/internal/service"
)

// secretValuePattern is what a password set on an existing service may hold.
// The value is written into the dsn, into config files and into the statements
// that change it without being escaped, so it is held to the characters a url
// carries as they are, which is everything a generated password is made of.
var secretValuePattern = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// validateSecretValue reports whether a value is one a password can be changed
// to. Unlike every other property, a password cannot be cleared: a service
// without one would hand its apps a dsn the datastore refuses.
func validateSecretValue(key string) func(value string) error {
	return func(value string) error {
		if value == "" {
			return fmt.Errorf("%s cannot be cleared, only changed", key)
		}

		if !secretValuePattern.MatchString(value) {
			return fmt.Errorf("invalid %s value, it may only contain letters, digits, '.', '_', '~' and '-'", key)
		}

		return nil
	}
}

// secretChangeable refuses a password the datastore a service runs has no way
// to change, or does not have at all
func secretChangeable(property string, secretName string) func(s *service.Datastore, serviceName string, value string) error {
	return func(s *service.Datastore, serviceName string, value string) error {
		secret, ok := s.Definition.Dokku.Secrets[secretName]
		if !ok {
			return fmt.Errorf("the %s datastore has no %s, so it cannot be set", s.Definition.Dokku.Plugin, property)
		}

		if !secret.Changeable() {
			return fmt.Errorf("the %s datastore has no way to change the %s of a service that exists", s.Definition.Dokku.Plugin, property)
		}

		return nil
	}
}

// secretReader reads a secret as the templates read it
func secretReader(secretName string) func(s *service.Datastore, serviceName string) (string, error) {
	return func(s *service.Datastore, serviceName string) (string, error) {
		return s.SecretValue(serviceName, secretName), nil
	}
}

// ChangeServiceSecretInput is the input for the ChangeServiceSecret function
type ChangeServiceSecretInput struct {
	// Datastore is the service's datastore
	Datastore *service.Datastore

	// ServiceName is the service whose secret is changed
	ServiceName string

	// Property is the property naming the secret
	Property SettableProperty

	// Value is what the secret is changed to
	Value string

	// AllowRestart is whether the service may be stopped and started, which
	// a secret the datastore reads as it starts cannot be changed without
	AllowRestart bool

	// NoRestart leaves the linked apps running on the config they had until
	// they are next restarted
	NoRestart bool

	// Logger reports what is done
	Logger Ui
}

// linkedAppURL is where a linked app holds the url of a service, and the url
// it holds
type linkedAppURL struct {
	// appName is the linked app
	appName string

	// scheme is the scheme the app asked for, empty for the datastore's own
	scheme string

	// url is the service url before the secret changed
	url string

	// keys are the app's config keys holding the url
	keys []string

	// jdbcKeys are the app's config keys holding the jdbc values, and jdbc
	// those values before the secret changed
	jdbcKeys []string
	jdbc     service.JDBCValues

	// environment is the app's config as it was read
	environment map[string]string
}

// ChangeServiceSecret changes a password a service keeps: in the datastore,
// in the file the service keeps it in, and in the url every linked app holds.
//
// The datastore is told first, so that a change it refuses leaves everything
// as it was. A password the datastore reads as it starts is written and the
// container made again, which needs the operator's consent through
// --allow-restart, since the service is down while it happens. The linked apps
// are found before anything changes, as they are found by the url they hold.
func ChangeServiceSecret(ctx context.Context, input ChangeServiceSecretInput) error {
	property := input.Property
	secret, ok := input.Datastore.Definition.Dokku.Secrets[property.secret]
	if !ok || !secret.Changeable() {
		return secretChangeable(property.Name, property.secret)(input.Datastore, input.ServiceName, input.Value)
	}

	if input.Datastore.SecretValue(input.ServiceName, property.secret) == input.Value {
		input.Logger.Info(fmt.Sprintf("%s is unchanged, so neither %s nor its linked apps were touched", property.Name, input.ServiceName))
		return nil
	}

	if secret.RestartsService() && !input.AllowRestart {
		return fmt.Errorf("the %s datastore only takes a new %s as the service starts, so changing it stops and starts %s: run the set again with --allow-restart", input.Datastore.Definition.Dokku.Plugin, property.Name, input.ServiceName)
	}

	// a change told to the datastore is told to it while it runs, and one in a
	// container beside it shares its network
	if !secret.RestartsService() {
		switch service.Status(ctx, service.StatusInput{Datastore: input.Datastore, ServiceName: input.ServiceName}) {
		case "running", "restarting":
		default:
			return fmt.Errorf("service %s is not running, and the datastore can only be told its new %s while it is: start it with `dokku %s:start %s`", input.ServiceName, property.Name, input.Datastore.Properties().CommandPrefix, input.ServiceName)
		}
	}

	linked, err := linkedAppURLs(ctx, input.Datastore, input.ServiceName, input.Logger)
	if err != nil {
		return err
	}

	if err := input.Datastore.ChangeSecret(ctx, input.ServiceName, property.secret, input.Value); err != nil {
		return err
	}

	// the linked apps are pointed at the new password even when the service
	// cannot be brought back up on it, since the file already holds it and the
	// service takes it the next time it starts
	var restartErr error
	if secret.Rebuild {
		if err := RecreateServiceContainer(ctx, RecreateServiceContainerInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
			Logger:      input.Logger,
			Reason:      fmt.Sprintf("for the new %s to take effect", property.Name),
		}); err != nil {
			restartErr = fmt.Errorf("%s was written, but %s could not be restarted, and takes the new %s once it is started: %w", property.Name, input.ServiceName, property.Name, err)
		}
	} else if secret.RestartsService() {
		// an offline change brings the service back up, and set returns once it
		// answers again
		restartErr = WaitForService(ctx, WaitForServiceInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
			Logger:      input.Logger,
		})
	}

	return errors.Join(restartErr, updateLinkedAppURLs(ctx, input, linked))
}

// linkedAppURLs are the urls the apps linked to a service hold, read while
// they still name the service as it is
func linkedAppURLs(ctx context.Context, s *service.Datastore, serviceName string, logger Ui) ([]linkedAppURL, error) {
	apps := service.LinkedApps(ctx, service.LinkedAppsInput{
		Datastore:   s,
		ServiceName: serviceName,
	})

	linked := make([]linkedAppURL, 0, len(apps))
	for _, appName := range apps {
		environment, err := AppEnvironment(ctx, appName)
		if err != nil {
			return nil, err
		}

		scheme := SchemeForApp(s, environment)
		serviceURL := s.URL(serviceName, scheme)
		keys := LinkedConfigKeys(environment, service.LinkConfigKeys(s, serviceName, appName), serviceURL)
		jdbcKeys := linkedJDBCKeys(s, serviceName, appName, environment)
		if len(keys) == 0 && len(jdbcKeys) == 0 {
			logger.Warn(WarnInput{
				Warning: fmt.Sprintf("App %s is linked to %s, but none of its config holds the service url, so it is left as it is", appName, serviceName),
			})
			continue
		}

		jdbc, _ := s.JDBC(serviceName)
		linked = append(linked, linkedAppURL{
			appName:     appName,
			scheme:      scheme,
			url:         serviceURL,
			keys:        keys,
			jdbcKeys:    jdbcKeys,
			jdbc:        jdbc,
			environment: environment,
		})
	}

	return linked, nil
}

// updateLinkedAppURLs points every linked app at the service url as it is now,
// replacing only the part of each value that names the service, so that a
// scheme or querystring an app has around it is kept. The jdbc url is replaced
// the same way, and the jdbc username and password are set to their new values.
//
// Every app is updated even when one cannot be, since an app left out only
// because another failed would be one more app holding a url that no longer
// works.
func updateLinkedAppURLs(ctx context.Context, input ChangeServiceSecretInput, linked []linkedAppURL) error {
	failed := []error{}
	for _, app := range linked {
		entries := map[string]string{}

		before := urlAfterScheme(app.url)
		after := urlAfterScheme(input.Datastore.URL(input.ServiceName, app.scheme))
		if before != after {
			for _, key := range app.keys {
				entries[key] = strings.ReplaceAll(app.environment[key], before, after)
			}
		}

		if jdbc, ok := input.Datastore.JDBC(input.ServiceName); ok && jdbc != app.jdbc {
			for _, key := range app.jdbcKeys {
				value := app.environment[key]
				updated := strings.ReplaceAll(value, app.jdbc.URL, jdbc.URL)
				switch value {
				case app.jdbc.Password:
					updated = jdbc.Password
				case app.jdbc.Username:
					updated = jdbc.Username
				}

				// only what changed, since the username rarely does
				if updated != value {
					entries[key] = updated
				}
			}
		}

		if len(entries) == 0 {
			continue
		}

		input.Logger.Info(fmt.Sprintf("Updating the url app %s holds as %s", app.appName, strings.Join(slices.Sorted(maps.Keys(entries)), " ")))
		if input.NoRestart {
			input.Logger.Info(fmt.Sprintf("%s %s", SkippingRestartMessage, app.appName))
		}

		if err := SetAppConfig(ctx, app.appName, entries, !input.NoRestart); err != nil {
			failed = append(failed, fmt.Errorf("%s was changed, but app %s still holds the old url: %w", input.Property.Name, app.appName, err))
		}
	}

	return errors.Join(failed...)
}
