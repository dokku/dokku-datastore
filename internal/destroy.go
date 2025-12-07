package internal

import (
	"context"
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal/datastores"
	"github.com/dokku/dokku/plugins/common"
)

// DestroyServiceInput is the input for the DestroyService function
type DestroyServiceInput struct {
	// Datastore is the service to destroy
	Datastore datastores.Datastore

	// ServiceName is the name of the service to destroy
	ServiceName string
}

// DestroyService destroys a service
func DestroyService(ctx context.Context, input DestroyServiceInput) error {
	_, err := datastores.CallPlugnTriggerWithContext(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"pre-delete", input.Datastore.ServiceType(), input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action pre-delete trigger: %w", err)
	}

	err = datastores.RemoveBackupSchedule(ctx, datastores.RemoveBackupScheduleInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to remove backup schedule: %w", err)
	}

	err = datastores.RemoveServiceContainer(ctx, datastores.RemoveServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	serviceFolders := datastores.Folders(input.Datastore, input.ServiceName)
	_, err = datastores.CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "run", "--rm", "-v", fmt.Sprintf("%s/data:/data", serviceFolders.HostRoot), "-v", fmt.Sprintf("%s/config:/config", serviceFolders.HostRoot), datastores.PluginBusyboxImage, "chmod", "777", "-R", "/config", "/data"},
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

	_, err = datastores.CallPlugnTriggerWithContext(ctx, common.PlugnTriggerInput{
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
