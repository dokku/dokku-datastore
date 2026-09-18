package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// SetCommand is the command for unexposing a service
type SetCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
}

// Name returns the name of the command
func (c *SetCommand) Name() string {
	return "set"
}

// Synopsis returns the synopsis of the command
func (c *SetCommand) Synopsis() string {
	return "Sets or clears a property for a service"
}

// Help returns the help text for the command
func (c *SetCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *SetCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Sets the initial network for a redis service named test": fmt.Sprintf("%s %s redis test initial-network my-network", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *SetCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to set a property on",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to set a property on",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "key",
		Description: "the property to set",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "value",
		Description: "the value to set, or empty to unset the property",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *SetCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *SetCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *SetCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *SetCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

// Run runs the command
func (c *SetCommand) Run(args []string) int {
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

	logger := internal.Ui{Ui: c.Ui}
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

	logger = internal.Ui{
		Ui:     c.Ui,
		Format: c.format,
		Quiet:  c.quiet,
		Trace:  c.trace,
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

	datastore, ok := service.Datastores[datastoreType]
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
			Error:   service.ErrMissingServiceName,
		})
		return 1
	}

	if err := service.ValidateServiceName(serviceName); err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	// a service runs the definition it was created with, which for a datastore
	// split by major version is not always the newest one
	datastore, unresolved := datastore.ForService(serviceName)
	if unresolved != nil {
		logger.Error(internal.ErrorInput{Error: unresolved})
		return 1
	}

	if !service.Exists(ctx, datastore, serviceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s does not exist", serviceName),
		})
		return 1
	}

	key := arguments["key"].StringValue()
	if key == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   errors.New("No key specified"), //nolint:staticcheck // matches the bash datastore plugins
		})
		return 1
	}

	// validate before announcing, so an unusable key does not print a line
	// claiming a change that will not happen
	if err := internal.ValidateProperty(key); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	value := arguments["value"].StringValue()
	if value == "" {
		logger.Info(fmt.Sprintf("Unsetting %s", key))
	} else {
		logger.Info(fmt.Sprintf("Setting %s to %s", key, value))
	}

	if err := internal.SetProperty(datastore, serviceName, key, value); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	return 0
}
