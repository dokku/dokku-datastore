package internal

import (
	"context"
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal/datastores"
)

// UnexposeServiceInput is the input for the UnexposeService function
type UnexposeServiceInput struct {
	// Datastore is the service to unexpose
	Datastore datastores.Datastore

	// ServiceName is the name of the service to unexpose
	ServiceName string
}

// UnexposeService unexposes a service
func UnexposeService(ctx context.Context, input UnexposeServiceInput) error {
	ambassadorContainerName := datastores.AmbassadorContainerName(input.Datastore, input.ServiceName)
	if datastores.ContainerExists(ctx, ambassadorContainerName) {
		err := RemoveAmbassadorContainer(ctx, input.Datastore, input.ServiceName)
		if err != nil {
			return fmt.Errorf("failed to remove ambassador container: %w", err)
		}
	}

	serviceFiles := datastores.Files(input.Datastore, input.ServiceName)
	if err := os.RemoveAll(serviceFiles.Port); err != nil {
		return fmt.Errorf("failed to remove port file: %w", err)
	}

	return nil
}
