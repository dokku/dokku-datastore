package internal

import (
	"context"
	"fmt"
	"strings"

	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// IsExposed checks if a service is exposed
func IsExposed(s *service.Datastore, serviceName string) bool {
	return len(service.ExposedHostPorts(s, serviceName)) > 0
}

// ConfiguredPorts returns the host ports a service is exposed on. Unlike
// service.ExposedPorts this is the raw port list rather than a container to
// host mapping.
func ConfiguredPorts(s *service.Datastore, serviceName string) string {
	return strings.Join(service.ExposedHostPorts(s, serviceName), " ")
}

// AlreadyExposedError returns the error reported when exposing a service that is
// already exposed
func AlreadyExposedError(s *service.Datastore, serviceName string) error {
	return fmt.Errorf("Service %s already exposed on port(s) %s", serviceName, ConfiguredPorts(s, serviceName)) //nolint:staticcheck // matches the bash datastore plugins
}

// ExposeServiceInput is the input for the ExposeService function
type ExposeServiceInput struct {
	// Datastore is the service to expose
	Datastore *service.Datastore

	// Ports is the ports to expose
	Ports []string

	// ServiceName is the name of the service to expose
	ServiceName string
}

// ExposeService exposes a service
func ExposeService(ctx context.Context, input ExposeServiceInput) error {
	serviceFiles := service.Files(input.Datastore, input.ServiceName)
	portFile := serviceFiles.Port

	if len(input.Ports) == 0 {
		ports, err := service.GenerateRandomPorts(len(input.Datastore.Properties().Ports))
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
		GroupName: hostenv.SystemGroup(),
		Mode:      0644,
		Username:  hostenv.SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write ports to %s: %w", portFile, err)
	}

	err = service.Start(ctx, service.StartInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	err = service.ServicePortReconcileStatus(ctx, service.ServicePortReconcileStatusInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile port status: %w", err)
	}

	return nil
}

// RemoveAmbassadorContainer removes the ambassador container for a service
func RemoveAmbassadorContainer(ctx context.Context, s *service.Datastore, serviceName string) error {
	ambassadorName := service.AmbassadorContainerName(s, serviceName)
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
