package internal

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// ErrLinkedService is returned when a service still has linked apps. The
// capitalization is deliberate and matches the bash datastore plugins.
var ErrLinkedService = errors.New("Cannot delete linked service")

// DestroyServiceInput is the input for the DestroyService function
type DestroyServiceInput struct {
	// Datastore is the service to destroy
	Datastore *service.Datastore

	// ServiceName is the name of the service to destroy
	ServiceName string
}

// DestroyService destroys a service
func DestroyService(ctx context.Context, input DestroyServiceInput) error {
	_, err := execx.PlugnTrigger(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"pre-delete", input.Datastore.ServiceType(), input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action pre-delete trigger: %w", err)
	}

	err = service.RemoveBackupSchedule(ctx, service.RemoveBackupScheduleInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to remove backup schedule: %w", err)
	}

	err = service.RemoveServiceContainer(ctx, service.RemoveServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	serviceFolders := service.Folders(input.Datastore, input.ServiceName)
	_, err = execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "run", "--rm", "-v", fmt.Sprintf("%s/data:/data", serviceFolders.HostRoot), "-v", fmt.Sprintf("%s/config:/config", serviceFolders.HostRoot), hostenv.BusyboxImage, "chmod", "777", "-R", "/config", "/data"},
	})
	if err != nil {
		return fmt.Errorf("failed to remove data: %w", err)
	}

	if err := os.RemoveAll(serviceFolders.Root); err != nil {
		return fmt.Errorf("failed to remove service root: %w", err)
	}

	err = common.PropertyDestroy(input.Datastore.Properties().CommandPrefix, input.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to destroy properties: %w", err)
	}

	_, err = execx.PlugnTrigger(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"post-delete", input.Datastore.ServiceType(), input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action pre-delete trigger: %w", err)
	}

	return nil
}
