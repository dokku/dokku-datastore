package render

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"

	"github.com/dokku/dokku-datastore/internal/definition"
)

// defaultConfigMode is the mode a seeded config gets when the definition does
// not say. It matches what the bash plugins write their config files as.
const defaultConfigMode fs.FileMode = 0644

// Config is a file to seed into a service's directory before the container
// starts.
type Config struct {
	// Path is the absolute path on the tool side, under the service root. It is
	// not the bind mount source, which is the path dockerd sees and can differ.
	Path string

	// Content is the rendered file body.
	Content string

	// Mode is the file mode.
	Mode fs.FileMode

	// UID and GID own the file. Empty means the caller's default, which is the
	// dokku system user.
	UID string
	GID string
}

// Configs resolves a definition's configs into files and their contents. Nothing
// is written here: the caller decides, and the rule it applies is that a config
// is seeded once and never clobbered, so an operator's edits to a datastore's
// config file survive a restart.
func Configs(input Input) ([]Config, error) {
	configs := make([]Config, 0, len(input.Definition.Service.Configs))

	for _, entry := range input.Definition.Service.Configs {
		declared, ok := input.Definition.Configs[entry.Source]
		if !ok {
			return nil, fmt.Errorf("config entry names %q, which is not a top level config", entry.Source)
		}

		relative, ok := input.Definition.ServicePath(entry.Target)
		if !ok {
			return nil, fmt.Errorf("config %q targets %q, which is not inside any bind mount", entry.Source, entry.Target)
		}

		content, err := definition.Render(declared.Content, input.Scope)
		if err != nil {
			return nil, err
		}

		mode := defaultConfigMode
		if entry.Mode != "" {
			parsed, err := strconv.ParseUint(entry.Mode, 8, 32)
			if err != nil {
				return nil, fmt.Errorf("config %q has mode %q, which is not an octal file mode: %w", entry.Source, entry.Mode, err)
			}

			mode = fs.FileMode(parsed)
		}

		configs = append(configs, Config{
			Path:    filepath.Join(input.Scope.ServiceRoot, filepath.FromSlash(relative)),
			Content: content,
			Mode:    mode,
			UID:     entry.UID,
			GID:     entry.GID,
		})
	}

	return configs, nil
}
