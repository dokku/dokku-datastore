package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dokku/dokku-datastore/internal/datastores"
)

// ListServicesInput is the input for the ListServices function
type ListServicesInput struct {
	// Datastore is the service to list the services for
	Datastore datastores.Datastore

	// Trace is whether to enable trace output
	Trace bool
}

// ListServices lists all services of a given datastore type
func ListServices(ctx context.Context, input ListServicesInput) ([]string, error) {
	// list all immediate subfolders in PluginDataRoot
	subfolders, err := os.ReadDir(filepath.Join(datastores.PluginDataRoot, input.Datastore.Properties().CommandPrefix))
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	services := make([]string, 0, len(subfolders))
	for _, subfolder := range subfolders {
		// a service is a directory, so a stray file here is not one. Regular
		// files are skipped rather than non directories, because a service root
		// symlinked onto another disk is still a service and requiring IsDir
		// would stop listing it.
		if subfolder.Type().IsRegular() {
			continue
		}

		services = append(services, subfolder.Name())
	}

	services, err = datastores.FilterServices(ctx, datastores.FilterServicesInput{
		Datastore: input.Datastore,
		Services:  services,
		Trace:     input.Trace,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to filter services: %w", err)
	}

	return services, nil
}
