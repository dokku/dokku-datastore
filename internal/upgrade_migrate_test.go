package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

// How an upgrade carries the data across is decided by the definition it lands
// on: postgres migrates every move between its definitions, in place where it
// declares a step for the one the service comes from, and a datastore whose
// next major reads the last one's data leaves it where it is.
func TestUpgradeMigration(t *testing.T) {
	named := func(plugin string, name string) *service.Datastore {
		t.Helper()

		found, err := service.Datastores[plugin].WithDefinitionNamed(name)
		if err != nil {
			t.Fatalf("expected %s to resolve, got %v", name, err)
		}
		return found
	}

	tests := []struct {
		name      string
		current   *service.Datastore
		target    *service.Datastore
		noMigrate bool
		expected  migration
	}{
		{
			name:     "inside a definition",
			current:  named("postgres", "postgres-17"),
			target:   named("postgres", "postgres-17"),
			expected: migrationNone,
		},
		{
			name:     "across a major with a step",
			current:  named("postgres", "postgres-17"),
			target:   named("postgres", "postgres-18"),
			expected: migrationStep,
		},
		{
			name:     "across a major of a flavor",
			current:  named("postgres", "postgres-pgvector-pg17"),
			target:   named("postgres", "postgres-pgvector-pg18"),
			expected: migrationExport,
		},
		{
			// the step is for the official image's cluster, which has none of
			// the flavor's extensions
			name:     "off a flavor onto a major with a step",
			current:  named("postgres", "postgres-pgvector-pg17"),
			target:   named("postgres", "postgres-18"),
			expected: migrationExport,
		},
		{
			name:     "onto a flavor inside a major",
			current:  named("postgres", "postgres-17"),
			target:   named("postgres", "postgres-timescaledb-pg17"),
			expected: migrationExport,
		},
		{
			name:     "down a major",
			current:  named("postgres", "postgres-18"),
			target:   named("postgres", "postgres-17"),
			expected: migrationExport,
		},
		{
			name:     "a datastore that reads its last major in place",
			current:  named("elasticsearch", "elasticsearch-7"),
			target:   named("elasticsearch", "elasticsearch-8"),
			expected: migrationNone,
		},
		{
			name:     "up from a major that was added later",
			current:  named("postgres", "postgres-15"),
			target:   named("postgres", "postgres-17"),
			expected: migrationStep,
		},
		{
			// a service pinned to the newest definition while its container kept
			// its data where postgres 15 does is put back without a migration,
			// which would export the empty cluster the newest one looks at
			name:      "down a major without migrating",
			current:   named("postgres", "postgres-18"),
			target:    named("postgres", "postgres-15"),
			noMigrate: true,
			expected:  migrationNone,
		},
		{
			name:      "across a major with a step without migrating",
			current:   named("postgres", "postgres-17"),
			target:    named("postgres", "postgres-18"),
			noMigrate: true,
			expected:  migrationNone,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := upgradeMigration(test.current, test.target, test.noMigrate); actual != test.expected {
				t.Errorf("expected %d, got %d", test.expected, actual)
			}
		})
	}
}

// A migration copies the data while the apps are stopped, and can only be
// undone onto an image the service is known to run, so both are asked for
// before anything is touched.
func TestCheckMigration(t *testing.T) {
	current, err := service.Datastores["postgres"].WithDefinitionNamed("postgres-17")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	target, err := service.Datastores["postgres"].WithDefinitionNamed("postgres-18")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	recorded := service.RecordedImage{Image: "postgres", ImageVersion: "17.11"}
	tests := []struct {
		name        string
		plan        migration
		restartApps bool
		recorded    service.RecordedImage
		expected    string
	}{
		{name: "no migration needs nothing", plan: migrationNone},
		{name: "a migration without the apps stopped", plan: migrationStep, recorded: recorded, expected: "needs --restart-apps"},
		{name: "an export without the apps stopped", plan: migrationExport, recorded: recorded, expected: "needs --restart-apps"},
		{name: "a migration with nothing to undo onto", plan: migrationStep, restartApps: true, expected: "unable to determine the image it runs"},
		{name: "a migration with the apps stopped", plan: migrationStep, restartApps: true, recorded: recorded},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkMigration(UpgradeServiceInput{
				Datastore:   current,
				RestartApps: test.restartApps,
				ServiceName: "lollipop",
			}, target, test.plan, test.recorded)

			if test.expected == "" {
				if err != nil {
					t.Errorf("unexpected error: %s", err)
				}
				return
			}

			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error mentioning %q, got %v", test.expected, err)
			}
		})
	}
}

// What a destroy widens includes the data an upgrade kept aside, which the
// datastore's user wrote just as it wrote the data the definition binds.
func TestDestroyWidensTheDataAnUpgradeKeptAside(t *testing.T) {
	withDataRoot(t)
	datastore, err := service.Datastores["postgres"].WithDefinitionNamed("postgres-18")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	folders := service.Folders(datastore, "lollipop")
	for _, directory := range []string{"data", "certs", "data.postgres-17.20261001T120000", "data.postgres-pgvector-pg18.20261002T120000"} {
		if err := os.MkdirAll(filepath.Join(folders.Root, directory), 0755); err != nil {
			t.Fatalf("unable to create %s: %s", directory, err)
		}
	}

	directories, err := destroyDirectories(datastore, "lollipop")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	joined := strings.Join(directories, "\n")
	for _, expected := range []string{
		folders.HostRoot + "/data",
		folders.HostRoot + "/data.postgres-17.20261001T120000",
		folders.HostRoot + "/data.postgres-pgvector-pg18.20261002T120000",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("expected %s to be widened, got %v", expected, directories)
		}
	}
}
