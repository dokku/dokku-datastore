package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/datastores"

	"github.com/dokku/dokku/plugins/common"
	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// CreateCommand is the command for creating a new datastore service
type CreateCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
	// configOptions is the configuration options to use for the service
	configOptions string
	// customEnv is the custom environment variables to use for the service
	customEnv string
	// image is the image to use for the service
	image string
	// imageVersion is the image version to use for the service
	imageVersion string
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
func (c *CreateCommand) Name() string {
	return "create"
}

// Synopsis returns the synopsis of the command
func (c *CreateCommand) Synopsis() string {
	return "Creates a new datastore service"
}

// Help returns the help text for the command
func (c *CreateCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *CreateCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Creates a new redis service named test": fmt.Sprintf("%s %s redis test", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *CreateCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to create",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to create",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *CreateCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis", "postgres", "mysql", "mongodb", "elasticsearch")
}

// ParsedArguments parses the arguments for the command
func (c *CreateCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *CreateCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.StringVar(&c.configOptions, "config-options", "", "extra arguments to pass to the container create command")
	f.StringVar(&c.customEnv, "custom-env", "", "semi-colon delimited environment variables to start the service with")
	f.StringVar(&c.image, "image", "", "the image name to start the service with")
	f.StringVar(&c.imageVersion, "image-version", "", "the image version to start the service with")
	f.IntVar(&c.memory, "memory", 0, "container memory limit in megabytes (default: unlimited)")
	f.StringVar(&c.initialNetwork, "initial-network", "", "the initial network to attach the service to")
	f.StringVar(&c.password, "password", "", "override the user-level service password")
	f.StringSliceVar(&c.postCreateNetwork, "post-create-network", []string{}, "a comma-separated list of networks to attach the service container to after service creation")
	f.StringVar(&c.rootPassword, "root-password", "", "override the root-level service password")
	f.StringSliceVar(&c.postStartNetwork, "post-start-network", []string{}, "a comma-separated list of networks to attach the service container to after service start")
	f.StringVar(&c.shmSize, "shm-size", "", "override shared memory size for $PLUGIN_COMMAND_PREFIX docker container")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *CreateCommand) AutocompleteFlags() complete.Flags {
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
func (c *CreateCommand) Run(args []string) int {
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
			Error:   fmt.Errorf("service name is required"),
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

	updatedFlags, err := internal.UpdateFlagFromEnv(internal.UpdateFlagFromEnvInput{
		ConfigOptions: c.configOptions,
		CustomEnv:     c.customEnv,
		Datastore:     datastore,
		Image:         c.image,
		ImageVersion:  c.imageVersion,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	err = internal.CreateService(ctx, internal.CreateServiceInput{
		ConfigOptions:      updatedFlags.ConfigOptions,
		CustomEnv:          updatedFlags.CustomEnv,
		Datastore:          datastore,
		Image:              updatedFlags.Image,
		ImageVersion:       updatedFlags.ImageVersion,
		InitialNetwork:     c.initialNetwork,
		Memory:             c.memory,
		Password:           c.password,
		PostCreateNetworks: c.postCreateNetwork,
		PostStartNetworks:  c.postStartNetwork,
		ServiceName:        serviceName,
		ShmSize:            c.shmSize,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	serviceProperties := datastore.Properties()
	waitPort := serviceProperties.WaitPort
	initialNetwork := datastores.InitialNetwork(datastore, serviceName)
	networkAlias := datastores.DNSHostname(datastore, serviceName)
	containerName := datastores.ContainerName(datastore, serviceName)

	linkContainerDockerArgs := []string{
		"container",
		"run",
		"--rm",
		"--link=" + containerName + ":" + networkAlias,
	}

	if initialNetwork != "" {
		linkContainerDockerArgs = append(linkContainerDockerArgs, "--network="+initialNetwork)
	}

	linkContainerDockerArgs = append(linkContainerDockerArgs, datastores.PluginWaitImage)
	linkContainerDockerArgs = append(linkContainerDockerArgs, "-c", fmt.Sprintf("%s:%d", networkAlias, waitPort))

	logger.Header1(fmt.Sprintf("Waiting for %s container to be ready", serviceName)) //nolint:errcheck
	_, err = datastores.CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    linkContainerDockerArgs,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		containerID := datastores.LiveContainerID(ctx, datastores.LiveContainerIDInput{
			Datastore:   datastore,
			ServiceName: serviceName,
		})
		logger.Header1(fmt.Sprintf("Start of %s container output", serviceName)) //nolint:errcheck
		common.LogVerboseQuietContainerLogs(containerID)
		logger.Header1(fmt.Sprintf("End of %s container output", serviceName)) //nolint:errcheck
		return 1
	}

	info := datastores.Info(ctx, datastores.InfoInput{
		Datastore:   datastore,
		ServiceName: serviceName,
	})
	if c.format == "json" {
		if err := json.NewEncoder(os.Stdout).Encode(info); err != nil {
			logger.Error(internal.ErrorInput{
				Error: err,
			})
			return 1
		}
	} else {
		logger.Header2(fmt.Sprintf("%s container created: %s", datastore.Title(), serviceName)) //nolint:errcheck
		flagKeys := []string{}

		flags := map[string]string{}
		for key, value := range info {
			flagKey := fmt.Sprintf("--%s", key)
			flagKeys = append(flagKeys, flagKey)
			flags[flagKey] = value
		}
		trimPrefix := false
		uppercaseFirstCharacter := true
		err = common.ReportSingleApp(datastoreType, serviceName, "", flags, flagKeys, c.format, trimPrefix, uppercaseFirstCharacter)
		if err != nil {
			logger.Error(internal.ErrorInput{
				Error: err,
			})
			return 1
		}
	}
	return 0
}
