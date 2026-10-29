package commands

import (
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal"

	"github.com/dokku/dokku/plugins/common"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// DestroyDatabaseCommand is the command for destroying a database made in a service
type DestroyDatabaseCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand

	// force is whether to destroy it without asking first
	force bool
}

// Name returns the name of the command
func (c *DestroyDatabaseCommand) Name() string {
	return "destroy-database"
}

// Synopsis returns the synopsis of the command
func (c *DestroyDatabaseCommand) Synopsis() string {
	return "Destroys a database created in a service"
}

// Help returns the help text for the command
func (c *DestroyDatabaseCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *DestroyDatabaseCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Destroys the paintshop database in a postgres service named lollipop": fmt.Sprintf("%s %s postgres lollipop paintshop", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *DestroyDatabaseCommand) Arguments() []command.Argument {
	args := accountArguments("database")
	args = append(args, command.Argument{
		Name:        "database-name",
		Description: "the name of the database to destroy",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *DestroyDatabaseCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("postgres")
}

// ParsedArguments parses the arguments for the command
func (c *DestroyDatabaseCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *DestroyDatabaseCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.BoolVarP(&c.force, "force", "f", false, "destroy it without asking for confirmation")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *DestroyDatabaseCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--force": complete.PredictNothing,
		},
	)
}

// Run runs the command
func (c *DestroyDatabaseCommand) Run(args []string) int {
	ctx := cancelOnSignal()

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

	datastore, serviceName, code, ok := resolveAccountService(ctx, c, logger, arguments, c.Name())
	if !ok {
		return code
	}

	name := arguments["database-name"].StringValue()
	if name == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   fmt.Errorf("database-name is required"),
		})
		return 1
	}

	if os.Getenv("DOKKU_APPS_FORCE_DELETE") == "1" {
		c.force = true
	}

	if !c.force {
		if err := common.AskForDestructiveConfirmation(name, fmt.Sprintf("database in the %s service %s", datastore.Definition.Dokku.Plugin, serviceName)); err != nil {
			logger.Error(internal.ErrorInput{Error: err})
			return 1
		}
	}

	if err := internal.DestroyDatabase(ctx, internal.AccountInput{
		Datastore:   datastore,
		ServiceName: serviceName,
		Name:        name,
		Logger:      logger,
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	logger.Info(fmt.Sprintf("Database %s destroyed in %s", name, serviceName))
	return 0
}
