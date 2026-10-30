package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// LabelCommand is the command for adding docker labels to a service
type LabelCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
}

// Name returns the name of the command
func (c *LabelCommand) Name() string {
	return "label"
}

// Synopsis returns the synopsis of the command
func (c *LabelCommand) Synopsis() string {
	return "Adds docker labels to a service"
}

// Help returns the help text for the command
func (c *LabelCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *LabelCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Adds a label to a redis service named test": fmt.Sprintf("%s %s redis test traefik.enable=true", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *LabelCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to label",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to label",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "labels",
		Description: "the labels, as <key>=<value>",
		Optional:    true,
		Type:        command.ArgumentList,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *LabelCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *LabelCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *LabelCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *LabelCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
	)
}

// Run runs the command
func (c *LabelCommand) Run(args []string) int {
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

	message, err := internal.LabelService(internal.LabelServiceInput{
		Datastore:   datastore,
		ServiceName: serviceName,
		Specs:       arguments["labels"].ListValue(),
	})
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	logger.Info(message)
	logger.Info(labelsRebuildNote(datastore))
	return 0
}

// labelsRebuildNote says when a change to a service's labels takes effect,
// which is not when the command that made it returns: docker cannot change the
// labels of a container it has already made
func labelsRebuildNote(datastore *service.Datastore) string {
	commandPrefix := datastore.Properties().CommandPrefix
	return fmt.Sprintf("Labels reach the container the next time one is built; on a running service use %s and then %s", hostenv.Invocation(commandPrefix, "stop"), hostenv.Invocation(commandPrefix, "start"))
}
