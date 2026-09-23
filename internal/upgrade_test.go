package internal

import (
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

// An upgrade that names only an image is nothing to do when the service already
// runs it. One that also changes a setting is a recreate whatever the image says,
// so the short circuit has to know the difference.
func TestUpgradeChangesSettings(t *testing.T) {
	value := "something"
	list := []string{"a-network"}

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
	postgres17 = postgres17.ForImageVersion("17.0")

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
