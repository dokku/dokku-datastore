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
	// DatastoreType is the type of datastore to destroy
	DatastoreType string
	// ServiceName is the name of the service to destroy
	ServiceName string
}

// DestroyService destroys a service
func DestroyService(input DestroyServiceInput) error {
	if input.DatastoreType == "" {
		return fmt.Errorf("datastore type is required")
	}

	serviceWrapper, ok := service.Services[input.DatastoreType]
	if !ok {
		return fmt.Errorf("datastore type %s is not supported", input.DatastoreType)
	}

	_, err := service.CallPlugnTrigger(common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"pre-delete", input.DatastoreType, input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action pre-delete trigger: %w", err)
	}

	err = service.RemoveBackupSchedule(serviceWrapper, input.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to remove backup schedule: %w", err)
	}

	err = service.RemoveServiceContainer(serviceWrapper, input.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	serviceFolders := service.Folders(serviceWrapper, input.ServiceName)
	_, err = service.CallExecCommandWithContext(context.Background(), common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "run", "--rm", "-v", fmt.Sprintf("%s/data:/data", serviceFolders.HostRoot), "-v", fmt.Sprintf("%s/config:/config", serviceFolders.HostRoot), service.PluginBusyboxImage, "chmod", "777", "-R", "/config", "/data"},
	})
	if err != nil {
		return fmt.Errorf("failed to remove data: %w", err)
	}

	if err := os.RemoveAll(serviceFolders.Root); err != nil {
		return fmt.Errorf("failed to remove service root: %w", err)
	}

	err = common.PropertyDestroy(serviceWrapper.Properties().CommandPrefix, input.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to destroy properties: %w", err)
	}

	_, err = service.CallPlugnTrigger(common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"post-delete", input.DatastoreType, input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action pre-delete trigger: %w", err)
	}

	return nil
}
