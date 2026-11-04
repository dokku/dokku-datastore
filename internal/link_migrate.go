package internal

import (
	"context"
	"fmt"
	"slices"

	"github.com/dokku/dokku-datastore/internal/service"
)

// MigrateLinksInput is the input for the MigrateLinks function
type MigrateLinksInput struct {
	// AppName is the one app to move, every linked app when empty
	AppName string

	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

	// Logger reports progress
	Logger Ui

	// ServiceName is the service whose links are moved
	ServiceName string
}

// MigrateLinks moves apps linked to a service off the container link earlier
// versions gave them and onto the network each link now runs over.
//
// Nothing is restarted and no config changes: the running containers join the
// network straight away, the service answers to the same host on it, and the
// container link is only removed once that worked. An app that cannot be moved
// keeps its container link, is reported, and the rest are still moved. An app
// already on its network is put back on it, which repairs a network that went
// missing or a container that is not on it.
func MigrateLinks(ctx context.Context, input MigrateLinksInput) error {
	linked := service.LinkedApps(ctx, service.LinkedAppsInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})

	apps := []string{}
	if input.AppName != "" {
		if !slices.Contains(linked, input.AppName) {
			return fmt.Errorf("Not linked to app %s", input.AppName) //nolint:staticcheck // matches unlink
		}
		apps = append(apps, input.AppName)
	} else {
		// an app deleted outside dokku has nothing to move, and is dropped
		// from the links file when the service is next unlinked from it
		for _, appName := range linked {
			if service.AppExists(appName) {
				apps = append(apps, appName)
			}
		}
	}

	commandPrefix := input.Datastore.Properties().CommandPrefix
	failed := []string{}
	keptLegacy := true
	for _, appName := range apps {
		legacy := service.HasLegacyLink(input.Datastore, input.ServiceName, appName)
		if legacy {
			input.Logger.Header1(fmt.Sprintf("Moving app %s onto the network of its link to %s service %s", appName, commandPrefix, input.ServiceName)) //nolint:errcheck
		} else {
			input.Logger.Info(fmt.Sprintf("App %s already uses a network link to %s service %s", appName, commandPrefix, input.ServiceName))
		}

		if err := connectLink(ctx, input.Logger, input.Datastore, input.ServiceName, appName); err != nil {
			input.Logger.Error(ErrorInput{Error: err})
			failed = append(failed, appName)
			keptLegacy = keptLegacy && legacy
		}
	}

	return migrateLinksFailure(failed, keptLegacy)
}

// migrateLinksFailure is the error ending a link-migrate that could not move
// every app, saying the apps that were still on their container link keep it
func migrateLinksFailure(failed []string, keptLegacy bool) error {
	if len(failed) == 0 {
		return nil
	}

	if len(failed) == 1 {
		kept := ""
		if keptLegacy {
			kept = ", it keeps its docker link"
		}
		return fmt.Errorf("Unable to move app %s onto a network%s; rerun the command once the errors above are fixed", failed[0], kept) //nolint:staticcheck // a sentence shown to the user
	}

	kept := ""
	if keptLegacy {
		kept = ", they keep their docker links"
	}
	return fmt.Errorf("Unable to move apps %s onto networks%s; rerun the command once the errors above are fixed", joinNames(failed), kept) //nolint:staticcheck // a sentence shown to the user
}
