package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku/plugins/common"
)

// previousDataPrefix starts the name of every directory an upgrade moved a
// service's old data to.
const previousDataPrefix = "data."

// previousDataLayout is the timestamp a previous data directory is named with:
// sortable, and free of anything a shell or a mount would read as syntax.
const previousDataLayout = "20060102T150405"

// PreviousDataName is the directory, under the service root, an upgrade moves
// a service's data to when it moves the service off a definition.
//
// Named for the definition the data belongs to, since that is what reads it,
// and for when it was moved, so that a second upgrade never lands on the first
// one's directory.
func PreviousDataName(definitionName string, now time.Time) string {
	return previousDataPrefix + definitionName + "." + now.UTC().Format(previousDataLayout)
}

// PreviousDataDirectories are the directories under a service root that an
// upgrade moved old data to, oldest first.
func PreviousDataDirectories(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("unable to read %s: %w", root, err)
	}

	directories := []string{}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), previousDataPrefix) {
			directories = append(directories, entry.Name())
		}
	}

	sort.Strings(directories)
	return directories, nil
}

// MoveDataAside moves a service's data directory to the named directory under
// its service root, and leaves an empty one in its place.
//
// A rename rather than a copy: it is instant whatever the size of the data, and
// needs nothing but the service root to be the dokku user's, which it is. The
// data inside belongs to whoever the datastore runs as, and stays that way.
func MoveDataAside(s *Datastore, serviceName string, name string) error {
	folders := Folders(s, serviceName)
	previous := filepath.Join(folders.Root, name)
	if _, err := os.Stat(previous); err == nil {
		return fmt.Errorf("unable to move the data of %s aside: %s already exists", serviceName, previous)
	}

	if err := os.Rename(folders.Data, previous); err != nil {
		return fmt.Errorf("unable to move the data of %s aside: %w", serviceName, err)
	}

	if err := makeDataDirectory(folders.Data); err != nil {
		return err
	}

	return nil
}

// RestoreDataAside moves data an upgrade moved aside back where the service
// reads it. The directory it replaces has to be gone already: what a failed
// upgrade wrote there belongs to the datastore's user, so only a container can
// clear it.
func RestoreDataAside(s *Datastore, serviceName string, name string) error {
	folders := Folders(s, serviceName)
	if err := os.Remove(folders.Data); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("unable to clear the data of %s: %w", serviceName, err)
	}

	if err := os.Rename(filepath.Join(folders.Root, name), folders.Data); err != nil {
		return fmt.Errorf("unable to put the data of %s back: %w", serviceName, err)
	}

	return nil
}

// makeDataDirectory makes a data directory the way create makes every bind
// directory, so that it is owned the same way whoever mounts it first.
func makeDataDirectory(directory string) error {
	if err := os.MkdirAll(directory, BindDirectoryMode); err != nil {
		return fmt.Errorf("unable to create %s: %w", directory, err)
	}

	if err := common.SetPermissions(common.SetPermissionInput{
		Filename:  directory,
		GroupName: hostenv.SystemGroup(),
		Mode:      BindDirectoryMode,
		Username:  hostenv.SystemUser(),
	}); err != nil {
		return fmt.Errorf("unable to set permissions on %s: %w", directory, err)
	}

	return nil
}
