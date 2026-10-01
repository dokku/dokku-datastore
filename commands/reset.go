package commands

import (
	"context"
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

// ResetCommand is the command for deleting all data in a datastore service
type ResetCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand

	// force is whether to reset the service without asking first
	force bool
}

// Name returns the name of the command
func (c *ResetCommand) Name() string {
	return "reset"
}

// Synopsis returns the synopsis of the command
func (c *ResetCommand) Synopsis() string {
	return "Deletes all data in a datastore service"
}

// Help returns the help text for the command
func (c *ResetCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *ResetCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Deletes all data in a redis service named test":                fmt.Sprintf("%s %s redis test", appName, c.Name()),
		"Deletes all data in a redis service named test without asking": fmt.Sprintf("%s %s redis test --force", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *ResetCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to reset",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to reset",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *ResetCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis", "postgres", "mysql", "mongodb", "elasticsearch")
}

// ParsedArguments parses the arguments for the command
func (c *ResetCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *ResetCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.BoolVarP(&c.force, "force", "f", false, "reset the service without asking for its name first")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *ResetCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--force": complete.PredictNothing,
		},
	)
}

// Run runs the command
func (c *ResetCommand) Run(args []string) int {
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

	logger = c.Logger(c.Ui)

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

	if code, unimplemented := requireImplemented(datastore, "reset"); unimplemented {
		return code
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

	// a service runs the definition it was created with, and how its data is
	// deleted is that definition's to say
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

	// unlike destroy, a linked service is not refused: keeping the links while
	// the data goes is what this command is for

	if os.Getenv("DOKKU_APPS_FORCE_DELETE") == "1" {
		c.force = true
	}

	if !c.force {
		err := common.AskForDestructiveConfirmation(serviceName, fmt.Sprintf("all data in the %s service", datastoreType))
		if err != nil {
			logger.Error(internal.ErrorInput{
				Error: err,
			})
			return 1
		}
	}

	logger.Info(fmt.Sprintf("Resetting %s service %s", datastoreType, serviceName))
	if err := datastore.ResetService(ctx, service.ResetServiceInput{
		Datastore:   datastore,
		ServiceName: serviceName,
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	// a datastore reset offline was stopped and started again, and the caller
	// asked for a service its apps can use
	if err := internal.WaitForService(ctx, internal.WaitForServiceInput{
		Datastore:   datastore,
		ServiceName: serviceName,
		Logger:      logger,
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	logger.Header2(fmt.Sprintf("%s service %s reset", datastore.Title(), serviceName)) //nolint:errcheck

	return 0
}
