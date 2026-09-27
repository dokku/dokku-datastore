package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// ExportCommand is the command for exporting a service's data
type ExportCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand

	// file is a path on the dokku host to export to instead of stdout
	file string
	// force is whether a file already at that path is replaced
	force bool
}

// Name returns the name of the command
func (c *ExportCommand) Name() string {
	return "export"
}

// Synopsis returns the synopsis of the command
func (c *ExportCommand) Synopsis() string {
	return "Exports a service's data to stdout or a file"
}

// Help returns the help text for the command
func (c *ExportCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *ExportCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Exports a redis service named test":                             fmt.Sprintf("%s %s redis test", appName, c.Name()),
		"Exports a redis service named test to a file on the dokku host": fmt.Sprintf("%s %s redis test --file /var/lib/dokku/data/storage/test.rdb", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *ExportCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to export",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to export",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *ExportCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *ExportCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *ExportCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.StringVarP(&c.file, "file", "f", "", "a file on the dokku host to export to instead of writing stdout")
	f.BoolVar(&c.force, "force", false, "replace the file named with --file if it already exists")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *ExportCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--file":  complete.PredictFiles("*"),
			"--force": complete.PredictNothing,
		},
	)
}

// Run runs the command
func (c *ExportCommand) Run(args []string) int {
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

	if code, unimplemented := requireImplemented(datastore, "export"); unimplemented {
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

	// the destination is created before anything is exported, so a path that
	// cannot be written fails without exporting a dump only to throw it away
	if c.force && c.file == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   fmt.Errorf("--force only applies to a file named with --file"),
		})
		return 1
	}

	var writer io.Writer = os.Stdout
	var destination *service.AtomicFile
	if c.file != "" {
		destination, err = exportDestination(c.file, c.force)
		if err != nil {
			logger.Error(internal.ErrorInput{Error: err})
			return 1
		}
		defer destination.Abort()
		writer = destination
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

	if err := datastore.ExportService(ctx, service.ExportServiceInput{
		Datastore:   datastore,
		ServiceName: serviceName,
		Writer:      writer,
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	if destination != nil {
		if err := destination.Commit(); err != nil {
			// a file created at the path while the export ran is left alone
			// just as one there before it would have been
			if errors.Is(err, fs.ErrExist) {
				err = errExportFileExists(c.file)
			} else {
				err = fmt.Errorf("unable to write %s on the dokku host: %w", c.file, err)
			}
			logger.Error(internal.ErrorInput{Error: err})
			return 1
		}
	}

	return 0
}

// exportDestination is the file an export is written to when one is named. The
// dump is streamed into a temporary file beside it, which only moves to the path
// once the export has succeeded, so a failed export never leaves a truncated
// dump behind. A file already at the path is refused unless force is set, and
// is then only replaced once the export has succeeded.
//
// The file is written by this process, so it is a path on the dokku host rather
// than on the machine running ssh. A redirection inside a quoted ssh command is
// never run through a shell there, which is why the file has to be named instead.
//
// The temporary file is created here, before the export runs, since creating it
// is the one check that answers whether the path can be written: it needs the
// same access to the directory the rename does.
func exportDestination(path string, force bool) (*service.AtomicFile, error) {
	target := path

	// a symlink is written through, the way a redirection would, rather than
	// replaced by the rename
	if stat, err := os.Lstat(path); err == nil && stat.Mode()&os.ModeSymlink != 0 {
		target, err = filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("unable to write %s on the dokku host: %w", path, err)
		}
	}

	stat, err := os.Stat(target)
	switch {
	case err == nil && !stat.Mode().IsRegular():
		return nil, fmt.Errorf("unable to export to %s: not a regular file on the dokku host", path)
	case err == nil && !force:
		return nil, errExportFileExists(path)
	case err != nil && !os.IsNotExist(err):
		return nil, fmt.Errorf("unable to write %s on the dokku host: %w", path, err)
	}

	file, err := service.CreateAtomicFile(target, service.PrivateFileMode, force)
	if err != nil {
		return nil, fmt.Errorf("unable to write %s on the dokku host: %w", path, err)
	}

	return file, nil
}

// errExportFileExists refuses to overwrite a file an export was not told it may
// replace.
func errExportFileExists(path string) error {
	return fmt.Errorf("unable to export to %s: the file already exists on the dokku host, pass --force to replace it", path)
}
