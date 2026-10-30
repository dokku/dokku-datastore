// Package sdk is how dokku-datastore is embedded. The dokku-datastore binary
// runs it with Run, and a dokku plugin serving every datastore under a single
// command, such as `dokku datastore:create postgres lollipop`, builds its
// commands, subcommands and triggers binaries around RunCommands,
// RunSubcommand and RunTrigger.
package sdk

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/dokku/dokku-datastore/commands"
	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/mitchellh/cli"
)

// AppName is the name of the cli tool
const AppName = "dokku-datastore"

// Namespace is the dokku command every datastore is reached through by the
// plugin serving all of them
const Namespace = "datastore"

// modulePath is this module, whose version is the one an embedding binary was
// built with
const modulePath = "github.com/dokku/dokku-datastore"

// version is the version Run was handed, which wins over the one the build
// recorded
var version string

// RunInput is the input for Run
type RunInput struct {
	// AppName is the name the cli reports itself as, AppName when empty
	AppName string

	// Args are the arguments, without the program name
	Args []string

	// Version is the version the cli reports, Version() when empty
	Version string
}

// Run executes a command of the binary, as the dokku-datastore cli does, and
// returns its exit code
func Run(input RunInput) int {
	appName := input.AppName
	if appName == "" {
		appName = AppName
	}

	if input.Version != "" {
		version = input.Version
	}

	ctx := context.Background()
	commandMeta := command.SetupRun(ctx, appName, Version(), input.Args)
	commandMeta.Ui = command.HumanZerologUiWithFields(commandMeta.Ui, make(map[string]interface{}, 0))
	c := cli.NewCLI(appName, Version())
	c.Args = input.Args
	c.Commands = command.Commands(ctx, commandMeta, Commands)
	exitCode, err := c.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error executing CLI: %s\n", err.Error())
		return 1
	}

	return exitCode
}

// Version is the version of dokku-datastore that is running: the one Run was
// handed, or else the one go recorded for this module when the binary
// embedding it was built, which is the version an embedding plugin requires
func Version() string {
	if version != "" {
		return version
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	if info.Main.Path == modulePath {
		return info.Main.Version
	}

	for _, dependency := range info.Deps {
		if dependency.Path != modulePath {
			continue
		}

		if dependency.Replace != nil && dependency.Replace.Version != "" {
			return dependency.Replace.Version
		}

		return dependency.Version
	}

	return ""
}

// runCommand runs one of the binary's commands. It is a variable so that the
// tests can see what each entry point asks for without running it.
var runCommand = func(args []string) int {
	return Run(RunInput{Args: args})
}

// useNamespace has every datastore answer under the one command, and resolves
// them again now that their definitions come only from the binary. It reports
// the exit code to stop with when they cannot be resolved.
func useNamespace() (int, bool) {
	hostenv.SetCommandNamespace(Namespace)
	if err := service.Load(); err != nil {
		fmt.Fprintf(os.Stderr, " !     %s\n", err.Error())
		return 1, false
	}

	return 0, true
}

// RunSubcommand runs one of a datastore's commands the way dokku's subcommands
// binary is asked for it: name is the command, from the binary's file name, and
// args are what dokku passed after the command itself, starting with the
// datastore type, as in `create postgres lollipop`.
func RunSubcommand(name string, args []string) int {
	if code, ok := useNamespace(); !ok {
		return code
	}

	return runSubcommand(name, args)
}

// runSubcommand is RunSubcommand once the namespace is in use
func runSubcommand(name string, args []string) int {
	if name == "enter" {
		// everything after the service name belongs to the command run in the
		// container, so it is fenced off from the binary's own flag and help
		// parsing, as the per-datastore plugin's script does
		if len(args) >= 2 {
			fenced := append([]string{args[0], args[1], "--"}, args[2:]...)
			return runCommand(append([]string{"enter"}, fenced...))
		}

		return runCommand(append([]string{"enter"}, args...))
	}

	if slices.Contains(SubcommandNames(), name) {
		return runCommand(append([]string{name}, args...))
	}

	// anything else is one of a datastore's own commands, which invoke runs and
	// refuses as not implemented for a datastore without it
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, " !     %s:%s requires a datastore type\n", Namespace, name)
		return 1
	}

	return runCommand(append([]string{"invoke", args[0], name}, args[1:]...))
}

