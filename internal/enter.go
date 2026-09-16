package internal

import (
	"context"

	"github.com/dokku/dokku-datastore/internal/service"
)

// EnterServiceInput is the input for the EnterService function
type EnterServiceInput struct {
	// Datastore is the service to enter
	Datastore *service.Datastore

	// ServiceName is the name of the service to enter
	ServiceName string
}

// EnterService enters a service
func EnterService(ctx context.Context, input EnterServiceInput) error {
	return service.EnterServiceContainer(ctx, service.EnterServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
}
