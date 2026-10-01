package commands

import (
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/mitchellh/cli"
)

// --all-databases is documented, in the usage, the prose and the flag list, only
// for a datastore that can export and import every database, and a backup says
// how to restore what it holds.
func TestAllDatabasesIsDocumentedWhereItIsTaken(t *testing.T) {
	commands := []internal.PluginCommand{&ExportCommand{}, &ImportCommand{}, &BackupCommand{}}

	for name, expected := range map[string]bool{"postgres": true, "redis": false} {
		t.Run(name, func(t *testing.T) {
			data := internal.NewDocumentationData(internal.DocumentationDataInput{Datastore: service.Datastores[name]})

			for _, c := range commands {
				usage, err := internal.RenderDocumentation(c.Usage(), data)
				if err != nil {
					t.Fatalf("unable to render the %s usage: %s", c.Name(), err)
				}

				documentation, err := internal.RenderDocumentation(c.Documentation(), data)
				if err != nil {
					t.Fatalf("unable to render the %s documentation: %s", c.Name(), err)
				}

				flags, err := internal.DocumentedFlags(c, data)
				if err != nil {
					t.Fatalf("unable to document the %s flags: %s", c.Name(), err)
				}

				documented := false
				for _, f := range flags {
					if strings.Contains(f.Label, "--all-databases") {
						documented = true
					}
				}

				if actual := strings.Contains(documentation, "--all-databases"); actual != expected {
					t.Errorf("expected %s to document --all-databases to be %t, got %t:\n%s", c.Name(), expected, actual, documentation)
				}

				if strings.Contains(documentation, "\n\n") {
					t.Errorf("expected the %s documentation to have no blank lines left by the conditionals", c.Name())
				}

				// backup takes no such flag: it always holds every database it can
				if c.Name() == "backup" {
					continue
				}

				if actual := strings.Contains(usage, "[--all-databases]"); actual != expected {
					t.Errorf("expected the %s usage to offer --all-databases to be %t, got %t: %s", c.Name(), expected, actual, usage)
				}

				if documented != expected {
					t.Errorf("expected the %s flags to list --all-databases to be %t, got %t", c.Name(), expected, documented)
				}
			}
		})
	}
}

// Asking a datastore that cannot dump every database for one is refused, before
// anything is exported or written, while asking one that can, or not asking at
// all, goes ahead.
func TestRefuseAllDatabases(t *testing.T) {
	tests := []struct {
		datastore    string
		allDatabases bool
		refused      bool
	}{
		{datastore: "postgres", allDatabases: true},
		{datastore: "postgres", allDatabases: false},
		{datastore: "redis", allDatabases: false},
		{datastore: "redis", allDatabases: true, refused: true},
	}

	for _, test := range tests {
		ui := cli.NewMockUi()
		code, refused := refuseAllDatabases(internal.Ui{Ui: ui}, service.Datastores[test.datastore], test.allDatabases)
		if refused != test.refused {
			t.Errorf("expected %s with --all-databases=%t to be refused=%t, got %t", test.datastore, test.allDatabases, test.refused, refused)
		}

		if refused && code != 1 {
			t.Errorf("expected a refusal to exit 1, got %d", code)
		}

		if refused && !strings.Contains(ui.ErrorWriter.String(), "--all-databases is not supported") {
			t.Errorf("expected the refusal to say --all-databases is not supported, got %q", ui.ErrorWriter.String())
		}
	}
}
