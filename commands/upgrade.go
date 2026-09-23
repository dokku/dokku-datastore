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

// UpgradeCommand is the command for unexposing a service
type UpgradeCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
	// image is the image to upgrade to
	image string
	// imageVersion is the image version to upgrade to
	imageVersion string
	// restartApps is whether to stop and start linked apps around the upgrade
	restartApps bool
	// configOptions are extra arguments passed to the container create command
	configOptions string
	// customEnv is the environment the service is started with
	customEnv string
	// initialNetwork is the network the container is attached to on create
	initialNetwork string
	// postCreateNetwork are the networks attached after the container is created
	postCreateNetwork []string
	// postStartNetwork are the networks attached after the container is started
	postStartNetwork []string
	// shmSize is the shared memory size for the container
	shmSize string
	// logDriver is the docker logging driver to use for the service container
	logDriver string
	// logOpt are the docker log options to use for the service container
	logOpt []string
	// restart is the docker restart policy to use for the service container
	restart string
}

// Name returns the name of the command
func (c *UpgradeCommand) Name() string {
	return "upgrade"
}

// Synopsis returns the synopsis of the command
func (c *UpgradeCommand) Synopsis() string {
	return "Upgrades a service to a different image version"
}

// Help returns the help text for the command
func (c *UpgradeCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *UpgradeCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Upgrades a redis service named test": fmt.Sprintf("%s %s redis test --image-version 8.8.0", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *UpgradeCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to upgrade",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to upgrade",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *UpgradeCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *UpgradeCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *UpgradeCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.StringVarP(&c.image, "image", "i", "", "the image to upgrade the service to")
	f.StringVarP(&c.imageVersion, "image-version", "I", "", "the image version to upgrade the service to")
	f.BoolVarP(&c.restartApps, "restart-apps", "R", false, "whether to stop and start the linked apps around the upgrade")
	f.StringVarP(&c.configOptions, "config-options", "c", "", "extra arguments to pass to the container create command")
	f.StringVarP(&c.customEnv, "custom-env", "C", "", "semi-colon delimited environment variables to start the service with")
	f.StringVarP(&c.initialNetwork, "initial-network", "N", "", "the initial network to attach the service to")
	f.StringSliceVarP(&c.postCreateNetwork, "post-create-network", "P", []string{}, "a comma-separated list of networks to attach the service container to after service creation")
	f.StringSliceVarP(&c.postStartNetwork, "post-start-network", "S", []string{}, "a comma-separated list of networks to attach the service container to after service start")
	f.StringVarP(&c.shmSize, "shm-size", "s", "", "override shared memory size for the service docker container")
	f.StringVar(&c.logDriver, "log-driver", "", "the docker logging driver to run the service container with (default: the daemon's own)")
	f.StringSliceVar(&c.logOpt, "log-opt", []string{}, "a comma-separated list of key=value docker log options for the service container")
	f.StringVar(&c.restart, "restart", "", "the docker restart policy to run the service container with (default: always)")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *UpgradeCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{},
	)
}

// Run runs the command
func (c *UpgradeCommand) Run(args []string) int {
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

	if err := internal.UpgradeService(ctx, internal.UpgradeServiceInput{
		Datastore:    datastore,
		Image:        c.image,
		ImageVersion: c.imageVersion,
		Logger:       logger,
		RestartApps:  c.restartApps,
		ServiceName:  serviceName,

		// what the service already has is kept unless a flag asked otherwise,
		// so an upgrade that only names an image does not blank the rest
		ConfigOptions:      changedString(flags, "config-options", c.configOptions),
		CustomEnv:          changedString(flags, "custom-env", c.customEnv),
		InitialNetwork:     changedString(flags, "initial-network", c.initialNetwork),
		PostCreateNetworks: changedSlice(flags, "post-create-network", c.postCreateNetwork),
		PostStartNetworks:  changedSlice(flags, "post-start-network", c.postStartNetwork),
		ShmSize:            changedString(flags, "shm-size", c.shmSize),
		LogDriver:          changedString(flags, "log-driver", c.logDriver),
		LogOptions:         changedSlice(flags, "log-opt", c.logOpt),
		RestartPolicy:      changedString(flags, "restart", c.restart),
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	return 0
}
