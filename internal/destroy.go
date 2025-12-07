package internal

import (
	"context"
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// DestroyServiceInput is the input for the DestroyService function
type DestroyServiceInput struct {
	// Service is the service to destroy
	Service service.Service
	// ServiceName is the name of the service to destroy
	ServiceName string
}

// DestroyService destroys a service
func DestroyService(ctx context.Context, input DestroyServiceInput) error {
	_, err := service.CallPlugnTriggerWithContext(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"pre-delete", input.Service.ServiceType(), input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action pre-delete trigger: %w", err)
	}

	err = service.RemoveBackupSchedule(ctx, service.RemoveBackupScheduleInput{
		Service:     input.Service,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to remove backup schedule: %w", err)
	}

	err = service.RemoveServiceContainer(ctx, service.RemoveServiceContainerInput{
		Service:     input.Service,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	serviceFolders := service.Folders(input.Service, input.ServiceName)
	_, err = service.CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "run", "--rm", "-v", fmt.Sprintf("%s/data:/data", serviceFolders.HostRoot), "-v", fmt.Sprintf("%s/config:/config", serviceFolders.HostRoot), service.PluginBusyboxImage, "chmod", "777", "-R", "/config", "/data"},
	})
	if err != nil {
		return fmt.Errorf("failed to remove data: %w", err)
	}

	if err := os.RemoveAll(serviceFolders.Root); err != nil {
		return fmt.Errorf("failed to remove service root: %w", err)
	}

	err = common.PropertyDestroy(input.Service.Properties().CommandPrefix, input.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to destroy properties: %w", err)
	}

	_, err = service.CallPlugnTriggerWithContext(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"post-delete", input.Service.ServiceType(), input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action pre-delete trigger: %w", err)
	}

	return nil
}
