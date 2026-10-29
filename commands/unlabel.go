package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// UnlabelCommand is the command for removing labels from a service
type UnlabelCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand

	// all removes every label the service has
	all bool
}

// Name returns the name of the command
func (c *UnlabelCommand) Name() string {
	return "unlabel"
}

// Synopsis returns the synopsis of the command
func (c *UnlabelCommand) Synopsis() string {
	return "Removes one or all labels from a service"
}

// Help returns the help text for the command
func (c *UnlabelCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *UnlabelCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Removes a label from a redis service named test": fmt.Sprintf("%s %s redis test traefik.enable", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *UnlabelCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to remove labels from",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to remove labels from",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "keys",
		Description: "the keys of the labels to remove",
		Optional:    true,
		Type:        command.ArgumentList,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *UnlabelCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *UnlabelCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *UnlabelCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.BoolVar(&c.all, "all", false, "remove every label the service has")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *UnlabelCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--all": complete.PredictNothing,
		},
	)
}

// Run runs the command
func (c *UnlabelCommand) Run(args []string) int {
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

	datastore, serviceName, ok := resolveExistingService(ctx, c, logger, arguments)
	if !ok {
		return 1
	}

	message, err := internal.UnlabelService(internal.UnlabelServiceInput{
		All:         c.all,
		Datastore:   datastore,
		Keys:        arguments["keys"].ListValue(),
		ServiceName: serviceName,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	logger.Info(message)
	logger.Info(labelsRebuildNote(datastore))
	return 0
}
