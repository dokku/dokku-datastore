// Package seed writes a definition's config files into a service's directory.
// It exists as its own package because it owns one rule that must hold in
// exactly one place: a config is written once and never clobbered.
package seed

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dokku/dokku-datastore/internal/render"
	"github.com/dokku/dokku/plugins/common"
)

// directoryMode matches the mode the service folders are created with.
const directoryMode fs.FileMode = 0775

// Input is the input for Configs.
type Input struct {
	// Configs are the resolved files, from render.Configs.
	Configs []render.Config

	// Username and GroupName own a config that does not name its own uid and
	// gid. Leaving either empty falls back to the dokku user, so a caller that
	// cannot rely on that user existing has to name one.
	Username  string
	GroupName string
}

// Configs writes the configs a service does not already have.
//
// A config that already exists is left alone. Docker's own configs are immutable
// and re-projected on every start, but a datastore's config file is something an
// operator edits and expects to keep, so seeding stops at the first write. The
// consequence, which nothing needs today, is that changing a credential leaves a
// seeded file holding the old one.
func Configs(input Input) error {
	for _, config := range input.Configs {
		if err := ensureDirectory(filepath.Dir(config.Path), input); err != nil {
			return err
		}

		if _, err := os.Stat(config.Path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("unable to check %s: %w", config.Path, err)
		}

		username, groupName := input.Username, input.GroupName
		if config.UID != "" || config.GID != "" {
			username, groupName = config.UID, config.GID
		}

		err := common.WriteStringToFile(common.WriteStringToFileInput{
			Content:   config.Content,
			Filename:  config.Path,
			GroupName: groupName,
			Mode:      config.Mode,
			Username:  username,
		})
		if err != nil {
			return fmt.Errorf("unable to write %s: %w", config.Path, err)
		}
	}

	return nil
}

// ensureDirectory creates the directory a config sits in, when a config targets
// somewhere below the bind mount itself. An existing directory is left exactly
// as it is: the create path already made the service folders with the mode and
// ownership they need, and re-applying a mode here would quietly narrow them.
func ensureDirectory(directory string, input Input) error {
	// every directory about to be created is collected first, because MkdirAll
	// makes the intermediate ones too and each of them needs the same ownership
	missing := []string{}
	for current := directory; ; current = filepath.Dir(current) {
		if _, err := os.Stat(current); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("unable to check %s: %w", current, err)
		}

		missing = append(missing, current)

		if parent := filepath.Dir(current); parent == current {
			break
		}
	}

	if len(missing) == 0 {
		return nil
	}

	// the mode is applied explicitly because MkdirAll is subject to the umask,
	// and the group keeps write access for the same reason the service folders
	// do: the datastore container takes ownership of what it is handed
	if err := os.MkdirAll(directory, directoryMode); err != nil {
		return fmt.Errorf("unable to create %s: %w", directory, err)
	}

	for _, created := range missing {
		err := common.SetPermissions(common.SetPermissionInput{
			Filename:  created,
			GroupName: input.GroupName,
			Mode:      directoryMode,
			Username:  input.Username,
		})
		if err != nil {
			return fmt.Errorf("unable to set permissions on %s: %w", created, err)
		}
	}

	return nil
}
