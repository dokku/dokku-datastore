package commands

import (
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// CreateUserCommand is the command for creating a user in a service
type CreateUserCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
}

// Name returns the name of the command
func (c *CreateUserCommand) Name() string {
	return "create-user"
}

// Synopsis returns the synopsis of the command
func (c *CreateUserCommand) Synopsis() string {
	return "Creates a user in a service"
}

// Help returns the help text for the command
func (c *CreateUserCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *CreateUserCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Creates a user named painter in a postgres service named lollipop": fmt.Sprintf("%s %s postgres lollipop painter", appName, c.Name()),
		"Creates it with a password of your own":                            fmt.Sprintf("%s %s postgres lollipop painter SOME_PASSWORD", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *CreateUserCommand) Arguments() []command.Argument {
	args := accountArguments("user")
	args = append(args, command.Argument{
		Name:        "user-name",
		Description: "the name of the user to create",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "password",
		Description: "the password to give the user, generated when left out",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *CreateUserCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("postgres")
}

// ParsedArguments parses the arguments for the command
func (c *CreateUserCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *CreateUserCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *CreateUserCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
	)
}

// Run runs the command
func (c *CreateUserCommand) Run(args []string) int {
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

	if err := internal.CreateUser(ctx, internal.AccountInput{
		Datastore:   datastore,
		ServiceName: serviceName,
		Name:        name,
		Password:    arguments["password"].StringValue(),
		Logger:      logger,
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	logger.Info(fmt.Sprintf("User %s created in %s", name, serviceName))
	return 0
}
