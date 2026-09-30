package internal

import (
	"context"
	"fmt"
	"strings"

	"github.com/dokku/dokku-datastore/internal/service"
)

// ConfirmRecreateInput is the input for the ConfirmRecreate function
type ConfirmRecreateInput struct {
	// ServiceName is the service that would be stopped and started
	ServiceName string

	// Logger says why the service has to be stopped and started
	Logger Ui

	// Force is whether the service is stopped and started without asking
	Force bool

	// Ask asks a question and returns the answer. Nil is the same as an
	// answer of no, since there is nobody to ask
	Ask func(string) (string, error)
}

// ConfirmRecreate asks whether a running service may be stopped and started so
// that the ports its container publishes can change, since docker has no way
// to change them on a container that exists.
//
// It is asked before anything is changed, so an answer of no, or no answer at
// all, leaves the service exactly as it was.
func ConfirmRecreate(input ConfirmRecreateInput) error {
	if input.Force {
		return nil
	}

	input.Logger.Warn(WarnInput{
		Warning: fmt.Sprintf("Service %s has to be stopped and started to change the ports its container publishes", input.ServiceName),
	})

	declined := fmt.Errorf("aborted, nothing was changed; rerun with --force to stop and start %s without being asked", input.ServiceName)
	if input.Ask == nil {
		return declined
	}

	answer, err := input.Ask(fmt.Sprintf("Stop and start %s now? [y/N]", input.ServiceName))
	if err != nil {
		return declined
	}

	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	}

	return declined
}

// RecreateServiceContainerInput is the input for the RecreateServiceContainer
// function
type RecreateServiceContainerInput struct {
	// Datastore is the service's datastore
	Datastore *service.Datastore

	// ServiceName is the service to stop and start
	ServiceName string

	// Logger reports what is being waited on once the container exists
	Logger Ui
}

// RecreateServiceContainer stops and starts a service, the way its stop and
// start commands do, so that its container is made again with the ports it
// should publish.
//
// Removing the container removes its ambassador with it, and the start makes
// one again only when the service is to be published through one.
func RecreateServiceContainer(ctx context.Context, input RecreateServiceContainerInput) error {
	input.Logger.Info(fmt.Sprintf("Stopping and starting %s to change the ports it publishes", input.ServiceName))

	if err := service.RemoveServiceContainer(ctx, service.RemoveServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	}); err != nil {
		return err
	}

	if err := service.Start(ctx, service.StartInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	}); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	return WaitForService(ctx, WaitForServiceInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		Logger:      input.Logger,
	})
}
