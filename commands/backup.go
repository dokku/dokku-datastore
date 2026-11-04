package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// BackupCommand is the command for unexposing a service
type BackupCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
	// useIAM uses the iam profile attached to the server instead of stored credentials
	useIAM bool

	// scheduleID is the schedule whose settings this backup runs with
	scheduleID string

	// allDatabases backs up every database in the service rather than only
	// the one named for it, when the flag is given
	allDatabases bool
}

// Name returns the name of the command
func (c *BackupCommand) Name() string {
	return "backup"
}

// Synopsis returns the synopsis of the command
func (c *BackupCommand) Synopsis() string {
	return "Backs a service up to an s3 bucket"
}

// Help returns the help text for the command
func (c *BackupCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *BackupCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Backs a redis service named test up to a bucket":                      fmt.Sprintf("%s %s redis test my-bucket", appName, c.Name()),
		"Backs every database in a postgres service named test up to a bucket": fmt.Sprintf("%s %s postgres test my-bucket --all-databases", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *BackupCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to back up",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to back up",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "bucket-name",
		Description: "the name of the s3 bucket to upload the backup to",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *BackupCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *BackupCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *BackupCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.BoolVarP(&c.useIAM, "use-iam", "u", false, "use the IAM profile associated with the current server")
	f.StringVar(&c.scheduleID, "id", "", "run with the settings of this schedule")
	f.BoolVar(&c.allDatabases, "all-databases", false, "back up every database in the service rather than only the one named for it, in place of the backup-all-databases property")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *BackupCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--id":            complete.PredictNothing,
			"--all-databases": complete.PredictNothing,
		},
	)
}

// Run runs the command
func (c *BackupCommand) Run(args []string) int {
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

	if code, unimplemented := requireImplemented(datastore, "backup"); unimplemented {
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
		return c.finish(ctx, &logger, datastore, serviceName, "", 1)
	}

	if !service.Exists(ctx, datastore, serviceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s does not exist", serviceName),
		})
		return 1
	}

	if code, refused := refuseAllDatabases(logger, datastore, c.allDatabases); refused {
		return c.finish(ctx, &logger, datastore, serviceName, arguments["bucket-name"].StringValue(), code)
	}

	// only a flag that was given decides, so that --all-databases=false backs
	// up the service's own database on a service set to back up every one
	var allDatabases *bool
	if flags.Changed("all-databases") {
		allDatabases = &c.allDatabases
	}

	bucketName := arguments["bucket-name"].StringValue()
	if bucketName == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   errors.New("Please specify an aws bucket for the backup"), //nolint:staticcheck // matches the bash datastore plugins
		})
		return c.finish(ctx, &logger, datastore, serviceName, bucketName, 1)
	}

	if err := internal.ValidateBucketName(bucketName); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return c.finish(ctx, &logger, datastore, serviceName, bucketName, 1)
	}

	// the start and end of each run are marked with the time, since a scheduled
	// backup appends everything it prints to the service's backup log
	message := fmt.Sprintf("Backing up %s to %s at %s", serviceName, bucketName, internal.BackupTimestamp(time.Now()))
	if c.scheduleID != "" {
		message = fmt.Sprintf("Backing up %s (%s) to %s at %s", serviceName, c.scheduleID, bucketName, internal.BackupTimestamp(time.Now()))
	}
	logger.Header2(message) //nolint:errcheck

	if err := internal.Backup(ctx, internal.BackupInput{
		BucketName:   bucketName,
		Datastore:    datastore,
		ServiceName:  serviceName,
		UseIAM:       c.useIAM,
		ScheduleID:   c.scheduleID,
		AllDatabases: allDatabases,
	}); err != nil {
		logger.Info(fmt.Sprintf("Backup of %s failed at %s", serviceName, internal.BackupTimestamp(time.Now())))
		logger.Error(internal.ErrorInput{Error: err})
		return c.finish(ctx, &logger, datastore, serviceName, bucketName, 1)
	}

	logger.Info(fmt.Sprintf("Backup of %s finished at %s", serviceName, internal.BackupTimestamp(time.Now())))
	return c.finish(ctx, &logger, datastore, serviceName, bucketName, 0)
}

// finish fires the post-backup trigger for a backup of an existing service that
// ended with the given exit code, and returns that code. The trigger is fired
// even when the backup was interrupted, and a plugin that fails to hear about
// the backup is warned about rather than changing whether the backup worked.
func (c *BackupCommand) finish(ctx context.Context, logger *internal.Ui, datastore *service.Datastore, serviceName string, bucketName string, code int) int {
	status := internal.BackupStatusSuccess
	if code != 0 {
		status = internal.BackupStatusFailure
	}

	if err := internal.CallPostBackup(context.WithoutCancel(ctx), internal.PostBackupInput{
		BucketName:  bucketName,
		Datastore:   datastore,
		ServiceName: serviceName,
		Status:      status,
	}); err != nil {
		logger.Warn(internal.WarnInput{Warning: err.Error()})
	}

	return code
}

// DocumentsFlag reports whether a flag is described for a datastore
func (c *BackupCommand) DocumentsFlag(name string, data internal.DocumentationData) bool {
	return documentsAllDatabases(name, data)
}
