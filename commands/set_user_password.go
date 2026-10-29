package commands

import (
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// SetUserPasswordCommand is the command for changing the password of a user made in a service
type SetUserPasswordCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand

	// noRestart is whether to skip restarting the apps linked as the user
	noRestart bool
}

// Name returns the name of the command
func (c *SetUserPasswordCommand) Name() string {
	return "set-user-password"
}

// Synopsis returns the synopsis of the command
func (c *SetUserPasswordCommand) Synopsis() string {
	return "Changes the password of a user created in a service"
}

// Help returns the help text for the command
func (c *SetUserPasswordCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *SetUserPasswordCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Gives the painter user in a postgres service named lollipop a new password": fmt.Sprintf("%s %s postgres lollipop painter", appName, c.Name()),
		"Gives it a password of your own":                                            fmt.Sprintf("%s %s postgres lollipop painter SOME_PASSWORD", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *SetUserPasswordCommand) Arguments() []command.Argument {
	args := accountArguments("user")
	args = append(args, command.Argument{
		Name:        "user-name",
		Description: "the name of the user whose password is changed",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "password",
		Description: "the new password, generated when left out",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *SetUserPasswordCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("postgres")
}

// ParsedArguments parses the arguments for the command
func (c *SetUserPasswordCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *SetUserPasswordCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.BoolVarP(&c.noRestart, "no-restart", "n", false, "whether to skip restarting the apps linked as the user")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *SetUserPasswordCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--no-restart": complete.PredictNothing,
		},
	)
}

// Run runs the command
func (c *SetUserPasswordCommand) Run(args []string) int {
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

	if err := internal.ChangeUserPassword(ctx, internal.ChangeUserPasswordInput{
		Datastore:   datastore,
		ServiceName: serviceName,
		User:        name,
		Password:    arguments["password"].StringValue(),
		NoRestart:   c.noRestart,
		Logger:      logger,
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	logger.Info(fmt.Sprintf("Password of user %s changed in %s", name, serviceName))
	return 0
}
