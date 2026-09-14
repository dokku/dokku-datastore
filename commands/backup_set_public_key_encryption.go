package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/datastores"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// BackupSetPublicKeyEncryptionCommand is the command for unexposing a service
type BackupSetPublicKeyEncryptionCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
}

// Name returns the name of the command
func (c *BackupSetPublicKeyEncryptionCommand) Name() string {
	return "backup-set-public-key-encryption"
}

// Synopsis returns the synopsis of the command
func (c *BackupSetPublicKeyEncryptionCommand) Synopsis() string {
	return "Encrypts future backups of a service with a gpg public key"
}

// Help returns the help text for the command
func (c *BackupSetPublicKeyEncryptionCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *BackupSetPublicKeyEncryptionCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Encrypts future backups of a redis service named test": fmt.Sprintf("%s %s redis test KEYID", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *BackupSetPublicKeyEncryptionCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to set public key backup encryption for",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to set public key backup encryption for",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "public-key-id",
		Description: "a gpg public key id or fingerprint",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *BackupSetPublicKeyEncryptionCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *BackupSetPublicKeyEncryptionCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *BackupSetPublicKeyEncryptionCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *BackupSetPublicKeyEncryptionCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

// Run runs the command
func (c *BackupSetPublicKeyEncryptionCommand) Run(args []string) int {
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

	if !datastores.Exists(ctx, datastore, serviceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s does not exist", serviceName),
		})
		return 1
	}

	publicKeyID := arguments["public-key-id"].StringValue()
	if publicKeyID == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   errors.New("Please specify a valid GPG Public Key ID (or fingerprint)"), //nolint:staticcheck // matches the bash datastore plugins
		})
		return 1
	}

	if err := internal.SetBackupPublicKeyEncryption(ctx, datastore, serviceName, publicKeyID); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	logger.Header2(fmt.Sprintf("Backup public key encryption set for %s", serviceName)) //nolint:errcheck
	return 0
}
