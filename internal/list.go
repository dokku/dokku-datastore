package internal

import (
	"os"
	"path/filepath"
)

func ListDatastores(datastoreType string) ([]string, error) {
	// list all immediate subfolders in PluginDataRoot
	subfolders, err := os.ReadDir(filepath.Join(PluginDataRoot, datastoreType))
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

	// todo: filter out services not allowed by the user-auth-service trigger
	// https://github.com/dokku/dokku-pushpin/blob/bdbefaab90b0a97af9a29052f7f0bd9e7f55778d/common-functions#L20

	return datastores, nil
}
