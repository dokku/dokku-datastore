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
func TestForImageSelectsTheMajor(t *testing.T) {
	postgres := postgresDatastore(t)

	tests := []struct {
		imageVersion string
		expected     string
	}{
		{imageVersion: "14.24", expected: "postgres-14"},
		{imageVersion: "15.7", expected: "postgres-15"},
		{imageVersion: "16.15", expected: "postgres-16"},
		{imageVersion: "17.8", expected: "postgres-17"},
		{imageVersion: "18.4", expected: "postgres-18"},
		// a version no definition claims falls back to the newest rather than
		// failing: a service has to stay operable either way
		{imageVersion: "12.1", expected: "postgres-18"},
		{imageVersion: "", expected: "postgres-18"},
	}

	for _, test := range tests {
		t.Run(test.imageVersion, func(t *testing.T) {
			if name := postgres.ForImage("", test.imageVersion).DefinitionName(); name != test.expected {
				t.Errorf("expected %s for %q, got %s", test.expected, test.imageVersion, name)
			}
		})
	}
}

// A flavor's image selects the flavor's definitions, and its tag the major
// among them, however that image writes its tags. pgvector's pg17 carries no
// leading number at all, which is how a postgres 17 service used to be handed
// postgres-18 and its data directory.
func TestForImageSelectsTheFlavor(t *testing.T) {
	postgres := postgresDatastore(t)

	tests := []struct {
		image        string
		imageVersion string
		expected     string
	}{
		{image: "pgvector/pgvector", imageVersion: "pg17", expected: "postgres-pgvector-pg17"},
		{image: "pgvector/pgvector", imageVersion: "pg18", expected: "postgres-pgvector-pg18"},
		{image: "pgvector/pgvector", imageVersion: "0.8.1-pg17", expected: "postgres-pgvector-pg17"},
		{image: "pgvector/pgvector", imageVersion: "0.8.1-pg17-trixie", expected: "postgres-pgvector-pg17"},
		{image: "docker.io/pgvector/pgvector", imageVersion: "pg17", expected: "postgres-pgvector-pg17"},
		{image: "timescale/timescaledb", imageVersion: "2.30.1-pg17", expected: "postgres-timescaledb-pg17"},
		{image: "timescale/timescaledb", imageVersion: "latest-pg18", expected: "postgres-timescaledb-pg18"},
		{image: "postgis/postgis", imageVersion: "17-3.5", expected: "postgres-postgis-pg17"},
		{image: "postgis/postgis", imageVersion: "18-3.6", expected: "postgres-postgis-pg18"},
		// the newest of the flavor, whose own version then applies
		{image: "pgvector/pgvector", imageVersion: "", expected: "postgres-pgvector-pg18"},
		{image: "pgvector/pgvector", imageVersion: "pg12", expected: "postgres-pgvector-pg18"},
		// an image no definition ships runs on the datastore's own, as it did
		{image: "registry.example.com/postgres", imageVersion: "17.8", expected: "postgres-17"},
		{image: "postgres", imageVersion: "17.8", expected: "postgres-17"},
		{image: "library/postgres", imageVersion: "18.4", expected: "postgres-18"},
	}

	for _, test := range tests {
		t.Run(test.image+":"+test.imageVersion, func(t *testing.T) {
			if name := postgres.ForImage(test.image, test.imageVersion).DefinitionName(); name != test.expected {
				t.Errorf("expected %s for %s:%s, got %s", test.expected, test.image, test.imageVersion, name)
			}
		})
	}
}

// A service created before the pin existed is placed by the image as well as
// the version it recorded, since the image is what names the flavor.
func TestForServiceDerivesTheFlavorFromTheImage(t *testing.T) {
	postgres := postgresDatastore(t)
	serviceRoot := withServiceRoot(t, postgres, "vectors")
	writeServiceFile(t, filepath.Join(serviceRoot, "IMAGE"), "pgvector/pgvector")
	writeServiceFile(t, filepath.Join(serviceRoot, "IMAGE_VERSION"), "pg17")

	resolved, err := postgres.ForService("vectors")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if name := resolved.DefinitionName(); name != "postgres-pgvector-pg17" {
		t.Errorf("expected postgres-pgvector-pg17 derived from pgvector/pgvector:pg17, got %s", name)
	}
}

