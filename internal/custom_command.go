package internal

import (
	"sort"

	"github.com/dokku/dokku-datastore/internal/definition"

	"github.com/josegonzalez/cli-skeleton/command"
	flag "github.com/spf13/pflag"
)

// CustomCommand presents a command a datastore declares for itself as one the
// help and the readme can document.
//
// The tool has no Go command for these, and deliberately so: what the tool
// implements is fixed. They are still a datastore's commands though, so a
// datastore's own help and readme list them beside the rest.
type CustomCommand struct {
	// CommandName is the name the definition declared it under
	CommandName string

	// Declared is the command itself
	Declared definition.Command
}

// Name is the subcommand name, without the plugin's command prefix.
func (c CustomCommand) Name() string {
	return c.CommandName
}

// Description is the one line description, which a definition must supply for a
// custom command: there is nothing else that could describe it.
func (c CustomCommand) Description() string {
	return c.Declared.Description
}

// Usage is the argument sketch rendered after the command name.
func (c CustomCommand) Usage() string {
	usage := "<service>"
	for _, argument := range c.Declared.Arguments {
		if argument.Optional {
			usage += " [" + argument.Name + "]"
			continue
		}

		usage += " <" + argument.Name + ">"
	}

	return usage
}

// Documentation is the long form documentation.
func (c CustomCommand) Documentation() string {
	return c.Declared.Documentation
}

// Group is the readme usage section the command is documented under. A
// definition that does not choose one gets the section the tool's own
// service commands are documented in, since that is what it is.
func (c CustomCommand) Group() string {
	if c.Declared.Group != "" {
		return c.Declared.Group
	}

	return GroupServiceLifecycle
}

// Arguments are the positional arguments the command accepts.
func (c CustomCommand) Arguments() []command.Argument {
	arguments := []command.Argument{{
		Name:        "service",
		Description: "the name of the service to run against",
		Optional:    false,
		Type:        command.ArgumentString,
	}}

	for _, argument := range c.Declared.Arguments {
		arguments = append(arguments, command.Argument{
			Name:        argument.Name,
			Description: argument.Description,
			Optional:    argument.Optional,
			Type:        command.ArgumentString,
		})
	}

	return arguments
}

// FlagSet is empty: a custom command takes what the definition declares and
// nothing else, since the tool has no flags of its own to offer it.
func (c CustomCommand) FlagSet() *flag.FlagSet {
	return flag.NewFlagSet(c.CommandName, flag.ContinueOnError)
}

// CustomCommands are a datastore's own commands, as documentable commands,
// sorted by name.
func CustomCommands(subject definition.Definition) []PluginCommand {
	names := make([]string, 0, len(subject.Dokku.CustomCommands))
	for name := range subject.Dokku.CustomCommands {
		names = append(names, name)
	}

	sort.Strings(names)

	commands := make([]PluginCommand, 0, len(names))
	for _, name := range names {
		commands = append(commands, CustomCommand{
			CommandName: name,
			Declared:    subject.Dokku.CustomCommands[name],
		})
	}

	return commands
}
