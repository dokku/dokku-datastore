package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/datastores"
	"github.com/dokku/dokku/plugins/common"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// LinkedCommand is the command for checking if a service is linked to an app
type LinkedCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
}

// Name returns the name of the command
func (c *LinkedCommand) Name() string {
	return "linked"
}

// Synopsis returns the synopsis of the command
func (c *LinkedCommand) Synopsis() string {
	return "Checks if a service is linked to an app"
}

// Help returns the help text for the command
func (c *LinkedCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *LinkedCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Checks if a redis service named test is linked to the app test-app": fmt.Sprintf("%s %s redis test test-app", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *LinkedCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to check if the service is linked to an app",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to check if it is linked to an app",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "app-name",
		Description: "the name of the app to check if the service is linked to",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *LinkedCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *LinkedCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *LinkedCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *LinkedCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

// Run runs the command
func (c *LinkedCommand) Run(args []string) int {
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

	appName := arguments["app-name"].StringValue()
	if appName == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   fmt.Errorf("app name is required"),
		})
		return 1
	}

	if err := common.VerifyAppName(appName); err != nil {
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

	linkedServices, err := internal.LinkedServices(ctx, internal.LinkedServicesInput{
		AppName:   appName,
		Datastore: datastore,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	if slices.Contains(linkedServices, serviceName) {
		logger.Info(fmt.Sprintf("Service %s is linked to app %s", serviceName, appName))
	} else {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s is not linked to app %s", serviceName, appName),
		})
		return 1
	}

	return 0
}
