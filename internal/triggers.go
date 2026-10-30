package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
)

// TriggerInput is the input shared by the plugin trigger implementations
type TriggerInput struct {
	// Datastore is the datastore the trigger is running for
	Datastore *service.Datastore

	// Logger reports progress
	Logger Ui
}

// linkedAppsInput builds the links file lookup for a service
func linkedAppsInput(s *service.Datastore, serviceName string) service.LinkedAppsInput {
	return service.LinkedAppsInput{Datastore: s, ServiceName: serviceName}
}

// CopyAppLinks records the new app name against every service the old app was
// linked to, and leaves the old name where it is. Both the clone and the rename
// trigger want that: a clone ends with two apps that both exist and are both
// linked, and a rename ends with one, because dokku destroys the old app as soon
// as this trigger returns and the pre-delete that fires takes the old name out
// through RemoveAppLinks.
func CopyAppLinks(ctx context.Context, input TriggerInput, oldAppName string, newAppName string) error {
	services, err := ListServices(ctx, ListServicesInput{Datastore: input.Datastore})
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	for _, serviceName := range services {
		if !slices.Contains(service.LinkedApps(ctx, linkedAppsInput(input.Datastore, serviceName)), oldAppName) {
			continue
		}

		if err := service.AddLinkedApp(ctx, linkedAppsInput(input.Datastore, serviceName), newAppName); err != nil {
			return err
		}

		// the new app's containers join the network for its own link when
		// they are made, so it has to be there for them
		if err := service.EnsureLinkNetwork(ctx, service.EnsureLinkNetworkInput{
			AppName:     newAppName,
			Datastore:   input.Datastore,
			ServiceName: serviceName,
		}); err != nil {
			return err
		}

		// dokku copies the config along with the app, so the new app holds the
		// url under the same keys. A link made before the keys were recorded
		// has none to copy, and recording none would forget any the new name
		// already had.
		if keys := service.LinkConfigKeys(input.Datastore, serviceName, oldAppName); len(keys) > 0 {
			if err := recordLinkConfigKeys(input.Logger, input.Datastore, serviceName, newAppName, keys); err != nil {
				return err
			}
		}

		// the jdbc values are copied along with the rest of the config
		if keys := service.LinkJDBCConfigKeys(input.Datastore, serviceName, oldAppName); len(keys) > 0 {
			if err := recordLinkJDBCConfigKeys(input.Logger, input.Datastore, serviceName, newAppName, keys); err != nil {
				return err
			}
		}

		// the copied url names the database and user the old app was linked
		// on, which the new app is found by from now on
		if account := service.LinkAccount(input.Datastore, serviceName, oldAppName); !account.IsDefault() {
			if err := recordLinkAccount(input.Logger, input.Datastore, serviceName, newAppName, account); err != nil {
				return err
			}
		}
	}

	return nil
}

// RemoveAppLinks drops an app from every service's links file, along with the
// config keys recorded for it and the network its link ran over
func RemoveAppLinks(ctx context.Context, input TriggerInput, appName string) error {
	services, err := ListServices(ctx, ListServicesInput{Datastore: input.Datastore})
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	for _, serviceName := range services {
		input.Logger.Info(fmt.Sprintf("Unlinking from %s", serviceName))
		if err := service.RemoveLinkedApp(ctx, linkedAppsInput(input.Datastore, serviceName), appName); err != nil {
			return err
		}

		if err := forgetLinkConfigKeys(input.Logger, input.Datastore, serviceName, appName); err != nil {
			return err
		}

		if err := service.RemoveLinkNetwork(ctx, service.LinkNetworkName(input.Datastore, serviceName, appName)); err != nil {
			return err
		}
	}

	return nil
}

// ConnectContainerToLinks attaches a container dokku just made for an app to
// the network of every link the app has, which is how an app reaches the
// services it is linked to. dokku fires post-container-create for the
// containers it builds, deploys and runs an app in, which are the phases a
// container link used to cover. A container made for anything else is left
// alone.
func ConnectContainerToLinks(ctx context.Context, input TriggerInput, containerType string, containerID string, appName string) error {
	if containerType != "app" || containerID == "" || appName == "" {
		return nil
	}

	services, err := LinkedServices(ctx, LinkedServicesInput{
		AppName:   appName,
		Datastore: input.Datastore,
	})
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	for _, serviceName := range services {
		// a network removed by hand is made again, so a deploy repairs it
		if err := service.EnsureLinkNetwork(ctx, service.EnsureLinkNetworkInput{
			AppName:     appName,
			Datastore:   input.Datastore,
			ServiceName: serviceName,
		}); err != nil {
			return err
		}

		if err := connectAppContainer(ctx, input.Logger, input.Datastore, serviceName, appName, containerID); err != nil {
			return err
		}
	}

	return nil
}

