package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// ReadmeCommand is the command for generating a datastore plugin's readme
type ReadmeCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
	// CommandFunc is the registry of every command this binary implements
	CommandFunc command.CommandFunc
	// pluginDir is the plugin checkout to generate the readme for
	pluginDir string
}

// Name returns the name of the command
func (c *ReadmeCommand) Name() string {
	return "readme"
}

// Synopsis returns the synopsis of the command
func (c *ReadmeCommand) Synopsis() string {
	return "Writes a datastore plugin's readme to stdout"
}

// Help returns the help text for the command
func (c *ReadmeCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *ReadmeCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Generates the readme for the redis plugin": fmt.Sprintf("%s %s redis", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *ReadmeCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to generate the readme for",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *ReadmeCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *ReadmeCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *ReadmeCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.StringVar(&c.pluginDir, "plugin-dir", ".", "the plugin checkout to generate the readme for")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *ReadmeCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--plugin-dir": complete.PredictDirs("*"),
		},
	)
}

// Run runs the command
func (c *ReadmeCommand) Run(args []string) int {
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

	sponsors, err := internal.PluginSponsors(c.pluginDir)
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	// the readme documents what the datastore offers, filtered the same way the
	// help is, with the datastore's own commands beside the rest
	documented := []internal.PluginCommand{}
	for _, pluginCommand := range pluginCommands(context.Background(), c.Meta, c.CommandFunc) {
		if pluginCommand.Name() == "invoke" {
			continue
		}

		if service.Implements(datastore, pluginCommand.Name()) {
			documented = append(documented, pluginCommand)
		}
	}

	documented = append(documented, internal.CustomCommands(datastore)...)

	readme, err := internal.Readme(internal.ReadmeInput{
		Commands: documented,
		Data: internal.NewDocumentationData(internal.DocumentationDataInput{
			Datastore: datastore,
			PluginDir: c.pluginDir,
		}),
		PluginDir: c.pluginDir,
		Sponsors:  sponsors,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	fmt.Print(readme)
	return 0
}
