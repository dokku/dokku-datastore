package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/registry"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// GenerateCommand writes the files a plugin checkout derives from its
// datastore's definition.
//
// The definition is the single source of truth, so what a plugin ships for its
// datastore's own commands is written from it rather than kept by hand where
// the two could drift.
type GenerateCommand struct {
	// Meta is the command meta
	command.Meta
	// GlobalFlagCommand is the global flag command
	GlobalFlagCommand

	// pluginDir is the plugin checkout to write into
	pluginDir string
}

// Name returns the name of the command
func (c *GenerateCommand) Name() string {
	return "generate"
}

// Synopsis returns the synopsis of the command
func (c *GenerateCommand) Synopsis() string {
	return "Writes the files a plugin derives from its datastore definition"
}

// Help returns the help text for the command
func (c *GenerateCommand) Help() string {
	return command.CommandHelp(c)
}

// Examples returns the examples for the command
func (c *GenerateCommand) Examples() map[string]string {
	appName := os.Getenv("CLI_APP_NAME")
	return map[string]string{
		"Writes the readme and subcommands for the mongo plugin": fmt.Sprintf("%s %s mongo --plugin-dir .", appName, c.Name()),
	}
}

// Arguments returns the arguments for the command
func (c *GenerateCommand) Arguments() []command.Argument {
	args := []command.Argument{}
	args = append(args, command.Argument{
		Name:        "datastore-type",
		Description: "the type of datastore to generate for",
		Optional:    false,
		Type:        command.ArgumentString,
	})
	return args
}

// AutocompleteArgs returns the autocomplete arguments for the command
func (c *GenerateCommand) AutocompleteArgs() complete.Predictor {
	return complete.PredictNothing
}

// ParsedArguments parses the arguments for the command
func (c *GenerateCommand) ParsedArguments(args []string) (map[string]command.Argument, error) {
	return internal.ParseArguments(args, c.Arguments())
}

// FlagSet returns the flag set for the command
func (c *GenerateCommand) FlagSet() *flag.FlagSet {
	f := c.Meta.FlagSet(c.Name(), command.FlagSetClient)
	c.GlobalFlags(f)
	f.StringVar(&c.pluginDir, "plugin-dir", ".", "the plugin checkout to write into")
	return f
}

// AutocompleteFlags returns the autocomplete flags for the command
func (c *GenerateCommand) AutocompleteFlags() complete.Flags {
	return command.MergeAutocompleteFlags(
		c.Meta.AutocompleteFlags(command.FlagSetClient),
		c.AutocompleteGlobalFlags(),
		complete.Flags{
			"--plugin-dir": complete.PredictDirs("*"),
		},
	)
}

// Run runs the command
func (c *GenerateCommand) Run(args []string) int {
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

	logger = internal.Ui{Ui: c.Ui, Format: c.format, Quiet: c.quiet, Trace: c.trace}

	arguments, err := c.ParsedArguments(flags.Args())
	if err != nil {
		logger.Error(internal.ErrorInput{
			Message: command.CommandErrorText(c),
			Error:   err,
		})
		return 1
	}

	datastoreType := arguments["datastore-type"].StringValue()
	datastore, ok := service.Datastores[datastoreType]
	if !ok {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("datastore type %s is not supported", datastoreType),
		})
		return 1
	}

	data := internal.NewDocumentationData(internal.DocumentationDataInput{
		Datastore: datastore,
		PluginDir: c.pluginDir,
	})

	written, err := c.writeSubcommands(datastore, data)
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	triggers, err := c.writeTriggers(datastore)
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	written = append(written, triggers...)

	definitions, err := c.writeDefinitions(datastore)
	if err != nil {
		logger.Error(internal.ErrorInput{Error: err})
		return 1
	}

	written = append(written, definitions...)

	for _, path := range written {
		logger.Info(fmt.Sprintf("wrote %s", path))
	}

	return 0
}

// writeTriggers writes one file per dokku trigger the datastore implements.
//
// They go at the plugin root rather than under subcommands, because that is
// where dokku looks for a trigger: it finds one by the name of a file, with no
// manifest to consult.
func (c *GenerateCommand) writeTriggers(datastore *service.Datastore) ([]string, error) {
	names := datastore.TriggerNames()
	if len(names) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(c.pluginDir, 0755); err != nil {
		return nil, fmt.Errorf("unable to create %s: %w", c.pluginDir, err)
	}

	written := []string{}
	for _, name := range names {
		path := filepath.Join(c.pluginDir, name)
		if err := os.WriteFile(path, []byte(internal.PluginTrigger(name)), 0755); err != nil {
			return nil, fmt.Errorf("unable to write %s: %w", path, err)
		}

		// applied explicitly, because WriteFile leaves the mode of a file that
		// already exists alone and dokku will not run a trigger it cannot execute
		if err := os.Chmod(path, 0755); err != nil {
			return nil, fmt.Errorf("unable to set the mode on %s: %w", path, err)
		}

		written = append(written, path)
	}

	return written, nil
}

