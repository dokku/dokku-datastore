package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/datastores"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// ExposeCommand is the command for exposing a service
type ExposeCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
}

// Name returns the name of the command
func (c *ExposeCommand) Name() string {
	return "expose"
}

// Synopsis returns the synopsis of the command
func (c *ExposeCommand) Synopsis() string {
	return "Exposes a service"
}

// Help returns the help text for the command
func (c *ExposeCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *ExposeCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Checks if a redis service named test exists": fmt.Sprintf("%s %s redis test", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *ExposeCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to enter",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to enter",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "ports",
		Description: "the ports to expose",
		Optional:    true,
		Type:        command.ArgumentList,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *ExposeCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *ExposeCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *ExposeCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *ExposeCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

// Run runs the command
func (c *ExposeCommand) Run(args []string) int {
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

	logger := internal.Ui{
		Ui:     c.Ui,
		Format: c.format,
		Quiet:  c.quiet,
		Trace:  c.trace,
	}

	flags := c.FlagSet()
	flags.Usage = func() {
		logger.Help(c.Help()) //nolint:errcheck
	}
	if err := flags.Parse(args); err != nil {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   err,
		})
		return 1
	}

	arguments, err := c.ParsedArguments(flags.Args())
	if err != nil {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   err,
		})
		return 1
	}

	datastoreType := arguments["datastore-type"].StringValue()
	if datastoreType == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   fmt.Errorf("datastore type is required"),
		})
		return 1
	}

	datastore, ok := datastores.Datastores[datastoreType]
	if !ok {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("datastore type %s is not supported", datastoreType),
		})
		return 1
	}

	serviceName := arguments["service-name"].StringValue()
	if serviceName == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   fmt.Errorf("service name is required"),
		})
		return 1
	}

	if err := datastores.ValidateServiceName(serviceName); err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	if !datastores.Exists(ctx, datastore, serviceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s does not exist", serviceName),
		})
		return 1
	}

	if internal.IsExposed(datastore, serviceName) {
		logger.Header2(fmt.Sprintf("Service %s is already exposed", serviceName)) //nolint:errcheck
		err = datastores.ServicePortReconcileStatus(ctx, datastores.ServicePortReconcileStatusInput{
			Datastore:   datastore,
			ServiceName: serviceName,
		})
		if err != nil {
			logger.Error(internal.ErrorInput{
				Error: err,
			})
			return 1
		}
		return 0
	}

	if internal.AmbassadorContainerExists(datastore, serviceName) {
		logger.Warn(internal.WarnInput{
			Warning: fmt.Sprintf("Service %s has an untracked expose container, removing", serviceName),
		})
		err = internal.RemoveAmbassadorContainer(ctx, datastore, serviceName)
		if err != nil {
			logger.Warn(internal.WarnInput{
				Warning: err.Error(),
			})
			return 1
		}
		return 1
	}

	err = internal.ExposeService(ctx, internal.ExposeServiceInput{
		Datastore:   datastore,
		Ports:       arguments["ports"].ListValue(),
		ServiceName: serviceName,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}
	logger.Header2(fmt.Sprintf("Service %s exposed on port(s) [container->host]: %s", serviceName, datastores.ExposedPorts(datastore, serviceName))) //nolint:errcheck
	return 0
}
