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

	// Force is whether a running service exposed directly is stopped and
	// started without asking, which it has to be for its container to publish
	// the ports
	Force bool

	// Ask asks whether a running service exposed directly may be stopped and
	// started. Nil is the same as an answer of no
	Ask func(string) (string, error)
}

// ExposeService exposes a service
func ExposeService(ctx context.Context, input ExposeServiceInput) error {
	serviceFiles := service.Files(input.Datastore, input.ServiceName)
	portFile := serviceFiles.Port

	// refused ahead of anything else, since a port-source-range the service
	// was given would otherwise be quietly ignored
	mode := service.ServiceExposeMode(input.Datastore, input.ServiceName)
	if err := service.CheckExposeModeSourceRange(mode, service.ServicePortSourceRange(input.Datastore, input.ServiceName)); err != nil {
		return err
	}

	if len(input.Ports) == 0 {
		// picked where they will be published, and written without the address,
		// so a later port-bind-address moves them rather than leaving them behind
		address := service.ServicePortBindAddress(input.Datastore, input.ServiceName)
		ports, err := service.GenerateRandomPorts(address, input.Datastore.Properties().Protocols)
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

	// checked before the port file is written, since that file is what says a
	// service is exposed. A port the ambassador cannot publish would otherwise
	// leave a service reported as exposed that publishes nothing
	for _, port := range input.Ports {
		if err := service.ValidateHostPort(port); err != nil {
			return err
		}
	}

	// ahead of the port file, which is the only thing that says a service is
	// exposed. Reconciling the ports fetches this too, but by then the file is
	// written, so a host that cannot get the ambassador would be left with a
	// service reported as exposed that publishes nothing and that a second
	// expose refuses to touch. A service exposed directly runs no ambassador
	if mode != service.ExposeModeDirect {
		if err := service.EnsureTaggedImage(ctx, service.EnsureTaggedImageInput{
			Action:      "port publishing",
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
			TaggedImage: hostenv.AmbassadorImage,
		}); err != nil {
			return err
		}
	}

	// a running container cannot be told to publish ports it was not made
	// with, so one exposed directly is made again. Asked before the port file
	// is written, so an answer of no leaves the service as it was
	publish := service.ServicePublishState(ctx, input.Datastore, input.ServiceName, input.Ports)
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

	if recreate {
		return RecreateServiceContainer(ctx, RecreateServiceContainerInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
			Logger:      input.Logger,
		})
	}

	// a container that is not running is made again by the start when it
	// publishes something other than what it should now
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