// writeDefinitions writes the datastore's definitions into the plugin, one
// directory each, exactly as the embedded tree is laid out.
//
// A plugin carrying its own definitions is how a datastore is developed in the
// repository that ships it, and the loader takes a plugin's definitions in place
// of the embedded ones entirely. Which means all of them have to be written: a
// plugin that shipped only the newest of a datastore split by major version
// would leave services pinned to an older one with no definition at all.
//
// The copy is generated output like the readme, so the drift check a plugin
// already runs proves it matches what the binary carries. Diverging deliberately
// means dropping this path from the plugin's generate target, which makes the
// divergence an edit to the Makefile rather than a quiet edit to a file that
// regenerating would silently undo.
func (c *GenerateCommand) writeDefinitions(datastore *service.Datastore) ([]string, error) {
	// checked before anything is written, so a datastore that cannot be shipped
	// says so instead of leaving a tree behind that fails every later command
	for _, found := range datastore.Definitions() {
		if err := registry.Shippable(found); err != nil {
			return nil, fmt.Errorf("%s cannot be shipped by its plugin: %w", found.Name, err)
		}
	}

	written := []string{}

	for _, found := range datastore.Definitions() {
		root := filepath.Join(c.pluginDir, "datastore", found.Name)
		if err := os.MkdirAll(root, 0755); err != nil {
			return nil, fmt.Errorf("unable to create %s: %w", root, err)
		}

		// the compose file and the Dockerfile verbatim: the comments in a
		// definition are where it explains itself, and a definition may use
		// compose keys this binary does not model
		for name, contents := range map[string][]byte{
			"docker-compose.yml": found.Compose,
			"Dockerfile":         found.Dockerfile,
		} {
			path := filepath.Join(root, name)
			if err := writeDefinitionFile(path, contents, 0644); err != nil {
				return nil, err
			}

			written = append(written, path)
		}

		for directory, files := range map[string]map[string][]byte{
			"bin":        found.Scripts,
			"rootfs":     found.Rootfs,
			"privileged": found.Privileged,
		} {
			paths, err := writeDefinitionTree(filepath.Join(root, directory), directory, files)
			if err != nil {
				return nil, err
			}

			written = append(written, paths...)
		}
	}

	// map iteration is unordered, so the log a run prints would otherwise differ
	// between two runs that wrote exactly the same thing
	sort.Strings(written)

	return written, nil
}

// writeDefinitionTree writes one of a definition's file trees, keyed by path
// relative to the directory it belongs in.
func writeDefinitionTree(root string, directory string, files map[string][]byte) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}

	written := []string{}
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, fmt.Errorf("unable to create %s: %w", filepath.Dir(path), err)
		}

		// bin/ and privileged/ hold scripts that are executed directly, so they
		// are written executable whatever they are named. Inside rootfs/ the
		// path decides, the same rule the mount uses.
		mode := os.FileMode(0755)
		if directory == "rootfs" {
			mode = definition.RootfsMode(name)
		}

		if err := writeDefinitionFile(path, contents, mode); err != nil {
			return nil, err
		}

		written = append(written, path)
	}

	return written, nil
}

// writeDefinitionFile writes one file of a definition, setting the mode
// explicitly because WriteFile leaves the mode of a file that already exists
// alone and a hook that cannot run is a create that fails.
func writeDefinitionFile(path string, contents []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, contents, mode); err != nil {
		return fmt.Errorf("unable to write %s: %w", path, err)
	}

	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("unable to set the mode on %s: %w", path, err)
	}

	return nil
}

// writeSubcommands writes one script per command the datastore adds for itself.
func (c *GenerateCommand) writeSubcommands(datastore *service.Datastore, data internal.DocumentationData) ([]string, error) {
	custom := datastore.CustomCommands()
	if len(custom) == 0 {
		return nil, nil
	}

	root := filepath.Join(c.pluginDir, "subcommands")
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("unable to create %s: %w", root, err)
	}

	written := []string{}
	for _, pluginCommand := range internal.CustomCommands(datastore) {
		name := pluginCommand.Name()
		contents, err := internal.PluginSubcommand(name, custom[name], data)
		if err != nil {
			return nil, err
		}

		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(contents), 0755); err != nil {
			return nil, fmt.Errorf("unable to write %s: %w", path, err)
		}

		// applied explicitly, because WriteFile leaves the mode of a file that
		// already exists alone and dokku will not run a script it cannot execute
		if err := os.Chmod(path, 0755); err != nil {
			return nil, fmt.Errorf("unable to set the mode on %s: %w", path, err)
		}

		written = append(written, path)
	}

	return written, nil
}
