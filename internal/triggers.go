package internal

import (
	"context"
	"fmt"
	"slices"
	"strings"

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
// linked to. Both the clone and rename triggers want this; a rename leaves the
// old entry in place, which is what the bash datastore plugins do.
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
	}

	return nil
}

// RemoveAppLinks drops an app from every service's links file
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
	}

	return nil
}

// StartLinkedServices starts every service an app is linked to that is not
// already running, so the app does not come up without its datastores
func StartLinkedServices(ctx context.Context, input TriggerInput, appName string) error {
	services, err := ListServices(ctx, ListServicesInput{Datastore: input.Datastore})
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	serviceType := input.Datastore.Properties().CommandPrefix
	for _, serviceName := range services {
		if !slices.Contains(service.LinkedApps(ctx, linkedAppsInput(input.Datastore, serviceName)), appName) {
			continue
		}

		status := strings.ToLower(service.Status(ctx, service.StatusInput{
			Datastore:   input.Datastore,
			ServiceName: serviceName,
		}))
		if status == "running" {
			continue
		}

		if status == "restarting" {
			input.Logger.Warn(WarnInput{
				Warning: fmt.Sprintf("%s service %s is restarting and may cause issues with linked app %s", serviceType, serviceName, appName),
			})
			continue
		}

		input.Logger.Warn(WarnInput{
			Warning: fmt.Sprintf("%s service %s is not running, issuing service start", serviceType, serviceName),
		})
		if err := service.Start(ctx, service.StartInput{
			Datastore:   input.Datastore,
			ServiceName: serviceName,
		}); err != nil {
			return err
		}
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
