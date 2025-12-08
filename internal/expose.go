package internal

import (
	"context"
	"fmt"
	"strings"

	"github.com/dokku/dokku-datastore/internal/datastores"
	"github.com/dokku/dokku/plugins/common"
)

func AmbassadorContainerExists(s datastores.Datastore, serviceName string) bool {
	ambassadorName := datastores.AmbassadorContainerName(s, serviceName)
	return common.ContainerExists(ambassadorName)
}

func IsExposed(s datastores.Datastore, serviceName string) bool {
	serviceFiles := datastores.Files(s, serviceName)
	portFile := serviceFiles.Port
	return common.FileExists(portFile) && common.ReadFirstLine(portFile) != ""
}

// ExposeServiceInput is the input for the ExposeService function
type ExposeServiceInput struct {
	// Datastore is the service to expose
	Datastore datastores.Datastore

	// Ports is the ports to expose
	Ports []string

	// ServiceName is the name of the service to expose
	ServiceName string
}

// ExposeService exposes a service
func ExposeService(ctx context.Context, input ExposeServiceInput) error {
	serviceFiles := datastores.Files(input.Datastore, input.ServiceName)
	portFile := serviceFiles.Port

	if len(input.Ports) == 0 {
		ports, err := datastores.GenerateRandomPorts(len(input.Datastore.Properties().Ports))
		if err != nil {
			return fmt.Errorf("failed to generate random ports: %w", err)
		}

		for _, port := range ports {
			input.Ports = append(input.Ports, fmt.Sprintf("%d", port))
		}
	}

	if len(input.Ports) != len(input.Datastore.Properties().Ports) {
		var ports []string
		for _, port := range input.Datastore.Properties().Ports {
			ports = append(ports, fmt.Sprintf("%d", port))
		}
		return fmt.Errorf("%d ports to be exposed need to be provided in the following order: %s", len(input.Ports), strings.Join(ports, ","))
	}

	err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   strings.Join(input.Ports, " "),
		Filename:  portFile,
		GroupName: datastores.SystemGroup(),
		Mode:      0644,
		Username:  datastores.SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write ports to %s: %w", portFile, err)
	}

	err = datastores.Start(ctx, datastores.StartInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	err = datastores.ServicePortReconcileStatus(ctx, datastores.ServicePortReconcileStatusInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile port status: %w", err)
	}

	return nil
}

// RemoveAmbassadorContainer removes the ambassador container for a service
func RemoveAmbassadorContainer(ctx context.Context, s datastores.Datastore, serviceName string) error {
	ambassadorName := datastores.AmbassadorContainerName(s, serviceName)
	_, err := common.CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "stop", ambassadorName},
	})
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w", ambassadorName, err)
	}
	_, err = common.CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "rm", ambassadorName},
	})
	if err != nil {
		return fmt.Errorf("failed to remove container %s: %w", ambassadorName, err)
	}

	return nil
}
