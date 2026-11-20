package internal

import (
	"errors"
	"fmt"
	"io"
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
			// tianon publishes no pg_upgrade image onto nineteen yet, so the
			// data is exported and imported
			name:     "onto a major with no step",
			current:  named("postgres", "postgres-18"),
			target:   named("postgres", "postgres-19"),
			expected: migrationExport,
		},
		{
			name:     "onto the newest major of a flavor",
			current:  named("postgres", "postgres-pgvector-pg18"),
			target:   named("postgres", "postgres-pgvector-pg19"),
			expected: migrationExport,
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

// Exporting and importing the data is a dump and a restore rather than a
// migration in place, so it is only done when it is asked for, either by
// --force or by an answer of yes, and anything else is a refusal that says how
// to go ahead. An upgrade that migrates in place, or not at all, asks nothing.
func TestConfirmMigration(t *testing.T) {
	current, err := service.Datastores["postgres"].WithDefinitionNamed("postgres-17")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	target, err := service.Datastores["postgres"].WithDefinitionNamed("postgres-pgvector-pg17")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	tests := []struct {
		name     string
		plan     migration
		force    bool
		ask      func(string) (string, error)
		accepted bool
	}{
		{name: "forced", plan: migrationExport, force: true, accepted: true},
		{name: "yes", plan: migrationExport, ask: answer("y"), accepted: true},
		{name: "yes in full", plan: migrationExport, ask: answer(" YES \n"), accepted: true},
		{name: "no", plan: migrationExport, ask: answer("n")},
		{name: "nothing", plan: migrationExport, ask: answer("")},
		{name: "no input left", plan: migrationExport, ask: func(string) (string, error) { return "", io.EOF }},
		{name: "nobody to ask", plan: migrationExport},
		{name: "in place", plan: migrationStep, accepted: true},
		{name: "not migrated", plan: migrationNone, accepted: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			asked := ""
			ask := test.ask
			if ask != nil {
				ask = func(question string) (string, error) {
					asked = question
					return test.ask(question)
				}
			}

			err := confirmMigration(UpgradeServiceInput{
				Datastore:   current,
				ServiceName: "lollipop",
				Logger:      Ui{Quiet: true, Stderr: io.Discard, Stdout: io.Discard, Format: "json"},
				Force:       test.force,
				Ask:         ask,
			}, target, test.plan)

			if test.accepted && err != nil {
				t.Fatalf("expected the upgrade to go ahead, got %q", err)
			}

			if !test.accepted {
				if err == nil {
					t.Fatal("expected the upgrade to be refused")
				}

				if !strings.Contains(err.Error(), "nothing was changed") || !strings.Contains(err.Error(), "--force") {
					t.Errorf("expected the refusal to say nothing changed and how to go ahead, got %q", err)
				}
			}

			if (test.force || test.plan != migrationExport) && asked != "" {
				t.Errorf("expected nothing to be asked, got %q", asked)
			}

			if !test.force && test.plan == migrationExport && test.ask != nil && !strings.Contains(asked, "Export and import lollipop now?") {
				t.Errorf("expected to be asked about the service, got %q", asked)
			}
		})
	}
}

// A step that fails is only tried again as an export and an import when the
// upgrade was forced, since that is not the migration the operator ran, and
// never once the service could not be put back on what it ran.
func TestFallsBackToExport(t *testing.T) {
	failed := errors.New("pg_upgrade failed")
	stuck := fmt.Errorf("%w: %w", errNotUndone, failed)

	tests := []struct {
		name     string
		plan     migration
		force    bool
		err      error
		expected bool
	}{
		{name: "a forced step", plan: migrationStep, force: true, err: failed, expected: true},
		{name: "a step", plan: migrationStep, err: failed},
		{name: "a forced step that was not undone", plan: migrationStep, force: true, err: stuck},
		{name: "a forced export", plan: migrationExport, force: true, err: failed},
		{name: "an export", plan: migrationExport, err: failed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := fallsBackToExport(test.plan, test.force, test.err); actual != test.expected {
				t.Errorf("expected %t, got %t", test.expected, actual)
			}
		})
	}
}
