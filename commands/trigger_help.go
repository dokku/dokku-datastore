package commands

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// defaultNotImplementedExit is the exit code dokku reads as "this plugin does
// not implement the command you asked for, keep looking"
const defaultNotImplementedExit = 10

// TriggerHelpCommand is the command backing a plugin's commands script
type TriggerHelpCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
	// CommandFunc is the registry of every command this binary implements
	CommandFunc command.CommandFunc
}

// Name returns the name of the command
func (c *TriggerHelpCommand) Name() string {
	return "trigger-help"
}

// Synopsis returns the synopsis of the command
func (c *TriggerHelpCommand) Synopsis() string {
	return "Prints the help a dokku plugin's commands script is asked for"
}

// Help returns the help text for the command
func (c *TriggerHelpCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *TriggerHelpCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Lists every command the redis plugin implements": fmt.Sprintf("%s %s redis redis:help", appName, c.Name()),
		"Prints the help for a single command":            fmt.Sprintf("%s %s redis redis:help create", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *TriggerHelpCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to print the help for",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "help-arguments",
		Description: "the command line dokku is asking about",
		Optional:    true,
		Type:        command.ArgumentList,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *TriggerHelpCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *TriggerHelpCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *TriggerHelpCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *TriggerHelpCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

// Run runs the command. The arguments are whatever dokku handed the plugin's
// commands script, so they are read as they arrive rather than parsed as flags.
func (c *TriggerHelpCommand) Run(args []string) int {
	logger := internal.Ui{Ui: c.Ui}

	arguments, err := c.ParsedArguments(args)
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

	data := internal.NewDocumentationData(internal.DocumentationDataInput{
		Datastore: datastore,
		PluginDir: hostenv.PluginCheckout(),
	})

	// the listing is filtered by the same rule the commands themselves apply,
	// so a datastore never advertises something that would exit as not a command
	implemented := []internal.PluginCommand{}
	for _, pluginCommand := range pluginCommands(context.Background(), c.Meta, c.CommandFunc) {
		// invoke is how a datastore's own commands are reached, not a command a
		// datastore has: the commands themselves are listed below
		if pluginCommand.Name() == "invoke" {
			continue
		}

		if service.Implements(datastore, pluginCommand.Name()) {
			implemented = append(implemented, pluginCommand)
		}
	}

	// a datastore's own commands are its commands, so they are listed beside
	// the rest rather than hidden behind the verb that dispatches them
	implemented = append(implemented, internal.CustomCommands(datastore.Definition)...)

	input := internal.PluginHelpInput{
		Commands: implemented,
		Data:     data,
	}

	argv := arguments["help-arguments"].ListValue()
	requested := ""
	if len(argv) > 0 {
		requested = argv[0]
	}

	subcommand := ""
	if len(argv) > 1 {
		subcommand = argv[1]
	}

	prefix := data.CommandPrefix
	switch requested {
	case prefix, prefix + ":help", prefix + ":default":
		return c.printPluginHelp(logger, input, subcommand)
	case "help":
		return c.printCommandList(logger, input)
	}

	return notImplementedExit()
}

// printPluginHelp prints the help the user asked for by name, which is either
// the plugin as a whole or one of its commands
func (c *TriggerHelpCommand) printPluginHelp(logger internal.Ui, input internal.PluginHelpInput, subcommand string) int {
	if subcommand == "" || subcommand == "--all" {
		overview, err := internal.PluginHelpOverview(input)
		if err != nil {
			logger.Error(internal.ErrorInput{Error: err})
			return 1
		}

		fmt.Print(overview)
		return 0
	}

	pluginCommand, ok := findPluginCommand(input.Commands, subcommand)
	if !ok {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("%s:%s is not a %s command", input.Data.CommandPrefix, subcommand, input.Data.CommandPrefix),
		})
		return 1
	}

	help, err := internal.PluginCommandHelp(pluginCommand, input.Data)
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	fmt.Print(help)
	return 0
}

// printCommandList answers dokku's request for the commands this plugin adds to
// the top level command listing. Dokku does not forward the --all flag, so the
// long form is only printed when the caller can be seen to have asked for it.
func (c *TriggerHelpCommand) printCommandList(logger internal.Ui, input internal.PluginHelpInput) int {
	if !internal.AncestorInvokedWith("--all") {
		fmt.Println(internal.PluginSummaryLine(input.Data))
		return 0
	}

	lines, err := internal.PluginCommandLines(input)
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	for _, line := range lines {
		fmt.Println(line)
	}

	return 0
}

// notImplementedExit is the exit code that tells dokku to keep looking for a
// plugin that implements the command the user asked for
func notImplementedExit() int {
	if code, err := strconv.Atoi(os.Getenv("DOKKU_NOT_IMPLEMENTED_EXIT")); err == nil {
		return code
	}

	return defaultNotImplementedExit
}
