package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// LinkCommand is the command for checking if a service is linked to an app
type LinkCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
	// alias is an alternative alias to export the service url as
	alias string
	// database is the database in the service to link the app to
	database string
	// envVar is the full name of the config variable to export the service url as
	envVar string
	// jdbc is whether to also export the jdbc url, username and password
	jdbc bool
	// noRestart is whether to skip restarting the app
	noRestart bool
	// querystring is appended to the service url
	querystring string
	// user is the user in the service to link the app as
	user string
}

// Name returns the name of the command
func (c *LinkCommand) Name() string {
	return "link"
}

// Synopsis returns the synopsis of the command
func (c *LinkCommand) Synopsis() string {
	return "Links a service to an app"
}

// Help returns the help text for the command
func (c *LinkCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *LinkCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Links a redis service named test to the app test-app":  fmt.Sprintf("%s %s redis test test-app", appName, c.Name()),
		"Links it as BLUE_URL instead of the default alias":     fmt.Sprintf("%s %s redis test test-app --alias BLUE", appName, c.Name()),
		"Links it as MB_DB_CONNECTION_URI, with no _URL suffix": fmt.Sprintf("%s %s redis test test-app --env-var MB_DB_CONNECTION_URI", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *LinkCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to link",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to link",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "app-name",
		Description: "the name of the app to link the service to",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *LinkCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *LinkCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *LinkCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.StringVarP(&c.alias, "alias", "a", "", "the prefix of the config variable the service url is set as on the app, which is suffixed with _URL")
	f.StringVarP(&c.envVar, "env-var", "e", "", "the full name of the config variable the service url is set as on the app, used instead of an alias and not suffixed with _URL")
	f.StringVarP(&c.querystring, "querystring", "q", "", "ampersand delimited querystring arguments to append to the service url after a ?")
	f.BoolVarP(&c.jdbc, "jdbc", "j", false, "whether to also set the jdbc url, username and password on the app, as JDBC_ prefixed config variables named after the service url's")
	f.BoolVarP(&c.noRestart, "no-restart", "n", false, "whether to skip restarting the app")
	f.StringVar(&c.database, "database", "", "the database in the service to link the app to, in place of the one named for the service")
	f.StringVar(&c.user, "user", "", "the user in the service to link the app as, in place of the one the service was created with")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *LinkCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--alias":       complete.PredictAnything,
			"--database":    complete.PredictAnything,
			"--env-var":     complete.PredictAnything,
			"--jdbc":        complete.PredictNothing,
			"--no-restart":  complete.PredictNothing,
			"--querystring": complete.PredictAnything,
			"--user":        complete.PredictAnything,
		},
	)
}

// Run runs the command
func (c *LinkCommand) Run(args []string) int {
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

	datastore, ok := lookupDatastore(logger, datastoreType)
	if !ok {
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

	appName := appNameOrCurrent(arguments["app-name"].StringValue())
	if appName == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   service.ErrMissingAppName,
		})
		return 1
	}

	if err := common.VerifyAppName(appName); err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	if !service.Exists(ctx, datastore, serviceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s does not exist", serviceName),
		})
		return 1
	}

	if c.noRestart {
		logger.Info(internal.SkippingRestartMessage)
	}

	if err := internal.LinkService(ctx, internal.LinkServiceInput{
		Alias:       c.alias,
		AppName:     appName,
		Database:    c.database,
		Datastore:   datastore,
		EnvVar:      c.envVar,
		JDBC:        c.jdbc,
		Logger:      logger,
		NoRestart:   c.noRestart,
		Querystring: c.querystring,
		ServiceName: serviceName,
		User:        c.user,
	}); err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	return 0
}

// DocumentsFlag reports whether a flag is described for a datastore
func (c *LinkCommand) DocumentsFlag(name string, data internal.DocumentationData) bool {
	switch name {
	case "jdbc":
		return data.JDBC
	case "database":
		return data.Databases
	case "user":
		return data.Users
	default:
		return true
	}
}
