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

// DestroyUserCommand is the command for destroying a user made in a service
type DestroyUserCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand

	// force is whether to destroy it without asking first
	force bool
}

// Name returns the name of the command
func (c *DestroyUserCommand) Name() string {
	return "destroy-user"
}

// Synopsis returns the synopsis of the command
func (c *DestroyUserCommand) Synopsis() string {
	return "Destroys a user created in a service"
}

// Help returns the help text for the command
func (c *DestroyUserCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *DestroyUserCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Destroys the painter user in a postgres service named lollipop": fmt.Sprintf("%s %s postgres lollipop painter", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *DestroyUserCommand) Arguments() []command.Argument {
	args := accountArguments("user")
	args = append(args, command.Argument{
		Name:        "user-name",
		Description: "the name of the user to destroy",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *DestroyUserCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("postgres")
}

// ParsedArguments parses the arguments for the command
func (c *DestroyUserCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *DestroyUserCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.BoolVarP(&c.force, "force", "f", false, "destroy it without asking for confirmation")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *DestroyUserCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--force": complete.PredictNothing,
		},
	)
}

// Run runs the command
func (c *DestroyUserCommand) Run(args []string) int {
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

	name := arguments["user-name"].StringValue()
	if name == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   fmt.Errorf("user-name is required"),
		})
		return 1
	}

	if os.Getenv("DOKKU_APPS_FORCE_DELETE") == "1" {
		c.force = true
	}

	if !c.force {
		if err := common.AskForDestructiveConfirmation(name, fmt.Sprintf("user in the %s service %s", datastore.Definition.Dokku.Plugin, serviceName)); err != nil {
			logger.Error(internal.ErrorInput{Error: err})
			return 1
		}
	}

	if err := internal.DestroyUser(ctx, internal.AccountInput{
		Datastore:   datastore,
		ServiceName: serviceName,
		Name:        name,
		Logger:      logger,
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	logger.Info(fmt.Sprintf("User %s destroyed in %s", name, serviceName))
	return 0
}
