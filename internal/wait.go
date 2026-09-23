package internal

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
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

	// Timeout bounds the wait in seconds, zero leaving the probe's own default
	Timeout int
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

	args = append(args, hostenv.WaitImage)
	args = append(args, "-c", fmt.Sprintf("%s:%d", input.NetworkAlias, input.Port))

	if input.Timeout > 0 {
		args = append(args, "-t", strconv.Itoa(input.Timeout))
	}

	return args
}

// RunningContainerTimeout bounds the wait for a container to report running.
// It covers the gap between starting one and docker agreeing that it is up,
// which is a question about docker rather than about the datastore inside, so
// it is far shorter than the readiness probe's own timeout.
const RunningContainerTimeout = 30 * time.Second

// RunningContainerInterval is how often the container's state is asked for
// while waiting for it to come up.
const RunningContainerInterval = 250 * time.Millisecond

// waitForRunningContainer blocks until the service's container reports running.
//
// A container that never gets there is not reported here. The probe that
// follows says what an operator needs to know - that nothing answered, and what
// the datastore itself said on the way down - and it says it the same way
// whether the container is missing, stopped or merely deaf.
func waitForRunningContainer(ctx context.Context, input WaitForServiceInput) error {
	deadline := time.Now().Add(RunningContainerTimeout)
	for {
		status := service.Status(ctx, service.StatusInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
		})
		if status == "running" {
			return nil
		}

		if time.Now().After(deadline) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(RunningContainerInterval):
		}
	}
}

// WaitForServiceInput is the input for WaitForService.
type WaitForServiceInput struct {
	// Datastore is the service's datastore
	Datastore *service.Datastore

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

	// a definition that declares no ports at all has nothing to connect to, and
	// probing port zero would only ever time out. No definition shipped today is
	// one, so this is here to keep a future one from waiting for nothing.
	if properties.WaitPort == 0 {
		return nil
	}

	// the probe reaches the service with --link, and docker refuses to link to a
	// container that is not up yet. Starting a container that already exists
	// returns before it has necessarily got there, so a service that is on its
	// way up would otherwise be reported as one that never answered
	if err := waitForRunningContainer(ctx, input); err != nil {
		return err
	}

	arguments := WaitArgs(WaitArgsInput{
		ContainerName:  service.ContainerName(input.Datastore, input.ServiceName),
		NetworkAlias:   service.DNSHostname(input.Datastore, input.ServiceName),
		InitialNetwork: service.InitialNetwork(input.Datastore, input.ServiceName),
		Port:           properties.WaitPort,
		Timeout:        input.Datastore.Definition.Dokku.WaitTimeout,
	})

	input.Logger.Header1(fmt.Sprintf("Waiting for %s container to be ready", input.ServiceName)) //nolint:errcheck

	_, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    arguments,
	})
	if err == nil {
		return nil
	}

	// the probe only reports that nothing answered, and what went wrong is in
	// the datastore's own output, so it is shown rather than left to be asked for
	containerID := service.LiveContainerID(ctx, service.LiveContainerIDInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})

	input.Logger.Header1(fmt.Sprintf("Start of %s container output", input.ServiceName)) //nolint:errcheck
	common.LogVerboseQuietContainerLogs(containerID)
	input.Logger.Header1(fmt.Sprintf("End of %s container output", input.ServiceName)) //nolint:errcheck

	return fmt.Errorf("service %s did not become ready: %w", input.ServiceName, err)
}
