package internal

import (
	"context"

	"github.com/dokku/dokku-datastore/internal/service"
)

// EnterServiceInput is the input for the EnterService function
type EnterServiceInput struct {
	// Service is the service to enter
	Service service.Service
	// ServiceName is the name of the service to enter
	ServiceName string
}

// EnterService enters a service
func EnterService(ctx context.Context, input EnterServiceInput) error {
	return service.EnterServiceContainer(ctx, service.EnterServiceContainerInput{
		Service:     input.Service,
		ServiceName: input.ServiceName,
	})
}
