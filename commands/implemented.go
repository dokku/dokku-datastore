package commands

import (
	"github.com/dokku/dokku-datastore/internal/service"
)

// requireImplemented reports the exit code a command should return when the
// datastore does not implement it, and whether it has to.
//
// A datastore that cannot do something exits the way dokku expects of a plugin
// that does not handle a command, so dokku reports it as not a command rather
// than as a command that failed.
func requireImplemented(datastore *service.Datastore, subcommand string) (int, bool) {
	if service.Implements(datastore, subcommand) {
		return 0, false
	}

	return notImplementedExit(), true
}
