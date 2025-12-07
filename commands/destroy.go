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

	"github.com/dokku/dokku/plugins/common"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// DestroyCommand is the command for destroying a datastore service
type DestroyCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand

	// force is whether to force the destruction of the service
	force bool
}

// Name returns the name of the command
func (c *DestroyCommand) Name() string {
	return "destroy"
}

// Synopsis returns the synopsis of the command
func (c *DestroyCommand) Synopsis() string {
	return "Destroys a datastore service"
}

// Help returns the help text for the command
func (c *DestroyCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *DestroyCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Destroys a redis service named test": fmt.Sprintf("%s %s redis test", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *DestroyCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to destroy",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to destroy",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *DestroyCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis", "postgres", "mysql", "mongodb", "elasticsearch")
}

// ParsedArguments parses the arguments for the command
func (c *DestroyCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *DestroyCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.BoolVar(&c.force, "force", false, "force the destruction of the service")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *DestroyCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--force": complete.PredictNothing,
		},
	)
}

// Run runs the command
func (c *DestroyCommand) Run(args []string) int {
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
	flags.Usage = func() { logger.Help(c.Help()) }
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

	serviceName := arguments["service-name"].StringValue()
	if serviceName == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   fmt.Errorf("service name is required"),
		})
		return 1
	}

	serviceWrapper, ok := service.Services[datastoreType]
	if !ok {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("datastore type %s is not supported", datastoreType),
		})
		return 1
	}

	// check if the service exists
	if !service.Exists(serviceWrapper, serviceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s does not exist", serviceName),
		})
		return 1
	}

	// check if the service is linked to any apps
	if len(service.LinkedApps(serviceWrapper, serviceName)) > 0 {
		logger.Error(internal.ErrorInput{
			Error: errors.New("cannot delete linked service"),
		})
		return 1
	}

	// if !c.force, ask for confirmation
	if !c.force {
		err := common.AskForDestructiveConfirmation(serviceName, fmt.Sprintf("%s service", datastoreType))
		if err != nil {
			logger.Error(internal.ErrorInput{
				Error: err,
			})
			return 1
		}
	}

	logger.Info(fmt.Sprintf("Destroying %s service %s", datastoreType, serviceName))
	err = internal.DestroyService(ctx, internal.DestroyServiceInput{
		DatastoreType: datastoreType,
		ServiceName:   serviceName,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	return 0
}
