package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// ImportCommand is the command for unexposing a service
type ImportCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand

	// file is a path on the dokku host to import instead of stdin
	file string
}

// Name returns the name of the command
func (c *ImportCommand) Name() string {
	return "import"
}

// Synopsis returns the synopsis of the command
func (c *ImportCommand) Synopsis() string {
	return "Imports data into a service from stdin or a file"
}

// Help returns the help text for the command
func (c *ImportCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *ImportCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Imports into a redis service named test":               fmt.Sprintf("%s %s redis test", appName, c.Name()),
		"Imports a file on the dokku host into a redis service": fmt.Sprintf("%s %s redis test --file /var/lib/dokku/data/storage/data.dump", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *ImportCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to import into",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to import into",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *ImportCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *ImportCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *ImportCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.StringVarP(&c.file, "file", "f", "", "a file on the dokku host to import instead of reading stdin")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *ImportCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--file": complete.PredictFiles("*"),
		},
	)
}

// Run runs the command
func (c *ImportCommand) Run(args []string) int {
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

	datastore, ok := service.Datastores[datastoreType]
	if !ok {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("datastore type %s is not supported", datastoreType),
		})
		return 1
	}

	if code, unimplemented := requireImplemented(datastore, "import"); unimplemented {
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

	reader, err := importSource(c.file, os.Stdin)
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}
	defer reader.Close() //nolint:errcheck

	if err := datastore.ImportService(ctx, service.ImportServiceInput{
		Datastore:   datastore,
		Reader:      reader,
		ServiceName: serviceName,
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	return 0
}

// importSource is what an import reads from: the file when one is named, and
// stdin otherwise.
//
// The file is opened by this process, so it is a path on the dokku host rather
// than on the machine running ssh. A redirection inside a quoted ssh command is
// never run through a shell there, which is why a dump already on the host has
// to be named instead.
func importSource(path string, stdin *os.File) (io.ReadCloser, error) {
	if path != "" {
		stat, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("unable to read %s on the dokku host: %w", path, err)
		}
		if !stat.Mode().IsRegular() {
			return nil, fmt.Errorf("unable to import %s: not a regular file on the dokku host", path)
		}

		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("unable to read %s on the dokku host: %w", path, err)
		}

		return file, nil
	}

	// refuse to truncate the service when nothing was piped in
	stat, err := stdin.Stat()
	if err != nil {
		return nil, fmt.Errorf("unable to inspect stdin: %w", err)
	}
	if stat.Mode()&os.ModeCharDevice != 0 {
		return nil, errors.New("No data provided on stdin.") //nolint:staticcheck // matches the bash datastore plugins
	}

	return io.NopCloser(stdin), nil
}
