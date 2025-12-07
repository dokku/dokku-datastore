package internal

import (
	"context"
	"fmt"
	"slices"

	"github.com/dokku/dokku-datastore/internal/service"
)

// LinkedAppsInput is the input for the LinkedApps function
type LinkedAppsInput struct {
	// AppName is the name of the app to list the linked services for
	AppName string
	// DatastoreType is the type of datastore to list the linked services for
	DatastoreType string
}

// LinkedApps lists all services that are linked to a given app
func LinkedApps(ctx context.Context, input LinkedAppsInput) ([]string, error) {
	if input.AppName == "" {
		return []string{}, fmt.Errorf("app name is required")
	}

	if input.DatastoreType == "" {
		return []string{}, fmt.Errorf("datastore type is required")
	}

	services, err := ListServices(ctx, ListServicesInput{
		DatastoreType: input.DatastoreType,
		Trace:         true,
	})
	if err != nil {
		return []string{}, err
	}

	serviceWrapper, ok := service.Services[input.DatastoreType]
	if !ok {
		return []string{}, fmt.Errorf("datastore type %s is not supported", input.DatastoreType)
	}

	linkedServices := []string{}
	for _, serviceName := range services {
		linkedApps := service.LinkedApps(serviceWrapper, serviceName)
		if slices.Contains(linkedApps, input.AppName) {
			linkedServices = append(linkedServices, serviceName)
		}
	}

	return linkedServices, nil
}
