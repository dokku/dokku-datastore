package commands

import (
	"fmt"

	"github.com/dokku/dokku-datastore/internal"
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

// refuseAllDatabases reports the exit code an export or import asked for every
// database should return when the definition the service runs cannot make or
// load such a dump, and whether it has to. It is a failure rather than a missing
// command: the datastore exports and imports, just not every database at once.
func refuseAllDatabases(logger internal.Ui, datastore *service.Datastore, allDatabases bool) (int, bool) {
	if !allDatabases || datastore.ExportsAllDatabases() {
		return 0, false
	}

	logger.Error(internal.ErrorInput{
		Error: fmt.Errorf("the %s datastore only exports and imports the database named for the service, so --all-databases is not supported", datastore.Definition.Dokku.Plugin),
	})
	return 1, true
}

// documentsAllDatabases is whether a datastore's help and readme describe
// --all-databases, which only one that can export every database accepts.
func documentsAllDatabases(name string, data internal.DocumentationData) bool {
	return name != "all-databases" || data.AllDatabases
}