// A service pinned before flavors existed keeps its pin: a pgvector 17 service
// that was handed postgres-18 has its data where postgres-18 mounts it.
func TestForServiceKeepsAPinOlderThanItsFlavor(t *testing.T) {
	postgres := postgresDatastore(t)
	serviceRoot := withServiceRoot(t, postgres, "vectors")
	writeServiceFile(t, filepath.Join(serviceRoot, "DEFINITION"), "postgres-18")
	writeServiceFile(t, filepath.Join(serviceRoot, "IMAGE"), "pgvector/pgvector")
	writeServiceFile(t, filepath.Join(serviceRoot, "IMAGE_VERSION"), "pg17")

	resolved, err := postgres.ForService("vectors")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if name := resolved.DefinitionName(); name != "postgres-18" {
		t.Errorf("expected the pinned postgres-18, got %s", name)
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

	resolved, err := postgres.ForService("pinned")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if name := resolved.DefinitionName(); name != "postgres-17" {
		t.Errorf("expected the pinned postgres-17, got %s", name)
	}
}

// A service created before the pin existed is placed by the image it recorded,
// which is what the pin would have held.
func TestForServiceDerivesFromTheImageVersion(t *testing.T) {
	postgres := postgresDatastore(t)
	serviceRoot := withServiceRoot(t, postgres, "unpinned")
	writeServiceFile(t, filepath.Join(serviceRoot, "IMAGE_VERSION"), "17.8")

	resolved, err := postgres.ForService("unpinned")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

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

// A pin naming a definition this binary does not have is reported rather than
// quietly satisfied with another one. It means a plugin shipped its own
// definitions and dropped one a service is still pinned to, and running that
// service on the newest instead is the failure the pin exists to prevent.
func TestForServiceRefusesAnUnknownPin(t *testing.T) {
	postgres := postgresDatastore(t)
	serviceRoot := withServiceRoot(t, postgres, "stale")
	writeServiceFile(t, filepath.Join(serviceRoot, "DEFINITION"), "postgres-9")
	writeServiceFile(t, filepath.Join(serviceRoot, "IMAGE_VERSION"), "17.8")

	resolved, err := postgres.ForService("stale")
	if err == nil {
		t.Fatal("expected an unknown pin to be reported")
	}

	for _, expected := range []string{"stale", "postgres-9", "does not ship"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("expected the error to mention %q, got %q", expected, err)
		}
	}

	// still usable, because destroy has to be able to remove a service that
	// cannot be run and info has to be able to describe it
	if resolved == nil || resolved.Properties().CommandPrefix != "postgres" {
		t.Error("expected a usable datastore alongside the error")
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

	resolved, err := redis.ForService("cache")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if name := resolved.DefinitionName(); name != "redis" {
		t.Errorf("expected redis, got %s", name)
	}
}

// Every postgres definition declares the same sections, and the readme is
// written once for the plugin, so each is documented once.
func TestDocumentationIsDeclaredOnceAcrossTheMajors(t *testing.T) {
	sections := postgresDatastore(t).Documentation()

	titles := []string{}
	for _, section := range sections {
		titles = append(titles, section.Title)
	}

	expected := []string{"Encrypting connections with TLS", "Choosing the database encoding and locale", "Upgrading across a major version"}
	if strings.Join(titles, "\n") != strings.Join(expected, "\n") {
		t.Errorf("expected %q once each, got %q", expected, titles)
	}

	if sections := Datastores["redis"].Documentation(); len(sections) != 0 {
		t.Errorf("expected no sections for redis, got %v", sections)
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

	// the datastore's own first, then each flavor, every one oldest first
	expected := []string{
		"postgres-14",
		"postgres-15",
		"postgres-16",
		"postgres-17",
		"postgres-18",
		"postgres-pgvector-pg17",
		"postgres-pgvector-pg18",
		"postgres-postgis-pg17",
		"postgres-postgis-pg18",
		"postgres-timescaledb-pg17",
		"postgres-timescaledb-pg18",
	}
	if strings.Join(names, " ") != strings.Join(expected, " ") {
		t.Errorf("expected %v, got %v", expected, names)
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

// A datastore's services live under its plugin name, unless the definition says
// otherwise. Graphite's have always been under the name of the image it runs, and
// an install that already has services there has to keep finding them: the binary
// looking somewhere else would make every one of them invisible.
func TestServicesLiveWhereTheDefinitionSays(t *testing.T) {
	tests := []struct {
		datastore string
		expected  string
	}{
		{datastore: "graphite", expected: "grafana-graphite-statsd"},
		{datastore: "redis", expected: "redis"},
		{datastore: "postgres", expected: "postgres"},
	}

	for _, test := range tests {
		t.Run(test.datastore, func(t *testing.T) {
			found, ok := Datastores[test.datastore]
			if !ok {
				t.Fatalf("expected a %s datastore", test.datastore)
			}

			root := Folders(found, "lollipop").Root
			if actual := filepath.Base(filepath.Dir(root)); actual != test.expected {
				t.Errorf("expected services under %s, got %s", test.expected, actual)
			}

			// the host side is the same directory as dockerd sees it, and a bind
			// mount pointing at the wrong one would be a service with no data
			host := Folders(found, "lollipop").HostRoot
			if actual := filepath.Base(filepath.Dir(host)); actual != test.expected {
				t.Errorf("expected host services under %s, got %s", test.expected, actual)
			}
		})
	}
}

// Graphite is the only definition that declares one, and the other twenty one
// have to keep resolving under their own plugin name: that default is what
// seventeen plugins depend on.
func TestOnlyGraphiteDeclaresADirectory(t *testing.T) {
	for _, datastoreType := range []string{"redis", "postgres", "solr", "elasticsearch", "mongo"} {
		found := Datastores[datastoreType]
		for _, one := range found.Definitions() {
			if one.Dokku.DataDirectory != "" {
				t.Errorf("%s declares %q; only graphite should", one.Name, one.Dokku.DataDirectory)
			}

			if one.ServicesDirectory() != datastoreType {
				t.Errorf("expected %s to resolve to %s, got %s", one.Name, datastoreType, one.ServicesDirectory())
			}
		}
	}
}

// A definition named outright wins over whatever the image would resolve to,
// and only a definition of the datastore itself can be named: postgres cannot
// be placed on redis, and a name nothing ships is refused with the ones that
// could have been given.
func TestWithDefinitionNamed(t *testing.T) {
	postgres := postgresDatastore(t)

	tests := []struct {
		name      string
		datastore *Datastore
		expected  string
		refused   bool
	}{
		{name: "postgres-17", datastore: postgres, expected: "postgres-17"},
		{name: "postgres-pgvector-pg17", datastore: postgres, expected: "postgres-pgvector-pg17"},
		{name: "postgres-18", datastore: postgres, expected: "postgres-18"},
		{name: "postgres-12", datastore: postgres, refused: true},
		{name: "redis", datastore: postgres, refused: true},
		{name: "redis", datastore: Datastores["redis"], expected: "redis"},
		{name: "postgres-17", datastore: Datastores["redis"], refused: true},
	}

	for _, test := range tests {
		t.Run(test.datastore.Definition.Dokku.Plugin+"/"+test.name, func(t *testing.T) {
			found, err := test.datastore.WithDefinitionNamed(test.name)
			if test.refused {
				if err == nil {
					t.Fatalf("expected %s to be refused, got %s", test.name, found.DefinitionName())
				}

				// the error names what could have been given instead
				for _, name := range test.datastore.registry.NamesFor(test.datastore.Definition.Dokku.Plugin) {
					if !strings.Contains(err.Error(), name) {
						t.Errorf("expected the error to list %s, got %q", name, err)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("expected %s to resolve, got %v", test.name, err)
			}

			if found.DefinitionName() != test.expected {
				t.Errorf("expected %s, got %s", test.expected, found.DefinitionName())
			}
		})
	}
}

// Naming a definition leaves the datastore it was asked of alone, since every
// service shares that one.
func TestWithDefinitionNamedDoesNotChangeTheDatastore(t *testing.T) {
	postgres := postgresDatastore(t)

	if _, err := postgres.WithDefinitionNamed("postgres-17"); err != nil {
		t.Fatalf("expected postgres-17 to resolve, got %v", err)
	}

	if postgres.DefinitionName() != "postgres-18" {
		t.Errorf("expected the shared datastore to stay on postgres-18, got %s", postgres.DefinitionName())
	}
}
