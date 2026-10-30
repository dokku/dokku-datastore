// Command plugin is a dokku plugin serving every datastore, laid out the way
// dokku lays out a plugin of its own: tests/sdk.bats links it as the plugin's
// commands file, as each of its subcommands and as each of its triggers, and it
// tells which it is by the name it was run as.
package main

import (
	"os"
	"path/filepath"

	"github.com/dokku/dokku-datastore/sdk"
)

func main() {
	name := filepath.Base(os.Args[0])

	switch {
	case name == "commands":
		os.Exit(sdk.RunCommands(os.Args[1:]))
	case filepath.Base(filepath.Dir(os.Args[0])) == "subcommands":
		// dokku hands a subcommand the command it was asked for first, as in
		// datastore:create, and the arguments after it
		args := []string{}
		if len(os.Args) > 2 {
			args = os.Args[2:]
		}
		os.Exit(sdk.RunSubcommand(name, args))
	default:
		os.Exit(sdk.RunTrigger(name, os.Args[1:]))
	}
}
