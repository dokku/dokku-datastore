package commands

import (
	"context"

	"github.com/dokku/dokku-datastore/internal"

	"github.com/josegonzalez/cli-skeleton/command"
)

// pluginCommands returns the commands this binary implements that a dokku
// datastore plugin exposes as subcommands. The registry lives in package main,
// which imports this one, so it is handed in rather than reached for.
func pluginCommands(ctx context.Context, meta command.Meta, commandFunc command.CommandFunc) []internal.PluginCommand {
	if commandFunc == nil {
		return nil
	}

	found := []internal.PluginCommand{}
	for _, factory := range commandFunc(ctx, meta) {
		c, err := factory()
		if err != nil {
			continue
		}

		pluginCommand, ok := c.(internal.PluginCommand)
		if !ok {
			continue
		}

		found = append(found, pluginCommand)
	}

	return found
}

// findPluginCommand returns the command with a given name
func findPluginCommand(commands []internal.PluginCommand, name string) (internal.PluginCommand, bool) {
	for _, c := range commands {
		if c.Name() == name {
			return c, true
		}
	}

	return nil, false
}
