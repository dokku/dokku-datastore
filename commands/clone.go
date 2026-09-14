package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/datastores"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// CloneCommand is the command for creating a new datastore service
type CloneCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
	// configOptions is the configuration options to use for the service
	configOptions string
	// customEnv is the custom environment variables to use for the service
	customEnv string
	// image is the image to use for the service
	// imageVersion is the image version to use for the service
	// memory is the memory limit to use for the service
	memory int
	// initialNetwork is the initial network to use for the service
	initialNetwork string
	// password is the password to use for the service
	password string
	// postCreateNetwork is the networks to attach the service container to after service creation
	postCreateNetwork []string
	// rootPassword is the root password to use for the service
	rootPassword string
	// postStartNetwork is the networks to attach the service container to after service start
	postStartNetwork []string
	// shmSize is the shared memory size to use for the service
	shmSize string
}

// Name returns the name of the command
func (c *CloneCommand) Name() string {
	return "clone"
}

// Synopsis returns the synopsis of the command
func (c *CloneCommand) Synopsis() string {
	return "Clones a service onto a new one"
}

// Help returns the help text for the command
func (c *CloneCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *CloneCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Clones a redis service named test onto a service named copy": fmt.Sprintf("%s %s redis test copy", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *CloneCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to clone",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to copy from",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "new-service-name",
		Description: "the name of the service to create",
		Optional:    true,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *CloneCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis", "postgres", "mysql", "mongodb", "elasticsearch")
}

// ParsedArguments parses the arguments for the command
func (c *CloneCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *CloneCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.StringVar(&c.configOptions, "config-options", "", "extra arguments to pass to the container create command")
	f.StringVar(&c.customEnv, "custom-env", "", "semi-colon delimited environment variables to start the service with")
	f.IntVar(&c.memory, "memory", 0, "container memory limit in megabytes (default: unlimited)")
	f.StringVar(&c.initialNetwork, "initial-network", "", "the initial network to attach the service to")
	f.StringVar(&c.password, "password", "", "override the user-level service password")
	f.StringSliceVar(&c.postCreateNetwork, "post-create-network", []string{}, "a comma-separated list of networks to attach the service container to after service creation")
	f.StringVar(&c.rootPassword, "root-password", "", "override the root-level service password")
	f.StringSliceVar(&c.postStartNetwork, "post-start-network", []string{}, "a comma-separated list of networks to attach the service container to after service start")
	f.StringVar(&c.shmSize, "shm-size", "", "override shared memory size for the service docker container")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *CloneCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--config-options":      complete.PredictAnything,
			"--custom-env":          complete.PredictAnything,
			"--image":               complete.PredictAnything,
			"--image-version":       complete.PredictAnything,
			"--memory":              complete.PredictAnything,
			"--initial-network":     complete.PredictAnything,
			"--password":            complete.PredictAnything,
			"--post-create-network": complete.PredictAnything,
			"--root-password":       complete.PredictAnything,
			"--post-start-network":  complete.PredictAnything,
			"--shm-size":            complete.PredictAnything,
		},
	)
}

// Run runs the command
func (c *CloneCommand) Run(args []string) int {
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

	serviceName := arguments["service-name"].StringValue()
	if serviceName == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   datastores.ErrMissingServiceName,
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

	newServiceName := arguments["new-service-name"].StringValue()
	if newServiceName == "" {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   errors.New("Please specify a name for the new service"), //nolint:staticcheck // matches the bash datastore plugins
		})
		return 1
	}

	if err := datastores.ValidateServiceName(newServiceName); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	if !datastores.Exists(ctx, datastore, serviceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s does not exist", serviceName),
		})
		return 1
	}

	if datastores.Exists(ctx, datastore, newServiceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("Invalid service name %s. Verify the service name is not already in use.", newServiceName), //nolint:staticcheck // matches the bash datastore plugins
		})
		return 1
	}

	status := strings.ToLower(datastores.Status(ctx, datastores.StatusInput{
		Datastore:   datastore,
		ServiceName: serviceName,
	}))
	if status != "running" {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("Service %s container is not running", serviceName), //nolint:staticcheck // matches the bash datastore plugins
		})
		return 1
	}

	updatedFlags, err := internal.UpdateFlagFromEnv(internal.UpdateFlagFromEnvInput{
		ConfigOptions: c.configOptions,
		CustomEnv:     c.customEnv,
		Datastore:     datastore,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	if err := internal.CloneService(ctx, internal.CloneServiceInput{
		ConfigOptions:      updatedFlags.ConfigOptions,
		CustomEnv:          updatedFlags.CustomEnv,
		Datastore:          datastore,
		InitialNetwork:     c.initialNetwork,
		Logger:             logger,
		Memory:             c.memory,
		NewServiceName:     newServiceName,
		Password:           c.password,
		PostCreateNetworks: c.postCreateNetwork,
		PostStartNetworks:  c.postStartNetwork,
		ServiceName:        serviceName,
		ShmSize:            c.shmSize,
	}); err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	return 0
}
