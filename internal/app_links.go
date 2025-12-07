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
	// Service is the service to list the linked services for
	Service service.Service
}

// LinkedApps lists all services that are linked to a given app
func LinkedApps(ctx context.Context, input LinkedAppsInput) ([]string, error) {
	if input.AppName == "" {
		return []string{}, fmt.Errorf("app name is required")
	}

	services, err := ListServices(ctx, ListServicesInput{
		Service: input.Service,
		Trace:   true,
	})
	if err != nil {
		return []string{}, err
	}

	linkedServices := []string{}
	for _, serviceName := range services {
		linkedApps := service.LinkedApps(input.Service, serviceName)
		if slices.Contains(linkedApps, input.AppName) {
			linkedServices = append(linkedServices, serviceName)
		}
	}

	return linkedServices, nil
}
