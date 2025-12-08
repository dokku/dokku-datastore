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

// InfoCommand is the command for getting information about a service
type InfoCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand
	// configDir is the configuration directory for the service
	configDir bool
	// dataDir is the data directory for the service
	dataDir bool
	// dsn is the data source name for the service
	dsn bool
	// exposedPorts is the exposed ports for the service
	exposedPorts bool
	// id is the ID for the service
	id bool
	// internalIp is the internal IP for the service
	internalIp bool
	// initialNetwork is the initial network for the service
	initialNetwork bool
	// links is the links for the service
	links bool
	// postCreateNetwork is the post create network for the service
	postCreateNetwork bool
	// postStartNetwork is the post start network for the service
	postStartNetwork bool
	// serviceRoot is the service root for the service
	serviceRoot bool
	// status is the status for the service
	status bool
	// version is the version for the service
	version bool
}

// Name returns the name of the command
func (c *InfoCommand) Name() string {
	return "info"
}

// Synopsis returns the synopsis of the command
func (c *InfoCommand) Synopsis() string {
	return "Gets information about a service"
}

// Help returns the help text for the command
func (c *InfoCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *InfoCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Gets information about a redis service named test": fmt.Sprintf("%s %s redis test", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *InfoCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to enter",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to enter",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *InfoCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictSet("redis")
}

// ParsedArguments parses the arguments for the command
func (c *InfoCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return command.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *InfoCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.BoolVar(&c.configDir, "config-dir", false, "the configuration directory for the service")
	f.BoolVar(&c.dataDir, "data-dir", false, "the data directory for the service")
	f.BoolVar(&c.dsn, "dsn", false, "the data source name for the service")
	f.BoolVar(&c.exposedPorts, "exposed-ports", false, "the exposed ports for the service")
	f.BoolVar(&c.id, "id", false, "the ID for the service")
	f.BoolVar(&c.internalIp, "internal-ip", false, "the internal IP for the service")
	f.BoolVar(&c.initialNetwork, "initial-network", false, "the initial network for the service")
	f.BoolVar(&c.links, "links", false, "the links for the service")
	f.BoolVar(&c.postCreateNetwork, "post-create-network", false, "the post create network for the service")
	f.BoolVar(&c.postStartNetwork, "post-start-network", false, "the post start network for the service")
	f.BoolVar(&c.serviceRoot, "service-root", false, "the service root for the service")
	f.BoolVar(&c.status, "status", false, "the status for the service")
	f.BoolVar(&c.version, "version", false, "the version for the service")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *InfoCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"config-dir":          complete.PredictNothing,
			"data-dir":            complete.PredictNothing,
			"dsn":                 complete.PredictNothing,
			"exposed-ports":       complete.PredictNothing,
			"id":                  complete.PredictNothing,
			"internal-ip":         complete.PredictNothing,
			"initial-network":     complete.PredictNothing,
			"links":               complete.PredictNothing,
			"post-create-network": complete.PredictNothing,
			"post-start-network":  complete.PredictNothing,
			"service-root":        complete.PredictNothing,
			"status":              complete.PredictNothing,
			"version":             complete.PredictNothing,
		},
	)
}

// Run runs the command
func (c *InfoCommand) Run(args []string) int {
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

	logger := internal.Ui{
		Ui:     c.Ui,
		Format: c.format,
		Quiet:  c.quiet,
		Trace:  c.trace,
	}

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
			Error:   fmt.Errorf("service name is required"),
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

	infoFlag := ""
	if c.configDir {
		infoFlag = "--config-dir"
	}
	if c.dataDir {
		infoFlag = "--data-dir"
	}
	if c.dsn {
		infoFlag = "--dsn"
	}
	if c.exposedPorts {
		infoFlag = "--exposed-ports"
	}
	if c.id {
		infoFlag = "--id"
	}
	if c.internalIp {
		infoFlag = "--internal-ip"
	}
	if c.initialNetwork {
		infoFlag = "--initial-network"
	}
	if c.links {
		infoFlag = "--links"
	}
	if c.postCreateNetwork {
		infoFlag = "--post-create-network"
	}
	if c.postStartNetwork {
		infoFlag = "--post-start-network"
	}
	if c.serviceRoot {
		infoFlag = "--service-root"
	}
	if c.status {
		infoFlag = "--status"
	}
	if c.version {
		infoFlag = "--version"
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

		}
	} else {
		flagKeys := []string{}

		flags := map[string]string{}
		for key, value := range info {
			flagKey := fmt.Sprintf("--%s", key)
			flagKeys = append(flagKeys, flagKey)
			flags[flagKey] = value
		}
		trimPrefix := false
		uppercaseFirstCharacter := true
		if c.format == "text" {
			c.format = "stdout"
		}
		err = common.ReportSingleApp(datastoreType, serviceName, infoFlag, flags, flagKeys, c.format, trimPrefix, uppercaseFirstCharacter)
		if err != nil {
			logger.Error(internal.ErrorInput{
				Error: err,
			})
			return 1
		}
	}
	return 0
}
