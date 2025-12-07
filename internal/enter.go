package internal

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/dokku-datastore/internal/service"
)

// EnterServiceInput is the input for the EnterService function
type EnterServiceInput struct {
	// DatastoreType is the type of datastore to destroy
	DatastoreType string
	// ServiceName is the name of the service to enter
	ServiceName string
}

// EnterService enters a service
func EnterService(input EnterServiceInput) error {
	if input.DatastoreType == "" {
		return fmt.Errorf("datastore type is required")
	}

	serviceWrapper, ok := service.Services[input.DatastoreType]
	if !ok {
		return fmt.Errorf("datastore type %s is not supported", input.DatastoreType)
	}

	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGTERM)
	go func() {
		<-signals
		cancel()
	}()

	return service.EnterServiceContainer(ctx, service.EnterServiceContainerInput{
		Service:     serviceWrapper,
		ServiceName: input.ServiceName,
	})
}
