package commands

import (
	"os"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/service"
)

// withoutVerbs is a datastore that declares no commands, which is what a
// datastore with nothing to connect to or dump looks like.
func withoutVerbs(t *testing.T) *service.Datastore {
	t.Helper()

	redis, ok := service.Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	bare := &service.Datastore{Definition: redis.Definition}
	bare.Definition.Dokku.Commands = map[string]definition.Command{}

	return bare
}

// exportOnly declares export but not import, which is what a datastore that can
// be dumped but not loaded looks like.
func exportOnly(t *testing.T) *service.Datastore {
	t.Helper()

	redis, ok := service.Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	partial := &service.Datastore{Definition: redis.Definition}
	partial.Definition.Dokku.Commands = map[string]definition.Command{
		"export": redis.Definition.Dokku.Commands["export"],
	}

	return partial
}

func TestRequireImplemented(t *testing.T) {
	tests := []struct {
		name          string
		datastore     *service.Datastore
		subcommand    string
		unimplemented bool
	}{
		{
			name:       "a declared command",
			datastore:  service.Datastores["redis"],
			subcommand: "connect",
		},
		{
			name:          "a command the datastore does not declare",
			datastore:     withoutVerbs(t),
			subcommand:    "connect",
			unimplemented: true,
		},
		{
			// the backup family is built on export, which is what the backup
			// path actually calls
			name:          "the backup family without an export",
			datastore:     withoutVerbs(t),
			subcommand:    "backup-schedule",
			unimplemented: true,
		},
		{
			name:       "the backup family with an export",
			datastore:  exportOnly(t),
			subcommand: "backup",
		},
		{
			// cloning is exporting one service and importing into another, so
			// half of that is not enough
			name:          "clone with only an export",
			datastore:     exportOnly(t),
			subcommand:    "clone",
			unimplemented: true,
		},
		{
			name:       "clone with both",
			datastore:  service.Datastores["redis"],
			subcommand: "clone",
		},
		{
			// everything a datastore does not choose is always available
			name:       "a command no definition decides",
			datastore:  withoutVerbs(t),
			subcommand: "info",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, unimplemented := requireImplemented(test.datastore, test.subcommand)
			if unimplemented != test.unimplemented {
				t.Fatalf("expected unimplemented=%v, got %v", test.unimplemented, unimplemented)
			}

			if !unimplemented {
				return
			}

			if code != defaultNotImplementedExit {
				t.Errorf("expected exit %d, got %d", defaultNotImplementedExit, code)
			}
		})
	}
}

// The code tells dokku to keep looking for a plugin that handles the command,
// and dokku is what says which code that is.
func TestTriggerHelpExitCodes(t *testing.T) {
	if actual := notImplementedExit(); actual != defaultNotImplementedExit {
		t.Errorf("expected %d by default, got %d", defaultNotImplementedExit, actual)
	}

	t.Setenv("DOKKU_NOT_IMPLEMENTED_EXIT", "27")
	if actual := notImplementedExit(); actual != 27 {
		t.Errorf("expected dokku's own code to be honoured, got %d", actual)
	}

	// a value that is not a number is not an instruction, so the default stands
	t.Setenv("DOKKU_NOT_IMPLEMENTED_EXIT", "nonsense")
	if actual := notImplementedExit(); actual != defaultNotImplementedExit {
		t.Errorf("expected the default for an unreadable value, got %d", actual)
	}

	os.Unsetenv("DOKKU_NOT_IMPLEMENTED_EXIT")
}
