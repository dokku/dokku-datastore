package commands

import (
	"context"
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

// MountCommand is the command for mounting a host path or docker volume into a
// service
type MountCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand

	// replace swaps the service's entire set of mounts for the ones given
	replace bool
	// volumeReadonly mounts the one mount read only
	volumeReadonly bool
	// volumeOptions are docker mount options for the one mount
	volumeOptions string
	// volumeSubpath is recorded against the one mount
	volumeSubpath string
	// volumeChown is recorded against the one mount
	volumeChown string
}

// Name returns the name of the command
func (c *MountCommand) Name() string {
	return "mount"
}

// Synopsis returns the synopsis of the command
func (c *MountCommand) Synopsis() string {
	return "Mounts a host path or docker volume into a service"
}

// Help returns the help text for the command
func (c *MountCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *MountCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Mounts a host directory read only into a redis service named test": fmt.Sprintf("%s %s redis test /var/lib/dokku/data/storage/test:/opt/extra:ro", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *MountCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to mount into",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to mount into",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "mounts",
		Description: "the mounts, as <source>:<container-dir>[:<options>]; one, or the whole set with --replace",
		Optional:    true,
		Type:        command.ArgumentList,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *MountCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *MountCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *MountCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.BoolVar(&c.replace, "replace", false, "replace the service's entire set of mounts with the ones given")
	f.BoolVar(&c.volumeReadonly, "volume-readonly", false, "mount the volume read only; not valid with --replace")
	f.StringVar(&c.volumeOptions, "volume-options", "", "comma-separated docker mount options, such as z or nocopy; not valid with --replace")
	f.StringVar(&c.volumeSubpath, "volume-subpath", "", "a subpath within the source, recorded but not applied; not valid with --replace")
	f.StringVar(&c.volumeChown, "volume-chown", "", "a chown option, recorded but not applied; not valid with --replace")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *MountCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--replace":         complete.PredictNothing,
			"--volume-readonly": complete.PredictNothing,
			"--volume-options":  complete.PredictAnything,
			"--volume-subpath":  complete.PredictAnything,
			"--volume-chown":    complete.PredictSet("herokuish", "heroku", "paketo", "root", "false"),
		},
	)
}

// Run runs the command
func (c *MountCommand) Run(args []string) int {
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

	datastore, serviceName, ok := resolveExistingService(ctx, c, logger, arguments)
	if !ok {
		return 1
	}

	message, err := internal.MountService(internal.MountServiceInput{
		Chown:         c.volumeChown,
		Datastore:     datastore,
		Readonly:      c.volumeReadonly,
		Replace:       c.replace,
		ServiceName:   serviceName,
		Specs:         arguments["mounts"].ListValue(),
		Subpath:       c.volumeSubpath,
		VolumeOptions: c.volumeOptions,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	logger.Info(message)
	logger.Info(mountsRebuildNote(datastore))
	return 0
}

// mountsRebuildNote says when a change to a service's mounts takes effect,
// which is not when the command that made it returns
func mountsRebuildNote(datastore *service.Datastore) string {
	commandPrefix := datastore.Properties().CommandPrefix
	return fmt.Sprintf("Mounts reach the container the next time one is built; on a running service use %s:stop and then %s:start", commandPrefix, commandPrefix)
}

// resolveExistingService reads the datastore type and service name a command
// was given and settles the datastore the service runs, reporting whatever
// stops it from doing so
func resolveExistingService(ctx context.Context, c command.NamedCommand, logger internal.Ui, arguments map[string]command.Argument) (*service.Datastore, string, bool) {
	datastoreType := arguments["datastore-type"].StringValue()
	if datastoreType == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   fmt.Errorf("datastore type is required"),
		})
		return nil, "", false
	}

	datastore, ok := service.Datastores[datastoreType]
	if !ok {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("datastore type %s is not supported", datastoreType),
		})
		return nil, "", false
	}

	serviceName := arguments["service-name"].StringValue()
	if serviceName == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   service.ErrMissingServiceName,
		})
		return nil, "", false
	}

	if err := service.ValidateServiceName(serviceName); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return nil, "", false
	}

	// a service runs the definition it was created with, which for a datastore
	// split by major version is not always the newest one, and whose volumes
	// are the ones a mount must not land on
	datastore, unresolved := datastore.ForService(serviceName)
	if unresolved != nil {
		logger.Error(internal.ErrorInput{Error: unresolved})
		return nil, "", false
	}

	if !service.Exists(ctx, datastore, serviceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s does not exist", serviceName),
		})
		return nil, "", false
	}

	return datastore, serviceName, true
}
