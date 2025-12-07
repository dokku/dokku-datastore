package internal

import (
	"context"
	"fmt"

	"github.com/dokku/dokku-datastore/internal/service"
)

// EnterServiceInput is the input for the EnterService function
type EnterServiceInput struct {
	// DatastoreType is the type of datastore to destroy
	DatastoreType string
	// ServiceName is the name of the service to enter
	ServiceName string
}

// EnterService enters a service
func EnterService(ctx context.Context, input EnterServiceInput) error {
	if input.DatastoreType == "" {
		return fmt.Errorf("datastore type is required")
	}

	serviceWrapper, ok := service.Services[input.DatastoreType]
	if !ok {
		return fmt.Errorf("datastore type %s is not supported", input.DatastoreType)
	}

	return service.EnterServiceContainer(ctx, service.EnterServiceContainerInput{
		Service:     serviceWrapper,
		ServiceName: input.ServiceName,
	})
}
