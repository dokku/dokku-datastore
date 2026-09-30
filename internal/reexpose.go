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

	// Logger reports what is being waited on when the service is stopped and
	// started
	Logger Ui

	// Force is whether a running service whose container publishes the wrong
	// ports is stopped and started without asking
	Force bool

	// Ask asks whether a running service whose container publishes the wrong
	// ports may be stopped and started. Nil is the same as an answer of no
	Ask func(string) (string, error)
}

// ReexposeService brings an exposed service's published ports in line with
// the ports and expose settings the service has now.
//
// A service published through an ambassador has it replaced when it no longer
// matches, leaving the service container alone, which is how a
// port-bind-address or port-source-range reaches a running service without
// restarting it. One that still matches is left alone, while one that has
// stopped publishing is made again.
//
// A service whose container has to publish something else - because it is
// exposed directly and its port-bind-address changed, or because it was moved
// between expose modes - has its container made again. That stops and starts
// a running service, so it is asked about first, and an answer of no leaves
// both the container and any ambassador as they were.
func ReexposeService(ctx context.Context, input ReexposeServiceInput) error {
	if !IsExposed(input.Datastore, input.ServiceName) {
		return NotExposedError(input.ServiceName)
	}

	mode := service.ServiceExposeMode(input.Datastore, input.ServiceName)
	if err := service.CheckExposeModeSourceRange(mode, service.ServicePortSourceRange(input.Datastore, input.ServiceName)); err != nil {
		return err
	}

	hostPorts := service.ExposedHostPorts(input.Datastore, input.ServiceName)
	publish := service.ServicePublishState(ctx, input.Datastore, input.ServiceName, hostPorts)
	if publish.Running && publish.Differs {
		if err := ConfirmRecreate(ConfirmRecreateInput{
			ServiceName: input.ServiceName,
			Logger:      input.Logger,
			Force:       input.Force,
			Ask:         input.Ask,
		}); err != nil {
			return err
		}

		return RecreateServiceContainer(ctx, RecreateServiceContainerInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
			Logger:      input.Logger,
		})
	}

	if err := service.ServicePortReconcileStatus(ctx, service.ServicePortReconcileStatusInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	}); err != nil {
		return fmt.Errorf("failed to reexpose service: %w", err)
	}

	return nil
}
