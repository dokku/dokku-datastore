package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"
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
	// infoFlags holds the flag that selects each key, built from
	// internal.InfoKeys so that the two cannot name different things
	infoFlags map[string]*bool
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
		"Gets information about every redis service":        fmt.Sprintf("%s %s redis", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *InfoCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to get information about",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	args = append(args, command.Argument{
		Name:        "service-name",
		Description: "the name of the service to get information about, or empty for every service",
		Optional:    true,
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
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command.
//
// The per-key flags are generated rather than declared, so that every key the
// command answers has a flag selecting it. Declaring them by hand is how
// config-options came to be reported without being selectable.
func (c *InfoCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	c.infoFlags = map[string]*bool{}
	for _, key := range internal.InfoKeys {
		c.infoFlags[key.Name] = f.Bool(key.Name, false, key.Description)
	}
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *InfoCommand) AutocompleteFlags() complete.Flags {
	keys := complete.Flags{}
	for _, key := range internal.InfoKeys {
		keys[key.Name] = complete.PredictNothing
	}

	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		keys,
	)
}

// selectedInfoFlag returns the single key flag the command was given, and an
// error when it was given more than one. Printing one value on its own is the
// only thing a flag can mean, so two of them have no answer.
func (c *InfoCommand) selectedInfoFlag(datastoreType string) (string, error) {
	selected := []string{}
	for _, key := range internal.InfoKeys {
		if value, ok := c.infoFlags[key.Name]; ok && *value {
			selected = append(selected, fmt.Sprintf("--%s", key.Name))
		}
	}

	if len(selected) > 1 {
		return "", fmt.Errorf("%s:info command allows only a single flag", datastoreType)
	}

	if len(selected) == 0 {
		return "", nil
	}

	return selected[0], nil
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

	infoFlag, err := c.selectedInfoFlag(datastoreType)
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	serviceName := arguments["service-name"].StringValue()
	serviceNames := []string{serviceName}
	if serviceName == "" {
		// naming no service reports on every one of them, the way a core plugin
		// report with no app reports every app
		serviceNames, err = internal.ListServices(ctx, internal.ListServicesInput{
			Datastore: datastore,
			Trace:     c.trace,
		})
		if err != nil {
			logger.Error(internal.ErrorInput{Error: err})
			return 1
		}

		if len(serviceNames) == 0 {
			logger.Warn(internal.WarnInput{Warning: fmt.Sprintf("There are no %s services", datastoreType)})
			return 0
		}
	}

	for _, name := range serviceNames {
		if code := c.infoForService(ctx, logger, datastore, datastoreType, name, infoFlag); code != 0 {
			return code
		}
	}

	return 0
}

// infoForService reports on one service
func (c *InfoCommand) infoForService(ctx context.Context, logger internal.Ui, datastore *service.Datastore, datastoreType string, serviceName string, infoFlag string) int {
	if err := service.ValidateServiceName(serviceName); err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	// a service runs the definition it was created with. This command works on a
	// service whose definition is missing, because otherwise there would be no
	// way to look at one or to get rid of it.
	datastore, unresolved := datastore.ForService(serviceName)
	if unresolved != nil {
		logger.Warn(internal.WarnInput{Warning: unresolved.Error()})
	}

	if !service.Exists(ctx, datastore, serviceName) {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("service %s does not exist", serviceName),
		})
		return 1
	}

	info := internal.Info(ctx, internal.InfoInput{
		Datastore:   datastore,
		ServiceName: serviceName,
	})

	flags := map[string]string{}
	for key, value := range info {
		flags[fmt.Sprintf("--%s", key)] = value
	}

	flagKeys := []string{}
	for _, key := range internal.InfoKeyNames() {
		flagKeys = append(flagKeys, fmt.Sprintf("--%s", key))
	}

	// both formats go through the helper, so that asking for a single value in a
	// format that cannot carry one is refused rather than quietly answered with
	// everything
	err := common.ReportSingleApp(common.ReportSingleAppInput{
		ReportType:              datastoreType,
		AppName:                 serviceName,
		InfoFlag:                infoFlag,
		InfoFlags:               flags,
		InfoFlagKeys:            flagKeys,
		Format:                  c.ReportFormat(),
		TrimPrefix:              false,
		UppercaseFirstCharacter: true,
	})
	if err != nil {
		logger.Error(internal.ErrorInput{
			Error: err,
		})
		return 1
	}

	return 0
}
