package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// BackupLogsCommand is the command for showing the log of a service's scheduled
// backups
type BackupLogsCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand

	// tail is the number of lines to display, and its presence is what asks for
	// the log to be followed, the same as on the logs command
	tail int
}

// Name returns the name of the command
func (c *BackupLogsCommand) Name() string {
	return "backup-logs"
}

// Synopsis returns the synopsis of the command
func (c *BackupLogsCommand) Synopsis() string {
	return "Shows the log of a service's scheduled backups"
}

// Help returns the help text for the command
func (c *BackupLogsCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *BackupLogsCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Shows the backup log of a redis service named test":                          fmt.Sprintf("%s %s redis test", appName, c.Name()),
		"Follows the backup log of a redis service named test":                        fmt.Sprintf("%s %s redis test --tail", appName, c.Name()),
		"Follows the backup log of a redis service named test from its last 50 lines": fmt.Sprintf("%s %s redis test --tail=50", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *BackupLogsCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to show the backup log of",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to show the backup log of",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *BackupLogsCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *BackupLogsCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *BackupLogsCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.IntVarP(&c.tail, "tail", "t", defaultLogLines, "follow the log, optionally showing this many lines")

	// makes the value optional, the same as on the logs command: --tail follows
	// with the default number of lines and --tail=50 says how many
	f.Lookup("tail").NoOptDefVal = strconv.Itoa(defaultLogLines)

	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *BackupLogsCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

// Run runs the command
func (c *BackupLogsCommand) Run(args []string) int {
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

	if code, unimplemented := requireImplemented(datastore, "backup-logs"); unimplemented {
		return code
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

	// the log outlives the schedule, so a service that is no longer scheduled
	// still has the output of the backups it ran
	if err := internal.BackupLogs(ctx, internal.BackupLogsInput{
		Datastore:   datastore,
		ServiceName: serviceName,
		Num:         c.tail,
		Tail:        flags.Changed("tail"),
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	return 0
}
