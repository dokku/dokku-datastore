package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// alternateAliasColors are the suffixes tried, in order, when the default alias
// is already in use. This list matches the bash datastore plugins.
var alternateAliasColors = []string{
	"AQUA", "BLACK", "BLUE", "FUCHSIA", "GRAY", "GREEN", "LIME", "MAROON",
	"NAVY", "OLIVE", "PURPLE", "RED", "SILVER", "TEAL", "WHITE", "YELLOW",
}

// SkippingRestartMessage is logged when an app is not restarted after a link change
const SkippingRestartMessage = "Skipping restart of linked app"

// AppEnvironment returns the environment variables set on an app. It is not
// merged with the global environment, matching what the bash plugins read.
//
// The installed dokku is asked rather than the files being read directly.
// Linking dokku's config package in pinned the storage layout at build time,
// and dokku moved app config from DOKKU_ROOT/<app>/ENV to
// DOKKU_LIB_ROOT/config/<app>/ENV in 0.38: against an older dokku the writes
// landed where nothing reads, so linking reported success and the app never
// received the url.
func AppEnvironment(ctx context.Context, appName string) (map[string]string, error) {
	// the flags precede the app name, because the subcommand stops reading them
	// at the first positional argument
	result, err := execx.Run(ctx, common.ExecCommandInput{
		Command: "dokku",
		Args:    []string{"config:export", "--format", "json", appName},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to read the config for app %s: %w", appName, err)
	}

	environment := map[string]string{}
	contents := strings.TrimSpace(result.StdoutContents())
	if contents == "" {
		return environment, nil
	}

	if err := json.Unmarshal([]byte(contents), &environment); err != nil {
		return nil, fmt.Errorf("unable to parse the config for app %s: %w", appName, err)
	}

	return environment, nil
}

// SchemeForApp returns the scheme to build a service url with, honoring the
// per-app override the datastore exposes. It reads from an environment already
// in hand, since every caller has just fetched one.
func SchemeForApp(s *service.Datastore, environment map[string]string) string {
	return environment[fmt.Sprintf("%s_DATABASE_SCHEME", s.Properties().PluginVariable)]
}

// ConfigSetArgs builds the argv that sets config variables on an app.
//
// The flags precede the app name, because the subcommand stops reading flags at
// the first positional argument: --no-restart after the app would be taken for a
// variable to set.
func ConfigSetArgs(appName string, entries map[string]string, restart bool) []string {
	args := []string{"config:set"}
	if !restart {
		args = append(args, "--no-restart")
	}

	args = append(args, appName)

	// sorted, so that setting the same variables twice is the same command
	for _, key := range slices.Sorted(maps.Keys(entries)) {
		args = append(args, fmt.Sprintf("%s=%s", key, entries[key]))
	}

	return args
}

// ConfigUnsetArgs builds the argv that removes config variables from an app.
func ConfigUnsetArgs(appName string, keys []string, restart bool) []string {
	args := []string{"config:unset"}
	if !restart {
		args = append(args, "--no-restart")
	}

	args = append(args, appName)

	return append(args, keys...)
}

// SetAppConfig sets config variables on an app through the installed dokku.
func SetAppConfig(ctx context.Context, appName string, entries map[string]string, restart bool) error {
	_, err := execx.Run(ctx, common.ExecCommandInput{
		Command:      "dokku",
		Args:         ConfigSetArgs(appName, entries, restart),
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("unable to set the config for app %s: %w", appName, err)
	}

	return nil
}

// UnsetAppConfig removes config variables from an app through the installed
// dokku.
func UnsetAppConfig(ctx context.Context, appName string, keys []string, restart bool) error {
	_, err := execx.Run(ctx, common.ExecCommandInput{
		Command:      "dokku",
		Args:         ConfigUnsetArgs(appName, keys, restart),
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("unable to unset the config for app %s: %w", appName, err)
	}

	return nil
}

// ConfigKeysForURL returns the config keys on an app whose value points at the
// given service url, sorted so the output is stable
func ConfigKeysForURL(environment map[string]string, serviceURL string) []string {
	keys := []string{}
	for key, value := range environment {
		if strings.Contains(value, serviceURL) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)

	return keys
}

// AlternateAlias returns the first alternate alias that is not already in use on
// the app, or an empty string when every alias is taken
func AlternateAlias(s *service.Datastore, environment map[string]string) string {
	for _, color := range alternateAliasColors {
		alias := fmt.Sprintf("%s_%s", s.Properties().AltAlias, color)
		if _, ok := environment[fmt.Sprintf("%s_URL", alias)]; !ok {
			return alias
		}
	}

	return ""
}

// LinkServiceInput is the input for the LinkService function
type LinkServiceInput struct {
	// Alias is an alternate alias to expose the service url as
	Alias string

	// AppName is the name of the app to link the service to
	AppName string

	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

	// NoRestart is whether to skip restarting the app
	NoRestart bool

	// Querystring is appended to the service url
	Querystring string

	// ServiceName is the name of the service to link
	ServiceName string
}

// LinkService links a service to an app
func LinkService(ctx context.Context, input LinkServiceInput) error {
	environment, err := AppEnvironment(ctx, input.AppName)
	if err != nil {
		return err
	}

	serviceURL := input.Datastore.URL(input.ServiceName, SchemeForApp(input.Datastore, environment))
	linkedKeys := ConfigKeysForURL(environment, serviceURL)

	alias := input.Datastore.Properties().DefaultAlias
	if input.Alias != "" {
		alias = input.Alias
		if _, ok := environment[fmt.Sprintf("%s_URL", alias)]; ok {
			return fmt.Errorf("Specified alias %s already in use", alias) //nolint:staticcheck // matches the bash datastore plugins
		}
	} else if _, ok := environment[fmt.Sprintf("%s_URL", alias)]; ok {
		alias = AlternateAlias(input.Datastore, environment)
	}

	if alias == "" {
		return errors.New("Unable to use default or generated URL alias") //nolint:staticcheck // matches the bash datastore plugins
	}

	// checked after the alias so that relinking reports the existing key rather
	// than complaining about the alias it would have generated
	if len(linkedKeys) > 0 {
		return fmt.Errorf("Already linked as %s", strings.Join(linkedKeys, " ")) //nolint:staticcheck // matches the bash datastore plugins
	}

	if err := callServiceAction(ctx, input.Datastore, "pre-link", input.ServiceName, input.AppName); err != nil {
		return err
	}

	if err := service.AddLinkedApp(ctx, service.LinkedAppsInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	}, input.AppName); err != nil {
		return err
	}

	if err := dockerOption(ctx, "add", input.Datastore, input.ServiceName, input.AppName); err != nil {
		return err
	}

	if input.Querystring != "" {
		serviceURL = fmt.Sprintf("%s?%s", serviceURL, input.Querystring)
	}

	if err := callServiceAction(ctx, input.Datastore, "post-link", input.ServiceName, input.AppName); err != nil {
		return err
	}

	if err := SetAppConfig(ctx, input.AppName, map[string]string{
		fmt.Sprintf("%s_URL", alias): serviceURL,
	}, !input.NoRestart); err != nil {
		return err
	}

	return callServiceAction(ctx, input.Datastore, "post-link-complete", input.ServiceName, input.AppName)
}

// UnlinkServiceInput is the input for the UnlinkService function
type UnlinkServiceInput struct {
	// AppName is the name of the app to unlink the service from
	AppName string

	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

	// NoRestart is whether to skip restarting the app
	NoRestart bool

	// ServiceName is the name of the service to unlink
	ServiceName string
}

// UnlinkService unlinks a service from an app
func UnlinkService(ctx context.Context, input UnlinkServiceInput) error {
	// an app that has been deleted has no config to unset, no docker options to
	// remove and nothing to restart, but its name still has to come out of the
	// links file or the service can never be destroyed. The service-action
	// triggers are skipped too: they are handed an app name, and firing them
	// for an app that is gone asks other plugins to act on nothing.
	if !service.AppExists(input.AppName) {
		return service.RemoveLinkedApp(ctx, service.LinkedAppsInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
		}, input.AppName)
	}

	environment, err := AppEnvironment(ctx, input.AppName)
	if err != nil {
		return err
	}

	serviceURL := input.Datastore.URL(input.ServiceName, SchemeForApp(input.Datastore, environment))
	linkedKeys := ConfigKeysForURL(environment, serviceURL)

	if err := callServiceAction(ctx, input.Datastore, "pre-unlink", input.ServiceName, input.AppName); err != nil {
		return err
	}

	// the links file and the docker options are cleaned up even when the app has
	// no config pointing at the service, so a partial link cannot be stranded
	if err := service.RemoveLinkedApp(ctx, service.LinkedAppsInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	}, input.AppName); err != nil {
		return err
	}

	if err := dockerOption(ctx, "remove", input.Datastore, input.ServiceName, input.AppName); err != nil {
		return err
	}

	if len(linkedKeys) == 0 {
		return fmt.Errorf("Not linked to app %s", input.AppName) //nolint:staticcheck // matches the bash datastore plugins
	}

	if err := callServiceAction(ctx, input.Datastore, "post-unlink", input.ServiceName, input.AppName); err != nil {
		return err
	}

	if err := UnsetAppConfig(ctx, input.AppName, linkedKeys, !input.NoRestart); err != nil {
		return err
	}

	return callServiceAction(ctx, input.Datastore, "post-unlink-complete", input.ServiceName, input.AppName)
}

// callServiceAction fires one of the service-action triggers for a link change
func callServiceAction(ctx context.Context, s *service.Datastore, action string, serviceName string, appName string) error {
	_, err := execx.PlugnTrigger(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{action, s.ServiceType(), serviceName, appName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action %s trigger: %w", action, err)
	}

	return nil
}

// dockerOption adds or removes the container link for an app across every phase
func dockerOption(ctx context.Context, operation string, s *service.Datastore, serviceName string, appName string) error {
	option := fmt.Sprintf("--link %s:%s", service.ContainerName(s, serviceName), service.DNSHostname(s, serviceName))
	_, err := execx.Run(ctx, common.ExecCommandInput{
		Command: "dokku",
		Args:    []string{fmt.Sprintf("docker-options:%s", operation), appName, "build,deploy,run", option},
	})
	if err != nil {
		return fmt.Errorf("failed to %s the docker option for app %s: %w", operation, appName, err)
	}

	return nil
}
