package commands

import (
	"fmt"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"
)

// lookupDatastore returns the datastore a command was asked to act on, and
// reports why there is none when there is not.
//
// A definition that did not load is reported before an unknown type: a broken
// override leaves its datastore out of the map, and saying the type is not
// supported would send the operator looking for the wrong problem.
func lookupDatastore(logger internal.Ui, datastoreType string) (*service.Datastore, bool) {
	if service.LoadError != nil {
		logger.Error(internal.ErrorInput{Error: service.LoadError})
		return nil, false
	}

	datastore, ok := service.Datastores[datastoreType]
	if !ok {
		logger.Error(internal.ErrorInput{
			Error: fmt.Errorf("datastore type %s is not supported", datastoreType),
		})
		return nil, false
	}

	return datastore, true
}
