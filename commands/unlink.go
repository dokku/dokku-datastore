package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/datastores"
	"github.com/dokku/dokku/plugins/common"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// UnlinkCommand is the command for checking if a service is linked to an app
type UnlinkCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
	// noRestart is whether to skip restarting the app
	noRestart bool
}

// Name returns the name of the command
func (c *UnlinkCommand) Name() string {
	return "unlink"
}

// Synopsis returns the synopsis of the command
func (c *UnlinkCommand) Synopsis() string {
	return "Unlinks a service from an app"
}

// Help returns the help text for the command
func (c *UnlinkCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *UnlinkCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Links a redis service named test to the app test-app": fmt.Sprintf("%s %s redis test test-app", appName, c.Name()),
		"Links it as BLUE_URL instead of the default alias":    fmt.Sprintf("%s %s redis test test-app --alias BLUE", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *UnlinkCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to unlink",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to unlink",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "app-name",
		Description: "the name of the app to unlink the service from",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *UnlinkCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *UnlinkCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *UnlinkCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.BoolVarP(&c.noRestart, "no-restart", "n", false, "whether to skip restarting the app")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *UnlinkCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--no-restart": complete.PredictNothing,
		},
	)
}

// Run runs the command
func (c *UnlinkCommand) Run(args []string) int {
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

	logger = internal.Ui{
		Ui:     c.Ui,
		Format: c.format,
		Quiet:  c.quiet,
		Trace:  c.trace,
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
	if datastoreType == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   fmt.Errorf("datastore type is required"),
		})
		return 1
	}

	datastore, ok := datastores.Datastores[datastoreType]
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
			Error:   datastores.ErrMissingServiceName,
		})
		return 1
	}

	if err := datastores.ValidateServiceName(serviceName); err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	appName := arguments["app-name"].StringValue()
	if appName == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   datastores.ErrMissingAppName,
		})
		return 1
	}

	// the service is checked first, because whether a missing app is an error
	// depends on whether this service is linked to it
	if !datastores.Exists(ctx, datastore, serviceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s does not exist", serviceName),
		})
		return 1
	}

	if err := common.VerifyAppName(appName); err != nil {
		// a link to an app that has been deleted still has to be removable, or
		// the service can never be destroyed and its data never recovered: this
		// is the only command that can take the name out of the links file. An
		// app that was never linked stays an error, so a typo is not accepted
		// silently.
		linked := slices.Contains(datastores.LinkedApps(ctx, datastores.LinkedAppsInput{
			Datastore:   datastore,
			ServiceName: serviceName,
		}), appName)

		if !linked || datastores.AppExists(appName) {
			logger.Error(internal.ErrorInput{
				Error: err,
			})
			return 1
		}
	}

	if c.noRestart {
		logger.Info(internal.SkippingRestartMessage)
	}

	if err := internal.UnlinkService(ctx, internal.UnlinkServiceInput{
		AppName:     appName,
		Datastore:   datastore,
		NoRestart:   c.noRestart,
		ServiceName: serviceName,
	}); err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	return 0
}
