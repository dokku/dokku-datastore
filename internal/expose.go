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

	// Logger reports what is being waited on once the container exists
	Logger Ui
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

	// ahead of the port file, which is the only thing that says a service is
	// exposed. Reconciling the ports fetches this too, but by then the file is
	// written, so a host that cannot get the ambassador would be left with a
	// service reported as exposed that publishes nothing and that a second
	// expose refuses to touch
	if err := service.EnsureTaggedImage(ctx, service.EnsureTaggedImageInput{
		Action:      "port publishing",
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		TaggedImage: hostenv.AmbassadorImage,
	}); err != nil {
		return err
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

	// an app told the service is exposed has something answering on the port.
	// The start above has already published it, since an ambassador only needs
	// the container running; reconciling again afterwards is a no-op unless the
	// service went down while it was being waited on
	if err := WaitForService(ctx, WaitForServiceInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		Logger:      input.Logger,
	}); err != nil {
		return err
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
