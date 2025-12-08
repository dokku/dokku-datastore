package internal

import (
	"context"
	"fmt"
	"slices"

	"github.com/dokku/dokku-datastore/internal/datastores"
)

// LinkedServicesInput is the input for the LinkedServices function
type LinkedServicesInput struct {
	// AppName is the name of the app to list the linked services for
	AppName string

	// Datastore is the service to list the linked services for
	Datastore datastores.Datastore
}

// LinkedServices lists all services that are linked to a given app
func LinkedServices(ctx context.Context, input LinkedServicesInput) ([]string, error) {
	if input.AppName == "" {
		return []string{}, fmt.Errorf("app name is required")
	}

	services, err := ListServices(ctx, ListServicesInput{
		Datastore: input.Datastore,
		Trace:     true,
	})
	if err != nil {
		return []string{}, err
	}

	linkedServices := []string{}
	for _, serviceName := range services {
		linkedApps := datastores.LinkedApps(input.Datastore, serviceName)
		if slices.Contains(linkedApps, input.AppName) {
			linkedServices = append(linkedServices, serviceName)
		}
	}

	return linkedServices, nil
}
