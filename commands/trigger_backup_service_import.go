package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// TriggerBackupServiceImportCommand is the command behind the
// backup-service-import trigger, which dokku's backup:import fires for every
// service it restores. It does nothing unless the service is one of the
// datastore's own, and otherwise makes the service again from what the export
// wrote into the scope directory, as internal.ImportServiceBackup describes. A
// service that already exists is refused.
type TriggerBackupServiceImportCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
}

// Name returns the name of the command
func (c *TriggerBackupServiceImportCommand) Name() string {
	return "trigger-backup-service-import"
}

// Synopsis returns the synopsis of the command
func (c *TriggerBackupServiceImportCommand) Synopsis() string {
	return "Restores a service from the scope directory of a dokku backup"
}

// Help returns the help text for the command
func (c *TriggerBackupServiceImportCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *TriggerBackupServiceImportCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Restores the redis service lollipop": fmt.Sprintf("%s %s redis redis lollipop /tmp/backup/services/redis/lollipop", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *TriggerBackupServiceImportCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore the trigger is running for",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-type",
		Description: "the datastore type of the service being restored, which every other datastore ignores",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "scope-dir",
		Description: "the directory the backup gives the service",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "additional-arguments",
		Description: "arguments dokku passes that this trigger does not use",
		Optional:    true,
		Type:        command.ArgumentList,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *TriggerBackupServiceImportCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *TriggerBackupServiceImportCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *TriggerBackupServiceImportCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *TriggerBackupServiceImportCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

// Run runs the command
func (c *TriggerBackupServiceImportCommand) Run(args []string) int {
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

	if err := internal.ImportServiceBackup(ctx, internal.BackupServiceInput{
		TriggerInput: internal.TriggerInput{
			Datastore: datastore,
			Logger:    logger,
		},
		ServiceType: arguments["service-type"].StringValue(),
		ServiceName: arguments["service-name"].StringValue(),
		ScopeDir:    arguments["scope-dir"].StringValue(),
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	return 0
}