// StartLinkedServices starts every service an app is linked to that is not
// already running, so the app does not come up without its datastores.
//
// It runs before an app starts, builds or releases, and the first service that
// cannot be started stops there: the app would otherwise fail later on a
// service that is not there, saying nothing about why.
func StartLinkedServices(ctx context.Context, input TriggerInput, appName string) error {
	// a trigger that names no app has no services to answer for
	if appName == "" {
		return nil
	}

	services, err := LinkedServices(ctx, LinkedServicesInput{
		AppName:   appName,
		Datastore: input.Datastore,
	})
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	for _, serviceName := range services {
		if err := startService(ctx, input, serviceName, appName); err != nil {
			return err
		}
	}

	return nil
}

// StartAllLinkedServices starts every service that is linked to any app, ahead
// of dokku restoring its apps.
//
// Dokku restores apps in parallel, and each one starts its own services from
// pre-start, so two apps sharing a service would otherwise race to make it.
// Starting them here, one at a time, means the apps find them running. A
// service that cannot be started is only warned about: failing here would stop
// every app from being restored, when the pre-start of the apps using it is
// already where that app is stopped.
func StartAllLinkedServices(ctx context.Context, input TriggerInput) error {
	services, err := ServicesWithLinks(ctx, input.Datastore)
	if err != nil {
		return err
	}

	for _, serviceName := range services {
		if err := startService(ctx, input, serviceName, ""); err != nil {
			input.Logger.Warn(WarnInput{Warning: err.Error()})
		}
	}

	return nil
}

// ServicesWithLinks lists the services that are linked to at least one app
func ServicesWithLinks(ctx context.Context, s *service.Datastore) ([]string, error) {
	services, err := ListServices(ctx, ListServicesInput{Datastore: s})
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	linked := []string{}
	for _, serviceName := range services {
		if len(service.LinkedApps(ctx, linkedAppsInput(s, serviceName))) > 0 {
			linked = append(linked, serviceName)
		}
	}

	return linked, nil
}

// startService starts one service that is not already running and waits for it
// to answer. The app name is only used to say who the service is started for.
func startService(ctx context.Context, input TriggerInput, serviceName string, appName string) error {
	serviceType := input.Datastore.Properties().CommandPrefix
	status := strings.ToLower(service.Status(ctx, service.StatusInput{
		Datastore:   input.Datastore,
		ServiceName: serviceName,
	}))
	if status == "running" {
		return nil
	}

	if status == "restarting" {
		warning := fmt.Sprintf("%s service %s is restarting and may cause issues with linked apps", serviceType, serviceName)
		if appName != "" {
			warning = fmt.Sprintf("%s service %s is restarting and may cause issues with linked app %s", serviceType, serviceName, appName)
		}

		input.Logger.Warn(WarnInput{Warning: warning})
		return nil
	}

	input.Logger.Warn(WarnInput{
		Warning: fmt.Sprintf("%s service %s is not running, issuing service start", serviceType, serviceName),
	})

	failed := func(err error) error {
		if appName == "" {
			return fmt.Errorf("unable to start %s service %s: %w", serviceType, serviceName, err)
		}

		return fmt.Errorf("unable to start %s service %s linked to app %s: %w", serviceType, serviceName, appName, err)
	}

	// a start with no container left to start makes one, so it is the
	// service's own definition that has to make it rather than the
	// datastore's newest
	datastore, unresolved := input.Datastore.ForService(serviceName)
	if unresolved != nil {
		return failed(unresolved)
	}

	if err := service.Start(ctx, service.StartInput{
		Datastore:   datastore,
		ServiceName: serviceName,
	}); err != nil {
		return failed(err)
	}

	// the app is about to be handed a connection string, so the datastore
	// has to be answering rather than merely created: this trigger is the
	// one place where something else deploys against what it just started
	if err := WaitForService(ctx, WaitForServiceInput{
		Datastore:   datastore,
		ServiceName: serviceName,
		Logger:      input.Logger,
	}); err != nil {
		return failed(err)
	}

	return nil
}

