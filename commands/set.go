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

// SetCommand is the command for unexposing a service
type SetCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand

	// allowRestart is whether a property that only takes effect once the
	// service is restarted or reexposed has that done for it, when its value
	// changed
	allowRestart bool

	// noRestart leaves the apps linked to a service running on the config they
	// had when a password changes the url they hold
	noRestart bool
}

// Name returns the name of the command
func (c *SetCommand) Name() string {
	return "set"
}

// Synopsis returns the synopsis of the command
func (c *SetCommand) Synopsis() string {
	return "Sets or clears a property for a service"
}

// Help returns the help text for the command
func (c *SetCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *SetCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Sets the initial network for a redis service named test":              fmt.Sprintf("%s %s redis test initial-network my-network", appName, c.Name()),
		"Sets the memory limit for a redis service named test and restarts it": fmt.Sprintf("%s %s --allow-restart redis test memory 512", appName, c.Name()),
		"Changes the password of a postgres service named test":                fmt.Sprintf("%s %s postgres test password a-new-password", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *SetCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to set a property on",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to set a property on",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "key",
		Description: "the property to set",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "value",
		Description: "the value to set, or empty to unset the property",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *SetCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *SetCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *SetCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.BoolVar(&c.allowRestart, "allow-restart", false, "restart or reexpose the service when the property only takes effect that way and its value changed")
	f.BoolVar(&c.noRestart, "no-restart", false, "do not restart the linked apps when a password change updates the url they hold")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *SetCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--allow-restart": complete.PredictNothing,
			"--no-restart":    complete.PredictNothing,
		},
	)
}

// Run runs the command
func (c *SetCommand) Run(args []string) int {
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

	logger = c.Logger(c.Ui)

	arguments, err := c.ParsedArguments(flags.Args())
	if err != nil {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   err,
		})
		return 1
	}

	datastoreType := arguments["datastore-type"].StringValue()
	if datastoreType == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   fmt.Errorf("datastore type is required"),
		})
		return 1
	}

	datastore, ok := service.Datastores[datastoreType]
	if !ok {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("datastore type %s is not supported", datastoreType),
		})
		return 1
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
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	// a service runs the definition it was created with, which for a datastore
	// split by major version is not always the newest one
	datastore, unresolved := datastore.ForService(serviceName)
	if unresolved != nil {
		logger.Error(internal.ErrorInput{Error: unresolved})
		return 1
	}

	if !service.Exists(ctx, datastore, serviceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s does not exist", serviceName),
		})
		return 1
	}

	key := arguments["key"].StringValue()
	if key == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   errors.New("No key specified"), //nolint:staticcheck // matches the bash datastore plugins
		})
		return 1
	}

	// validate before announcing, so an unusable key or value does not print a
	// line claiming a change that will not happen
	if err := internal.ValidateProperty(key); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	value := arguments["value"].StringValue()
	if err := internal.ValidateServicePropertyValue(datastore, serviceName, key, value); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	property, _ := internal.LookupSettableProperty(key)

	// only a password reaches the linked apps, so the flag would do nothing
	// for anything else
	if c.noRestart && !property.IsSecret() {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("--no-restart only applies to a password, which is the only property the linked apps hold"),
		})
		return 1
	}

	// a password is changed in the datastore and in the linked apps, rather
	// than written and left for the service to read
	if property.IsSecret() {
		logger.Info(fmt.Sprintf("Setting %s", key))
		if err := internal.ChangeServiceSecret(ctx, internal.ChangeServiceSecretInput{
			Datastore:    datastore,
			ServiceName:  serviceName,
			Property:     property,
			Value:        value,
			AllowRestart: c.allowRestart,
			NoRestart:    c.noRestart,
			Logger:       logger,
		}); err != nil {
			logger.Error(internal.ErrorInput{Error: err})
			return 1
		}

		return 0
	}

	// read before anything is written, so that a value set again is told from
	// a change, and a read that fails leaves the property as it was
	restart := c.allowRestart && property.RequiresRestart()
	var before string
	if restart {
		if before, err = internal.ReadProperty(datastore, serviceName, key); err != nil {
			logger.Error(internal.ErrorInput{Error: err})
			return 1
		}
	}

	// a credential is never printed, not even to the terminal that typed it
	switch {
	case value == "":
		logger.Info(fmt.Sprintf("Unsetting %s", key))
	case property.Sensitive:
		logger.Info(fmt.Sprintf("Setting %s", key))
	default:
		logger.Info(fmt.Sprintf("Setting %s to %s", key, value))
	}

	if err := internal.SetProperty(datastore, serviceName, key, value); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	if err := internal.ApplyProperty(ctx, datastore, serviceName, key); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	if restart {
		after, err := internal.ReadProperty(datastore, serviceName, key)
		if err != nil {
			logger.Error(internal.ErrorInput{Error: err})
			return 1
		}

		if after != before {
			if err := internal.RestartForProperty(ctx, internal.RestartForPropertyInput{
				Datastore:   datastore,
				ServiceName: serviceName,
				Property:    property,
				Logger:      logger,
			}); err != nil {
				logger.Error(internal.ErrorInput{
					Error: fmt.Errorf("%s was set, but %s could not be restarted: %w", key, serviceName, err),
				})
				return 1
			}

			return 0
		}

		// still warned about below, since a value set earlier without the flag
		// may not have reached the container yet
		logger.Info(fmt.Sprintf("%s is unchanged, so %s was not restarted", key, serviceName))
	}

	if warning := property.Warning(datastore.Properties().CommandPrefix, serviceName); warning != "" {
		logger.Warn(internal.WarnInput{Warning: warning})
	}

	return 0
}
