package internal

import (
	"fmt"
	"os"
	"path/filepath"
)

// ListServicesInput is the input for the ListServices function
type ListServicesInput struct {
	// DatastoreType is the type of datastore to list
	DatastoreType string
	// Trace is whether to enable trace output
	Trace bool
}

// ListServices lists all services of a given datastore type
func ListServices(input ListServicesInput) ([]string, error) {
	if input.DatastoreType == "" {
		return nil, fmt.Errorf("datastore type is required")
	}

	// list all immediate subfolders in PluginDataRoot
	subfolders, err := os.ReadDir(filepath.Join(PluginDataRoot, input.DatastoreType))
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	datastores := make([]string, len(subfolders))
	for i, subfolder := range subfolders {
		datastores[i] = subfolder.Name()
	}

	datastores, err = FilterServices(input.DatastoreType, datastores, input.Trace)
	if err != nil {
		return nil, fmt.Errorf("failed to filter services: %w", err)
	}

	return datastores, nil
}
