package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// TriggerNamespaceHelpCommand is the command backing the commands script of a
// plugin that serves every datastore under one command, such as the datastore
// plugin dokku ships
type TriggerNamespaceHelpCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
	// CommandFunc is the registry of every command this binary implements
	CommandFunc command.CommandFunc
}

// Name returns the name of the command
func (c *TriggerNamespaceHelpCommand) Name() string {
	return "trigger-namespace-help"
}

// Synopsis returns the synopsis of the command
func (c *TriggerNamespaceHelpCommand) Synopsis() string {
	return "Prints the help a plugin serving every datastore is asked for"
}

// Help returns the help text for the command
func (c *TriggerNamespaceHelpCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *TriggerNamespaceHelpCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Lists every datastore and the commands they share":  fmt.Sprintf("%s %s datastore:help", appName, c.Name()),
		"Lists every command the redis datastore implements": fmt.Sprintf("%s %s datastore:help redis", appName, c.Name()),
		"Prints the help for a single redis command":         fmt.Sprintf("%s %s datastore:help redis create", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *TriggerNamespaceHelpCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "help-arguments",
		Description: "the command line dokku is asking about",
		Optional:    true,
		Type:        command.ArgumentList,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *TriggerNamespaceHelpCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

// ParsedArguments parses the arguments for the command
func (c *TriggerNamespaceHelpCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *TriggerNamespaceHelpCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *TriggerNamespaceHelpCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

// Run runs the command. The arguments are whatever dokku handed the plugin's
// commands script, so they are read as they arrive rather than parsed as flags.
func (c *TriggerNamespaceHelpCommand) Run(args []string) int {
	logger := internal.Ui{Ui: c.Ui}

	// dokku pipes the help through sort and column, so there is no json to be
	// had, as with trigger-help
	if askedForJSON(args) {
		logger.Error(internal.ErrorInput{Error: errTextOnly(c.Name())})
		return 1
	}

	namespace := hostenv.CommandNamespace()
	if namespace == "" {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("%s only answers for a plugin serving every datastore under one command", c.Name()),
		})
		return 1
	}

	if service.LoadError != nil {
		logger.Error(internal.ErrorInput{Error: service.LoadError})
		return 1
	}

	requested := ""
	if len(args) > 0 {
		requested = args[0]
	}

	switch requested {
	case namespace, namespace + ":help", namespace + ":default":
		return c.printHelp(logger, args[1:])
	case "help":
		return c.printCommandList(logger)
	}

	return NotImplementedExit()
}

// namespaceHelpInput is every command any datastore implements, other than the
// verb a datastore's own commands are reached through, and every datastore
func (c *TriggerNamespaceHelpCommand) namespaceHelpInput() internal.NamespaceHelpInput {
	shared := []internal.PluginCommand{}
	for _, pluginCommand := range pluginCommands(context.Background(), c.Meta, c.CommandFunc) {
		if pluginCommand.Name() == "invoke" {
			continue
		}

		shared = append(shared, pluginCommand)
	}

	return internal.NamespaceHelpInput{
		Commands: shared,
		Types:    service.Types(),
	}
}

// printHelp prints the help the user asked for by name: the plugin as a whole,
// one datastore, or one of a datastore's commands
func (c *TriggerNamespaceHelpCommand) printHelp(logger internal.Ui, argv []string) int {
	if len(argv) == 0 || argv[0] == "--all" {
		overview, err := internal.NamespaceHelpOverview(c.namespaceHelpInput())
		if err != nil {
			logger.Error(internal.ErrorInput{Error: err})
			return 1
		}

		fmt.Print(overview)
		return 0
	}

	datastore, ok := service.Datastores[argv[0]]
	if !ok {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("%s is not a datastore type, which is one of: %s", argv[0], strings.Join(service.Types(), ", ")),
		})
		return 1
	}

	subcommand := ""
	if len(argv) > 1 {
		subcommand = argv[1]
	}

	return printPluginHelp(logger, datastoreHelpInput(c.Meta, c.CommandFunc, datastore), subcommand)
}

// printCommandList answers dokku's request for the commands this plugin adds to
// the top level command listing, which as with trigger-help is the long form
// only when the caller can be seen to have asked for it
func (c *TriggerNamespaceHelpCommand) printCommandList(logger internal.Ui) int {
	if !internal.AncestorInvokedWith("--all") {
		fmt.Println(internal.NamespaceSummaryLine())
		return 0
	}

	lines, err := internal.NamespaceCommandLines(c.namespaceHelpInput())
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	for _, line := range lines {
		fmt.Println(line)
	}

	return 0
}
