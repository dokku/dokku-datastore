package internal

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

// An upgrade that names only an image is nothing to do when the service already
// runs it. One that also changes a setting is a recreate whatever the image says,
// so the short circuit has to know the difference.
func TestUpgradeChangesSettings(t *testing.T) {
	value := "something"
	list := []string{"a-network"}
	driver := "json-file"
	options := []string{"max-size=20m"}
	policy := "unless-stopped"
	timeout := "120"
	memory := 512

	tests := []struct {
		name     string
		input    UpgradeServiceInput
		expected bool
	}{
		{name: "nothing but an image", input: UpgradeServiceInput{Image: "redis", ImageVersion: "8.8.0"}},
		{name: "config options", input: UpgradeServiceInput{ConfigOptions: &value}, expected: true},
		{name: "custom env", input: UpgradeServiceInput{CustomEnv: &value}, expected: true},
		{name: "initial network", input: UpgradeServiceInput{InitialNetwork: &value}, expected: true},
		{name: "post create networks", input: UpgradeServiceInput{PostCreateNetworks: &list}, expected: true},
		{name: "post start networks", input: UpgradeServiceInput{PostStartNetworks: &list}, expected: true},
		{name: "shm size", input: UpgradeServiceInput{ShmSize: &value}, expected: true},
		{name: "memory", input: UpgradeServiceInput{Memory: &memory}, expected: true},
		{name: "log driver", input: UpgradeServiceInput{LogDriver: &driver}, expected: true},
		{name: "log options", input: UpgradeServiceInput{LogOptions: &options}, expected: true},
		{name: "restart policy", input: UpgradeServiceInput{RestartPolicy: &policy}, expected: true},
		{name: "wait timeout", input: UpgradeServiceInput{WaitTimeout: &timeout}, expected: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := test.input.changesSettings(); actual != test.expected {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

// Clearing a setting is a change like any other, and the reason these are
// pointers: an empty string has to be able to mean "set this to nothing" rather
// than "this was not asked about".
func TestUpgradeCanClearASetting(t *testing.T) {
	empty := ""
	if !(UpgradeServiceInput{ConfigOptions: &empty}).changesSettings() {
		t.Error("expected clearing config options to count as a change")
	}

	// a limit of zero is no limit, which is a value to set rather than an
	// absent flag
	unlimited := 0
	if !(UpgradeServiceInput{Memory: &unlimited}).changesSettings() {
		t.Error("expected clearing the memory limit to count as a change")
	}
}

// Which version a bare upgrade lands on. A service is moved to the newest its
// own definition ships rather than the newest the plugin has, because crossing a
// major version moves where the data is mounted and has to be asked for by name.
func TestUpgradeVersion(t *testing.T) {
	postgres17, ok := service.Datastores["postgres"]
	if !ok {
		t.Fatal("expected postgres to be registered")
	}
	// the definition a 17.x service runs, which is not the newest postgres
	postgres17 = postgres17.ForImage("", "17.0")

	definition17 := postgres17.Definition
	if definition17.Name != "postgres-17" {
		t.Fatalf("expected the postgres-17 definition, got %s", definition17.Name)
	}

	tests := []struct {
		name           string
		recorded       service.RecordedImage
		requestedImage string
		requested      string
		expected       string
		expectedErr    bool
	}{
		{
			name:      "a version asked for wins",
			recorded:  service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			requested: "18.6",
			expected:  "18.6",
		},
		{
			// the newest 17, not the newest postgres: moving to 18 relocates the
			// data directory and is not something a bare upgrade may do
			name:     "no version asked for stays inside the definition",
			recorded: service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			expected: definition17.DefaultImageVersion,
		},
		{
			// an upgrade was asked for in so many words, so the default is a
			// choice rather than a guess, and it is what puts right a service
			// that start refused to place
			name:     "no record at all takes the default",
			expected: definition17.DefaultImageVersion,
		},
		{
			name:        "a custom image has no newest to move to",
			recorded:    service.RecordedImage{Image: "postgis/postgis", ImageVersion: "17-3.4"},
			expectedErr: true,
		},
		{
			name:      "a custom image can still be moved by name",
			recorded:  service.RecordedImage{Image: "postgis/postgis", ImageVersion: "17-3.4"},
			requested: "17-3.5",
			expected:  "17-3.5",
		},
		{
			// the version belongs to the repository that published it, so the
			// definition's is no more applicable to an image being moved to than
			// to one already being run
			name:           "an image asked for has no newest either",
			recorded:       service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			requestedImage: "postgis/postgis",
			expectedErr:    true,
		},
		{
			name:           "an image asked for by name and version is taken",
			recorded:       service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			requestedImage: "postgis/postgis",
			requested:      "17-3.5",
			expected:       "17-3.5",
		},
		{
			// the definition's own image, named rather than assumed, still has
			// a version to fall back on
			name:           "the definition's own image asked for takes the default",
			recorded:       service.RecordedImage{Image: "postgis/postgis", ImageVersion: "17-3.4"},
			requestedImage: definition17.DefaultImage,
			expected:       definition17.DefaultImageVersion,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := upgradeVersion(definition17, test.recorded, test.requestedImage, test.requested)
			if test.expectedErr {
				if err == nil {
					t.Fatalf("expected an error, got the version %q", actual)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// A flavor ships its own image, so a bare upgrade of a service on it has a
// newest to move to, and it is the newest of the service's own major.
func TestUpgradeVersionOfAFlavor(t *testing.T) {
	pgvector17 := service.Datastores["postgres"].ForImage("pgvector/pgvector", "pg17").Definition
	if pgvector17.Name != "postgres-pgvector-pg17" {
		t.Fatalf("expected the postgres-pgvector-pg17 definition, got %s", pgvector17.Name)
	}

	recorded := service.RecordedImage{Image: "pgvector/pgvector", ImageVersion: "pg17"}
	actual, err := upgradeVersion(pgvector17, recorded, "", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if actual != pgvector17.DefaultImageVersion {
		t.Errorf("expected %q, got %q", pgvector17.DefaultImageVersion, actual)
	}
}

// A definition named outright supplies the version as well as the image, so a
// service moved onto one with nothing else said lands on the version it pins
// rather than the one the service's old definition does.
func TestUpgradeVersionOfANamedDefinition(t *testing.T) {
	pgvector17, err := service.Datastores["postgres"].WithDefinitionNamed("postgres-pgvector-pg17")
	if err != nil {
		t.Fatalf("expected postgres-pgvector-pg17 to resolve, got %v", err)
	}

	recorded := service.RecordedImage{Image: "postgres", ImageVersion: "18.4"}
	actual, err := upgradeVersion(pgvector17.Definition, recorded, pgvector17.Definition.DefaultImage, "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if actual != pgvector17.Definition.DefaultImageVersion {
		t.Errorf("expected %q, got %q", pgvector17.Definition.DefaultImageVersion, actual)
	}

	// and an image of its own still needs a version named alongside it
	if _, err := upgradeVersion(pgvector17.Definition, recorded, "myorg/pgvector", ""); err == nil {
		t.Error("expected an image the definition does not ship to need a version")
	}
}

// Which definition an upgrade leaves a service on. It moves only when the image
// the service ran and the one it is moved to resolve to different definitions,
// so a service pinned before its flavor had definitions keeps the directory its
// data is in, unless the upgrade named a definition outright.
func TestUpgradeTarget(t *testing.T) {
	postgres := service.Datastores["postgres"]

	named := func(name string) *service.Datastore {
		found, err := postgres.WithDefinitionNamed(name)
		if err != nil {
			t.Fatalf("expected %s to resolve, got %v", name, err)
		}
		return found
	}

	tests := []struct {
		name     string
		pinned   *service.Datastore
		named    *service.Datastore
		recorded service.RecordedImage
		image    string
		version  string
		expected string
	}{
		{
			name:     "inside a major the pin stays",
			pinned:   postgres.ForImage("", "17.0"),
			recorded: service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			image:    "postgres",
			version:  "17.11",
			expected: "postgres-17",
		},
		{
			name:     "across a major the pin moves",
			pinned:   postgres.ForImage("", "17.0"),
			recorded: service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			image:    "postgres",
			version:  "18.6",
			expected: "postgres-18",
		},
		{
			name:     "onto a flavor the pin moves",
			pinned:   postgres.ForImage("", "17.0"),
			recorded: service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			image:    "pgvector/pgvector",
			version:  "0.8.6-pg17",
			expected: "postgres-pgvector-pg17",
		},
		{
			// created with pgvector/pgvector:pg17 before pgvector had
			// definitions, which placed it on postgres-18 and its data with it
			name:     "a pin older than its flavor stays",
			pinned:   postgres.ForImage("", "18.4"),
			recorded: service.RecordedImage{Image: "pgvector/pgvector", ImageVersion: "pg17"},
			image:    "pgvector/pgvector",
			version:  "0.8.6-pg17",
			expected: "postgres-18",
		},
		{
			name:     "a pin older than its flavor moves across a major",
			pinned:   postgres.ForImage("", "18.4"),
			recorded: service.RecordedImage{Image: "pgvector/pgvector", ImageVersion: "pg17"},
			image:    "pgvector/pgvector",
			version:  "0.8.6-pg18",
			expected: "postgres-pgvector-pg18",
		},
		{
			name:     "no record takes what the image resolves to",
			pinned:   postgres.ForImage("", "18.4"),
			image:    "postgres",
			version:  "17.11",
			expected: "postgres-17",
		},
		{
			// a tag that does not carry its major resolves to nothing in
			// particular, and so to the newest, which is why a definition can be
			// named at all
			name:     "a custom tag with no name moves to the newest",
			pinned:   postgres.ForImage("", "17.0"),
			recorded: service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			image:    "myorg/postgres",
			version:  "custom-3",
			expected: "postgres-18",
		},
		{
			name:     "a custom tag with a name stays on it",
			pinned:   postgres.ForImage("", "17.0"),
			named:    named("postgres-17"),
			recorded: service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			image:    "myorg/postgres",
			version:  "custom-3",
			expected: "postgres-17",
		},
		{
			name:     "a name wins over the image",
			pinned:   postgres.ForImage("", "18.4"),
			named:    named("postgres-pgvector-pg17"),
			recorded: service.RecordedImage{Image: "postgres", ImageVersion: "18.4"},
			image:    "postgres",
			version:  "18.4",
			expected: "postgres-pgvector-pg17",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := upgradeTarget(test.pinned, test.named, test.recorded, test.image, test.version)
			if name := actual.DefinitionName(); name != test.expected {
				t.Errorf("expected %s, got %s", test.expected, name)
			}
		})
	}
}

// An upgrade takes the old container away before it makes the new one, so a log
// option docker will not accept has to be refused at the top rather than when
// the container is finally built, which would leave the service with neither.
func TestUpgradeRefusesAnUnusableLogConfig(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	options := []string{"max-size=20"}
	err := UpgradeService(t.Context(), UpgradeServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		LogOptions:  &options,
	})
	if err == nil {
		t.Fatal("expected a malformed log option to be refused, got no error")
	}

	if !strings.Contains(err.Error(), `invalid max-size value "20"`) {
		t.Errorf("expected the error to name the value, got %q", err)
	}
}

// A definition that is not the datastore's own is refused before the service is
// touched, rather than moving it onto whatever its image would resolve to.
func TestUpgradeRefusesAnUnknownDefinition(t *testing.T) {
	datastore := service.Datastores["postgres"]
	withDataRoot(t)

	err := UpgradeService(t.Context(), UpgradeServiceInput{
		Datastore:   datastore,
		Definition:  "redis",
		ServiceName: "lollipop",
	})
	if err == nil {
		t.Fatal("expected the redis definition to be refused for postgres, got no error")
	}

	if !strings.Contains(err.Error(), "is not a postgres definition") {
		t.Errorf("expected the error to say whose definition it is not, got %q", err)
	}
}

// The same holds for a restart policy: docker refuses to make a container from
// one it does not know, and by then the old container is already gone.
func TestUpgradeRefusesAnUnusableRestartPolicy(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	policy := "sometimes"
	err := UpgradeService(t.Context(), UpgradeServiceInput{
		Datastore:     datastore,
		ServiceName:   "lollipop",
		RestartPolicy: &policy,
	})
	if err == nil {
		t.Fatal("expected a malformed restart policy to be refused, got no error")
	}

	if !strings.Contains(err.Error(), `invalid restart-policy value "sometimes"`) {
		t.Errorf("expected the error to name the value, got %q", err)
	}
}

// And a wait timeout, which would otherwise be written down and then fail the
// wait the upgrade ends with, and every start after it.
func TestUpgradeRefusesAnUnusableWaitTimeout(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	timeout := "0"
	err := UpgradeService(t.Context(), UpgradeServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		WaitTimeout: &timeout,
	})
	if err == nil {
		t.Fatal("expected a malformed wait timeout to be refused, got no error")
	}

	if !strings.Contains(err.Error(), `invalid wait-timeout value "0"`) {
		t.Errorf("expected the error to name the value, got %q", err)
	}
}

// An upgrade rewrites the custom environment and the config options of a service
// that may have been made when both were readable by everyone, and the new
// values must not land in a file that still is.
func TestUpgradeSettingsKeepTheEnvironmentPrivate(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	files := service.Files(datastore, "lollipop")
	if err := os.MkdirAll(service.Folders(datastore, "lollipop").Root, 0775); err != nil {
		t.Fatalf("failed to create the service root: %s", err)
	}
	for _, filename := range []string{files.Env, files.ConfigOptions} {
		if err := os.WriteFile(filename, []byte("old"), 0644); err != nil {
			t.Fatalf("failed to write %s: %s", filename, err)
		}
		if err := os.Chmod(filename, 0644); err != nil {
			t.Fatalf("failed to chmod %s: %s", filename, err)
		}
	}

	env := "API_TOKEN=hunter2"
	options := "--requirepass hunter2"
	shmSize := "256m"
	if err := applyUpgradeSettings(UpgradeServiceInput{
		ConfigOptions: &options,
		CustomEnv:     &env,
		Datastore:     datastore,
		ServiceName:   "lollipop",
		ShmSize:       &shmSize,
	}); err != nil {
		t.Fatalf("failed to apply the settings: %s", err)
	}

	for filename, expected := range map[string]os.FileMode{
		files.Env:           service.PrivateFileMode,
		files.ConfigOptions: service.PrivateFileMode,
		files.ShmSize:       0644,
	} {
		if mode := fileMode(t, filename); mode != expected {
			t.Errorf("expected %s to be %o, got %o", filename, expected, mode)
		}
	}

	contents, err := os.ReadFile(files.Env)
	if err != nil {
		t.Fatalf("failed to read %s: %s", files.Env, err)
	}
	if string(contents) != env {
		t.Errorf("expected the new environment, got %q", contents)
	}
}

// The memory limit is read from its file each time a container is made, so an
// upgrade changes it by writing the file, and leaves it alone when not asked.
func TestApplyUpgradeSettingsWritesTheMemory(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	files := service.Files(datastore, "lollipop")
	if err := os.MkdirAll(service.Folders(datastore, "lollipop").Root, 0775); err != nil {
		t.Fatalf("failed to create the service root: %s", err)
	}
	if err := os.WriteFile(files.Memory, []byte("512"), 0644); err != nil {
		t.Fatalf("failed to write %s: %s", files.Memory, err)
	}

	readMemory := func() string {
		t.Helper()
		contents, err := os.ReadFile(files.Memory)
		if err != nil {
			t.Fatalf("failed to read %s: %s", files.Memory, err)
		}
		return string(contents)
	}

	if err := applyUpgradeSettings(UpgradeServiceInput{Datastore: datastore, ServiceName: "lollipop"}); err != nil {
		t.Fatalf("failed to apply the settings: %s", err)
	}
	if actual := readMemory(); actual != "512" {
		t.Errorf("expected the memory limit to be kept, got %q", actual)
	}

	for _, memory := range []int{256, 0} {
		if err := applyUpgradeSettings(UpgradeServiceInput{Datastore: datastore, ServiceName: "lollipop", Memory: &memory}); err != nil {
			t.Fatalf("failed to apply the settings: %s", err)
		}
		if actual, expected := readMemory(), strconv.Itoa(memory); actual != expected {
			t.Errorf("expected %q, got %q", expected, actual)
		}
		if mode := fileMode(t, files.Memory); mode != 0644 {
			t.Errorf("expected %s to be 644, got %o", files.Memory, mode)
		}
	}
}
