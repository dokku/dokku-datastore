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

// TriggerCommand runs a command a datastore adds for itself.
//
// There is one of these however many custom commands exist, because what the
// tool implements is fixed: a datastore adding a command of its own must not
// add one to every other datastore, nor to the tool's own command list.
type TriggerCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
}

// Name returns the name of the command
func (c *TriggerCommand) Name() string {
	return "trigger"
}

// Synopsis returns the synopsis of the command
func (c *TriggerCommand) Synopsis() string {
	return "Runs a dokku trigger a datastore implements"
}

// Help returns the help text for the command
func (c *TriggerCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *TriggerCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Runs the post-extract trigger for a solr service": fmt.Sprintf("%s %s post-extract solr lollipop /tmp/build", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *TriggerCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "trigger-name",
		Description: "the dokku trigger being handled",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore handling the trigger",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "trigger-arguments",
		Description: "the arguments dokku passed the trigger, the first of which is the app",
		Optional:    true,
		Type:        command.ArgumentList,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *TriggerCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictAnything
}

// ParsedArguments parses the arguments for the command
func (c *TriggerCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *TriggerCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *TriggerCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

// Run runs the command
func (c *TriggerCommand) Run(args []string) int {
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

	logger = internal.Ui{Ui: c.Ui, Format: c.format, Quiet: c.quiet, Trace: c.trace}

	arguments, err := c.ParsedArguments(flags.Args())
	if err != nil {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   err,
		})
		return 1
	}

	datastoreType := arguments["datastore-type"].StringValue()
	datastore, ok := service.Datastores[datastoreType]
	if !ok {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("datastore type %s is not supported", datastoreType),
		})
		return 1
	}

	triggerName := arguments["trigger-name"].StringValue()

	// a trigger this datastore does not implement is not a failure: dokku runs
	// every plugin's trigger for every event, and most of them do not apply
	if !datastore.HandlesTrigger(triggerName) {
		return 0
	}

	triggerArguments := arguments["trigger-arguments"].ListValue()
	if len(triggerArguments) == 0 {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   errors.New("the trigger was given no app to act on"),
		})
		return 1
	}

	// every trigger names the app first, which is what decides the services
	// this one has anything to do with
	appName := triggerArguments[0]
	services, err := internal.LinkedServices(ctx, internal.LinkedServicesInput{
		AppName:   appName,
		Datastore: datastore,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	for _, serviceName := range services {
		err := datastore.ForService(serviceName).RunTrigger(ctx, service.RunTriggerInput{
			ServiceName: serviceName,
			Name:        triggerName,
			Arguments:   triggerArguments,
		})
		if err != nil {
			logger.Error(internal.ErrorInput{Error: err})
			return 1
		}
	}

	return 0
}
