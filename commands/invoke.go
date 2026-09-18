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
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// InvokeCommand runs a command a datastore adds for itself.
//
// There is one of these however many custom commands exist, because what the
// tool implements is fixed: a datastore adding a command of its own must not
// add one to every other datastore, nor to the tool's own command list.
type InvokeCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
}

// Name returns the name of the command
func (c *InvokeCommand) Name() string {
	return "invoke"
}

// Synopsis returns the synopsis of the command
func (c *InvokeCommand) Synopsis() string {
	return "Invokes a datastore's own command against a service"
}

// Help returns the help text for the command
func (c *InvokeCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *InvokeCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Connects to a mongo service as its admin user": fmt.Sprintf("%s %s mongo connect-admin lollipop", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *InvokeCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore the command belongs to",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "command-name",
		Description: "the command the datastore declares",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to run against",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "command-arguments",
		Description: "the arguments the command declares, in order",
		Optional:    true,
		Type:        command.ArgumentList,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *InvokeCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

// ParsedArguments parses the arguments for the command
func (c *InvokeCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *InvokeCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *InvokeCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

// Run runs the command
func (c *InvokeCommand) Run(args []string) int {
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

	commandName := arguments["command-name"].StringValue()

	// a command this datastore does not add exits the way dokku expects of a
	// plugin that does not handle one, so another datastore's command is not a
	// command here
	if !datastore.ImplementsCustom(commandName) {
		return notImplementedExit()
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
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	// a service runs the definition it was created with, which for a datastore
	// split by major version is not always the newest one
	datastore = datastore.ForService(serviceName)

	if !service.Exists(ctx, datastore, serviceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s does not exist", serviceName),
		})
		return 1
	}

	if err := datastore.RunCommand(ctx, service.RunCommandInput{
		ServiceName: serviceName,
		Name:        commandName,
		Arguments:   arguments["command-arguments"].ListValue(),
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	return 0
}