// ServiceListForTrigger returns the prefixed service names other dokku plugins
// consume, or nothing when the request is for a different datastore
func ServiceListForTrigger(ctx context.Context, input TriggerInput, serviceType string) ([]string, error) {
	commandPrefix := input.Datastore.Properties().CommandPrefix
	if serviceType != "" && serviceType != commandPrefix {
		return []string{}, nil
	}

	services, err := ListServices(ctx, ListServicesInput{Datastore: input.Datastore})
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	names := make([]string, 0, len(services))
	for _, serviceName := range services {
		names = append(names, fmt.Sprintf("%s:%s", commandPrefix, serviceName))
	}

	return names, nil
}

// CronEntriesInput is the input for the CronEntriesForTrigger function
type CronEntriesInput struct {
	TriggerInput

	// EntryFormat is the entry format dokku passed to the trigger, which is
	// CronEntryFormatJSON when dokku reads json entries and empty otherwise
	EntryFormat string
}

// CronEntriesForTrigger returns the tasks the cron-entries trigger prints, one
// for each scheduled backup, in the order the services are listed and, within
// a service, the default schedule first. Alongside them are warnings: the services left out because what they
// were scheduled with cannot be run, as the dokku crontab is refused as a whole
// when a line in it is invalid, so one service's schedule must not stop every
// other task on the host; and the services whose MAILTO is ignored because dokku
// does not read json entries.
func CronEntriesForTrigger(ctx context.Context, input CronEntriesInput) ([]CronTask, []error, error) {
	services, err := ListServices(ctx, ListServicesInput{Datastore: input.Datastore})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list services: %w", err)
	}

	commandPrefix := input.Datastore.Properties().CommandPrefix
	tasks := []CronTask{}
	warnings := []error{}
	for _, serviceName := range services {
		schedules, readErr := ReadBackupSchedules(input.Datastore, serviceName)
		if readErr != nil {
			warnings = append(warnings, fmt.Errorf("unable to read the scheduled backups for %s: %w", serviceName, readErr))
		}

		for _, schedule := range schedules {
			// the recipient cron mails is resolved here and not written back,
			// so a service's backup-mailto does not become a setting of the schedule
			resolved := schedule.withInheritedMailto(input.Datastore, serviceName)
			if err := resolved.Validate(); err != nil {
				warnings = append(warnings, fmt.Errorf("skipping the scheduled backup for %s: %w", serviceName, err))
				continue
			}

			if resolved.Mailto != "" && input.EntryFormat != CronEntryFormatJSON {
				warnings = append(warnings, fmt.Errorf("%s for %s is ignored, dokku does not support a per-entry MAILTO", service.BackupMailtoProperty, serviceName))
			}

			tasks = append(tasks, CronEntry(commandPrefix, serviceName, resolved))
		}
	}

	return tasks, warnings, nil
}

// commandPrefixPattern is the line a per-datastore plugin's config file names
// the datastore it serves with
var commandPrefixPattern = regexp.MustCompile(`(?m)^export PLUGIN_COMMAND_PREFIX="([^"]+)"$`)

// OwnedTypes are the datastore types an enabled per-datastore plugin serves,
// which a plugin serving every datastore leaves to that plugin.
//
// A type is owned by a plugin named after it, which is how every one of them is
// installed by default, or by any plugin whose config file says it serves it,
// which is what one installed under another name looks like.
func OwnedTypes() map[string]bool {
	owned := map[string]bool{}
	enabled := hostenv.PluginEnabledPath()

	for _, name := range service.Types() {
		if _, err := os.Stat(filepath.Join(enabled, name)); err == nil {
			owned[name] = true
		}
	}

	entries, err := os.ReadDir(enabled)
	if err != nil {
		return owned
	}

	for _, entry := range entries {
		contents, err := os.ReadFile(filepath.Join(enabled, entry.Name(), "config"))
		if err != nil {
			continue
		}

		for _, match := range commandPrefixPattern.FindAllStringSubmatch(string(contents), -1) {
			owned[match[1]] = true
		}
	}

	return owned
}
