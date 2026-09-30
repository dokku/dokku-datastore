package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// TriggerCronEntriesCommand is the command that hands dokku the scheduled backups of a datastore
type TriggerCronEntriesCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
}

// Name returns the name of the command
func (c *TriggerCronEntriesCommand) Name() string {
	return "trigger-cron-entries"
}

// Synopsis returns the synopsis of the command
func (c *TriggerCronEntriesCommand) Synopsis() string {
	return "Lists the scheduled backups dokku writes into its crontab"
}

// Help returns the help text for the command
func (c *TriggerCronEntriesCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *TriggerCronEntriesCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Lists the scheduled redis backups":                 fmt.Sprintf("%s %s redis docker-local", appName, c.Name()),
		"Lists the scheduled redis backups as json entries": fmt.Sprintf("%s %s redis docker-local json", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *TriggerCronEntriesCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to list scheduled backups for",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "scheduler",
		Description: "the scheduler dokku is writing cron tasks for, which does not change the output",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "entry-format",
		Description: "the entry format dokku reads, where json has each entry printed as a json object on its own line",
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
func (c *TriggerCronEntriesCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *TriggerCronEntriesCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *TriggerCronEntriesCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *TriggerCronEntriesCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

// Run runs the command
func (c *TriggerCronEntriesCommand) Run(args []string) int {
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

	entryFormat := arguments["entry-format"].StringValue()
	tasks, warnings, err := internal.CronEntriesForTrigger(ctx, internal.CronEntriesInput{
		TriggerInput: internal.TriggerInput{
			Datastore: datastore,
			Logger:    logger,
		},
		EntryFormat: entryFormat,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	// to stderr, since dokku reads every line of stdout as a cron task
	for _, err := range warnings {
		logger.Warn(internal.WarnInput{Warning: err.Error()})
	}

	// dokku reads the output a line at a time, as a json object when it asked
	// for json entries and split on semicolons otherwise
	text := strings.Builder{}
	for _, task := range tasks {
		line := task.Text()
		if entryFormat == internal.CronEntryFormatJSON {
			line, err = task.JSONLine()
			if err != nil {
				logger.Error(internal.ErrorInput{Error: err})
				return 1
			}
		}
		text.WriteString(line + "\n")
	}

	// what dokku asked for is printed whatever format a person asked for
	if entryFormat == internal.CronEntryFormatJSON {
		if err := logger.Write(text.String()); err != nil {
			logger.Error(internal.ErrorInput{Error: err})
			return 1
		}
		return 0
	}

	if err := logger.Document(tasks, text.String()); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	return 0
}
