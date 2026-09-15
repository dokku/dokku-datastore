package internal

import (
	"context"
	"fmt"

	"github.com/dokku/dokku-datastore/internal/datastores"
	"github.com/dokku/dokku/plugins/common"
)

// WaitArgsInput is the input for WaitArgs.
type WaitArgsInput struct {
	// ContainerName is the service container to probe
	ContainerName string

	// NetworkAlias is the name the probe reaches the service by
	NetworkAlias string

	// InitialNetwork is the network the service was created on, which the probe
	// has to join to see it
	InitialNetwork string

	// Port is the port the service answers on
	Port int
}

// WaitArgs builds the argv for the readiness probe. It is pure, so what a
// service is waited on with can be pinned by a test without a docker daemon.
func WaitArgs(input WaitArgsInput) []string {
	args := []string{
		"container",
		"run",
		"--rm",
		"--link=" + input.ContainerName + ":" + input.NetworkAlias,
	}

	if input.InitialNetwork != "" {
		args = append(args, "--network="+input.InitialNetwork)
	}

	args = append(args, datastores.PluginWaitImage)
	return append(args, "-c", fmt.Sprintf("%s:%d", input.NetworkAlias, input.Port))
}

// WaitForServiceInput is the input for WaitForService.
type WaitForServiceInput struct {
	// Datastore is the service's datastore
	Datastore datastores.Datastore

	// ServiceName is the service to wait on
	ServiceName string

	// Logger reports what is being waited on, and what the container said when
	// the wait did not succeed
	Logger Ui
}

// WaitForService blocks until a service answers on its port.
//
// A datastore is not usable the moment its container is running: the server
// inside still has to finish starting, and a link made before then hands an app
// a connection string that refuses connections. The probe is a sidecar rather
// than a healthcheck because not every stock image ships a tool whose exit
// status can be trusted.
func WaitForService(ctx context.Context, input WaitForServiceInput) error {
	properties := input.Datastore.Properties()

	arguments := WaitArgs(WaitArgsInput{
		ContainerName:  datastores.ContainerName(input.Datastore, input.ServiceName),
		NetworkAlias:   datastores.DNSHostname(input.Datastore, input.ServiceName),
		InitialNetwork: datastores.InitialNetwork(input.Datastore, input.ServiceName),
		Port:           properties.WaitPort,
	})

	input.Logger.Header1(fmt.Sprintf("Waiting for %s container to be ready", input.ServiceName)) //nolint:errcheck

	_, err := datastores.CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    arguments,
	})
	if err == nil {
		return nil
	}

	// the probe only reports that nothing answered, and what went wrong is in
	// the datastore's own output, so it is shown rather than left to be asked for
	containerID := datastores.LiveContainerID(ctx, datastores.LiveContainerIDInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})

	input.Logger.Header1(fmt.Sprintf("Start of %s container output", input.ServiceName)) //nolint:errcheck
	common.LogVerboseQuietContainerLogs(containerID)
	input.Logger.Header1(fmt.Sprintf("End of %s container output", input.ServiceName)) //nolint:errcheck

	return fmt.Errorf("service %s did not become ready: %w", input.ServiceName, err)
}
