package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

type ListCommand struct {
	command.Meta
	GlobalFlagCommand
}

func (c *ListCommand) Name() string {
	return "list"
}

func (c *ListCommand) Synopsis() string {
	return "Lists all datastores"
}

func (c *ListCommand) Help() string {
	return command.CommandHelp(c)
}

func (c *ListCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Lists all redis datastores":         fmt.Sprintf("%s %s redis", appName, c.Name()),
		"Lists all postgres datastores":      fmt.Sprintf("%s %s postgres", appName, c.Name()),
		"Lists all mysql datastores":         fmt.Sprintf("%s %s mysql", appName, c.Name()),
		"Lists all mongodb datastores":       fmt.Sprintf("%s %s mongodb", appName, c.Name()),
		"Lists all elasticsearch datastores": fmt.Sprintf("%s %s elasticsearch", appName, c.Name()),
	}
}

func (c *ListCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to list",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	return args
}

func (c *ListCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis", "postgres", "mysql", "mongodb", "elasticsearch")
}

func (c *ListCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

func (c *ListCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

func (c *ListCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

func (c *ListCommand) Run(args []string) int {
	flags := c.FlagSet()
	flags.Usage = func() { c.Ui.Output(c.Help()) }
	if err := flags.Parse(args); err != nil {
		c.Ui.Error(err.Error())
		c.Ui.Error(command.CommandErrorText(c))
		return 1
	}

	arguments, err := c.ParsedArguments(flags.Args())
	if err != nil {
		c.Ui.Error(err.Error())
		c.Ui.Error(command.CommandErrorText(c))
		return 1
	}

	datastoreType := arguments["datastore-type"].StringValue()
	if datastoreType == "" {
		c.Ui.Error("Datastore type is required")
		return 1
	}

	datastores, err := internal.ListDatastores(datastoreType)
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	switch c.format {
	case "json":
		err = json.NewEncoder(os.Stdout).Encode(datastores)
		if err != nil {
			c.Ui.Error(err.Error())
			return 1
		}
	case "text":
		logger, ok := c.Ui.(*command.ZerologUi)
		if !ok {
			c.Ui.Error("Failed to cast Ui to ZerologUi")
			return 1
		}
		if !c.quiet {
			logger.LogHeader1(fmt.Sprintf("%v datastores", datastoreType))
		}

		for _, datastore := range datastores {
			if !c.quiet {
				c.Ui.Output(datastore)
			}
		}
	default:
		c.Ui.Error("Invalid format")
		return 1
	}

	return 0
}
