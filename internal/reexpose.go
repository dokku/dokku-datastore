package internal

import (
	"context"
	"fmt"

	"github.com/dokku/dokku-datastore/internal/service"
)

// NotExposedError returns the error reported when reexposing a service that is
// not exposed
func NotExposedError(serviceName string) error {
	return fmt.Errorf("Service %s is not exposed", serviceName) //nolint:staticcheck // matches AlreadyExposedError
}

// ReexposeServiceInput is the input for the ReexposeService function
type ReexposeServiceInput struct {
	// Datastore is the service to reexpose
	Datastore *service.Datastore

	// ServiceName is the name of the service to reexpose
	ServiceName string
}

// ReexposeService replaces an exposed service's ambassador with one made from
// the ports and expose settings the service has now, leaving the service
// container alone.
//
// It is how a port-bind-address or expose-source-range reaches a running
// service without restarting it. The ambassador is replaced even when nothing
// has changed, so it is also how one that has stopped publishing is made
// again.
func ReexposeService(ctx context.Context, input ReexposeServiceInput) error {
	if !IsExposed(input.Datastore, input.ServiceName) {
		return NotExposedError(input.ServiceName)
	}

	if err := service.ServicePortReconcileStatus(ctx, service.ServicePortReconcileStatusInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		Force:       true,
	}); err != nil {
		return fmt.Errorf("failed to reexpose service: %w", err)
	}

	return nil
}
