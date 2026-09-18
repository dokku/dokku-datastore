package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// postgres is the datastore this exists for: it is split by major version, and
// the two definitions mount their data in different places.
func postgresDatastore(t *testing.T) *Datastore {
	t.Helper()

	found, ok := Datastores["postgres"]
	if !ok {
		t.Fatal("expected a postgres datastore")
	}

	if found.Definition.Name != "postgres-18" {
		t.Fatalf("expected postgres to default to its newest definition, got %q", found.Definition.Name)
	}

	return found
}

// A service created on an older major runs that major's definition, which is the
// bug this exists to close: every service used to be handed the newest one, so a
// postgres 17 service was run by postgres-18 and mounted its data one directory
// up from where postgres 17 keeps it.
func TestForImageVersionSelectsTheMajor(t *testing.T) {
	postgres := postgresDatastore(t)

	tests := []struct {
		imageVersion string
		expected     string
	}{
		{imageVersion: "17.8", expected: "postgres-17"},
		{imageVersion: "18.4", expected: "postgres-18"},
		// a version no definition claims falls back to the newest rather than
		// failing: a service has to stay operable either way
		{imageVersion: "12.1", expected: "postgres-18"},
		{imageVersion: "", expected: "postgres-18"},
	}

	for _, test := range tests {
		t.Run(test.imageVersion, func(t *testing.T) {
			if name := postgres.ForImageVersion(test.imageVersion).DefinitionName(); name != test.expected {
				t.Errorf("expected %s for %q, got %s", test.expected, test.imageVersion, name)
			}
		})
	}
}

// A service that recorded its definition keeps it, whatever it is running and
// whatever the datastore's newest is.
func TestForServiceReadsThePin(t *testing.T) {
	postgres := postgresDatastore(t)
	serviceRoot := withServiceRoot(t, postgres, "pinned")

	// deliberately at odds with the image, so that what is being read is not in
	// doubt: a pin is followed rather than derived
	writeServiceFile(t, filepath.Join(serviceRoot, "DEFINITION"), "postgres-17")
	writeServiceFile(t, filepath.Join(serviceRoot, "IMAGE_VERSION"), "18.4")

	if name := postgres.ForService("pinned").DefinitionName(); name != "postgres-17" {
		t.Errorf("expected the pinned postgres-17, got %s", name)
	}
}

// A service created before the pin existed is placed by the image it recorded,
// which is what the pin would have held.
func TestForServiceDerivesFromTheImageVersion(t *testing.T) {
	postgres := postgresDatastore(t)
	serviceRoot := withServiceRoot(t, postgres, "unpinned")
	writeServiceFile(t, filepath.Join(serviceRoot, "IMAGE_VERSION"), "17.8")

	resolved := postgres.ForService("unpinned")
	if name := resolved.DefinitionName(); name != "postgres-17" {
		t.Fatalf("expected postgres-17 derived from 17.8, got %s", name)
	}

	// and reading is not writing: the backfill happens where a write is expected
	if _, err := os.Stat(filepath.Join(serviceRoot, "DEFINITION")); !os.IsNotExist(err) {
		t.Error("expected reading the definition to leave no pin behind")
	}

	if err := PinDefinition(resolved, "unpinned"); err != nil {
		t.Fatalf("failed to pin the definition: %v", err)
	}

	if pinned := readServiceFile(t, filepath.Join(serviceRoot, "DEFINITION")); pinned != "postgres-17" {
		t.Errorf("expected postgres-17 to be pinned, got %q", pinned)
	}
}

// A pin naming a definition that is no longer loaded - a plugin that shipped its
// own and has since dropped one - falls back to the image rather than to nothing.
func TestForServiceIgnoresAnUnknownPin(t *testing.T) {
	postgres := postgresDatastore(t)
	serviceRoot := withServiceRoot(t, postgres, "stale")
	writeServiceFile(t, filepath.Join(serviceRoot, "DEFINITION"), "postgres-16")
	writeServiceFile(t, filepath.Join(serviceRoot, "IMAGE_VERSION"), "17.8")

	if name := postgres.ForService("stale").DefinitionName(); name != "postgres-17" {
		t.Errorf("expected postgres-17, got %s", name)
	}
}

// A datastore with one definition resolves to it however it is asked, which is
// every datastore but postgres and solr.
func TestForServiceWithASingleDefinition(t *testing.T) {
	redis, ok := Datastores["redis"]
	if !ok {
		t.Fatal("expected a redis datastore")
	}

	serviceRoot := withServiceRoot(t, redis, "cache")
	writeServiceFile(t, filepath.Join(serviceRoot, "IMAGE_VERSION"), "7.4.0")

	if name := redis.ForService("cache").DefinitionName(); name != "redis" {
		t.Errorf("expected redis, got %s", name)
	}
}

// What a datastore can do is what any of its definitions can do: dokku asks
// before a service is named, so answering for the newest alone would report a
// command as unimplemented for the services that have it.
func TestDefinitionsSpansTheMajors(t *testing.T) {
	postgres := postgresDatastore(t)

	names := []string{}
	for _, found := range postgres.Definitions() {
		names = append(names, found.Name)
	}

	if len(names) != 2 || names[0] != "postgres-17" || names[1] != "postgres-18" {
		t.Errorf("expected postgres-17 and postgres-18 oldest first, got %v", names)
	}
}

func writeServiceFile(t *testing.T, filename string, contents string) {
	t.Helper()

	if err := os.WriteFile(filename, []byte(contents+"\n"), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", filename, err)
	}
}

func readServiceFile(t *testing.T, filename string) string {
	t.Helper()

	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("failed to read %s: %v", filename, err)
	}

	return strings.TrimSpace(string(contents))
}