// RunCommands answers whatever dokku hands the plugin's commands binary: the
// help it asks every plugin for, and any of the plugin's commands it found no
// subcommand for, which is what a datastore's own commands are.
func RunCommands(args []string) int {
	if len(args) == 0 {
		return commands.NotImplementedExit()
	}

	requested := args[0]
	switch {
	case requested == "help":
		// dokku builds its listing by asking every plugin in a pipeline under
		// set -e, so a failure here would take the listing down for every
		// other plugin too
		if _, ok := useNamespace(); !ok {
			return 0
		}

		runCommand(append([]string{"trigger-namespace-help"}, args...))
		return 0
	case requested == Namespace, requested == Namespace+":help", requested == Namespace+":default":
		if code, ok := useNamespace(); !ok {
			return code
		}

		return runCommand(append([]string{"trigger-namespace-help"}, args...))
	case requested == Namespace+":version":
		fmt.Println(Version())
		return 0
	case strings.HasPrefix(requested, Namespace+":"):
		return RunSubcommand(strings.TrimPrefix(requested, Namespace+":"), args[1:])
	}

	return commands.NotImplementedExit()
}

// RunTrigger runs a dokku trigger for every datastore no per-datastore plugin
// serves, the way dokku runs a plugin's trigger binary: name is the trigger,
// from the binary's file name, and args are what dokku passed it. It stops at
// the first datastore that fails, as dokku stops at the first plugin that does.
func RunTrigger(name string, args []string) int {
	if code, ok := useNamespace(); !ok {
		return code
	}

	owned := internal.OwnedTypes()
	install := name == "install" || name == "update"

	installed := []string{}
	for _, datastoreType := range service.Types() {
		if owned[datastoreType] {
			continue
		}

		var argv []string
		switch {
		case install:
			// update has no command of its own: installing is written to be
			// run again, which is all an update is
			argv = []string{"trigger-install", datastoreType}
		case slices.Contains(internal.BuiltinTriggers, name):
			argv = append([]string{"trigger-" + name, datastoreType}, args...)
		default:
			argv = append([]string{"trigger", name, datastoreType}, args...)
		}

		if code := runCommand(argv); code != 0 {
			return code
		}

		installed = append(installed, datastoreType)
	}

	if install {
		return regenerateCrontab(installed)
	}

	return 0
}

// regenerateCrontab has dokku write its crontab again once every datastore is
// installed, when any of them has a scheduled backup. A datastore taken over
// from its own plugin is in the crontab under that plugin's command until
// dokku writes it again, and dokku does not on install.
func regenerateCrontab(installed []string) int {
	ctx := context.Background()
	for _, datastoreType := range installed {
		scheduled, err := internal.HasScheduledBackups(ctx, service.Datastores[datastoreType])
		if err != nil {
			fmt.Fprintf(os.Stderr, " !     %s\n", err.Error())
			return 1
		}

		if !scheduled {
			continue
		}

		if err := internal.RegenerateCrontab(ctx); err != nil {
			fmt.Fprintf(os.Stderr, " !     %s\n", err.Error())
			return 1
		}

		return 0
	}

	return 0
}

// SubcommandNames are the commands every datastore may implement, which is
// what an embedding plugin links a subcommand for. A datastore's own commands
// are not among them: they are reached through RunCommands.
func SubcommandNames() []string {
	names := []string{}
	for name, factory := range Commands(context.Background(), command.Meta{}) {
		c, err := factory()
		if err != nil {
			continue
		}

		// invoke is how a datastore's own commands are reached, not a command
		if _, ok := c.(internal.PluginCommand); !ok || name == "invoke" {
			continue
		}

		names = append(names, name)
	}

	slices.Sort(names)
	return names
}

// TriggerNames are the dokku triggers an embedding plugin links a trigger for:
// the ones every datastore implements, install and update, and every one a
// datastore's definitions declare
func TriggerNames() []string {
	names := append([]string{"install", "update"}, internal.BuiltinTriggers...)
	for _, datastore := range service.Datastores {
		names = append(names, datastore.TriggerNames()...)
	}

	slices.Sort(names)
	return slices.Compact(names)
}

// Types are the datastores the binary serves
func Types() []string {
	return service.Types()
}
