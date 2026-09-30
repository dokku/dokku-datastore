package internal

import (
	"context"
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal/service"
)

// UnexposeServiceInput is the input for the UnexposeService function
type UnexposeServiceInput struct {
	// Datastore is the service to unexpose
	Datastore *service.Datastore

	// ServiceName is the name of the service to unexpose
	ServiceName string

	// Logger reports what is being waited on when the service is stopped and
	// started
	Logger Ui

	// Force is whether a running service exposed directly is stopped and
	// started without asking, which it has to be for its container to stop
	// publishing the ports
	Force bool

	// Ask asks whether a running service exposed directly may be stopped and
	// started. Nil is the same as an answer of no
	Ask func(string) (string, error)
}

// UnexposeService unexposes a service
func UnexposeService(ctx context.Context, input UnexposeServiceInput) error {
	// a running container goes on publishing what it was made with until it is
	// made again. Asked before anything is removed, so an answer of no leaves
	// the service exposed as it was
	publish := service.ServicePublishState(ctx, input.Datastore, input.ServiceName, nil)
	recreate := publish.Running && publish.Differs
	if recreate {
		if err := ConfirmRecreate(ConfirmRecreateInput{
			ServiceName: input.ServiceName,
			Logger:      input.Logger,
			Force:       input.Force,
			Ask:         input.Ask,
		}); err != nil {
			return err
		}
	}

	ambassadorContainerName := service.AmbassadorContainerName(input.Datastore, input.ServiceName)
	if service.ContainerExists(ctx, ambassadorContainerName) {
		err := service.RemoveAmbassadorContainer(ctx, input.Datastore, input.ServiceName)
		if err != nil {
			return fmt.Errorf("failed to remove ambassador container: %w", err)
		}
	}

	serviceFiles := service.Files(input.Datastore, input.ServiceName)
	if err := os.RemoveAll(serviceFiles.Port); err != nil {
		return fmt.Errorf("failed to remove port file: %w", err)
	}

	// a container that is not running is made again by its next start
	if recreate {
		return RecreateServiceContainer(ctx, RecreateServiceContainerInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
			Logger:      input.Logger,
		})
	}

	return nil
}
