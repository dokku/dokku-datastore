package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
)

// accountCommand is what the commands that make and remove a service's
// databases and users share
type accountCommand interface {
	command.NamedCommand
	ParsedArguments(args []string) (map[string]command.Argument, error)
}

// cancelOnSignal is the context a command runs with, cancelled when the
// command is interrupted
func cancelOnSignal() context.Context {
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

	return ctx
}

// resolveAccountService resolves the service one of the databases or users
// commands acts on. A datastore that cannot make what the command makes exits
// the way dokku expects of a plugin that does not handle a command, and so does
// a service whose definition cannot, since a datastore split by major version
// is asked of the definition the service runs.
func resolveAccountService(ctx context.Context, c accountCommand, logger internal.Ui, arguments map[string]command.Argument, subcommand string) (*service.Datastore, string, int, bool) {
	if datastore, ok := service.Datastores[arguments["datastore-type"].StringValue()]; ok {
		if code, unimplemented := requireImplemented(datastore, subcommand); unimplemented {
			return nil, "", code, false
		}
	}

	datastore, serviceName, ok := resolveExistingService(ctx, c, logger, arguments)
	if !ok {
		return nil, "", 1, false
	}

	if !datastore.Definition.Implements(subcommand) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s runs the %s definition, which does not support %s", serviceName, datastore.DefinitionName(), subcommand),
		})
		return nil, "", 1, false
	}

	return datastore, serviceName, 0, true
}

// accountArguments are the arguments every databases and users command takes
// ahead of its own
func accountArguments(subject string) []command.Argument {
	return []command.Argument{
		{
			Name:        "datastore-type",
			Description: "the type of datastore the service is",
			Optional:    false,
			Type:        command.ArgumentString,
		},
		{
			Name:        "service-name",
			Description: fmt.Sprintf("the name of the service the %s is in", subject),
			Optional:    false,
			Type:        command.ArgumentString,
		},
	}
}
