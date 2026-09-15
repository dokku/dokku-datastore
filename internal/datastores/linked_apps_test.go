package datastores

import (
	"os"
	"path/filepath"
	"testing"
)

// linkedService writes a links file for a service and points the package at a
// temporary dokku root, so the test does not depend on the host's apps.
func linkedService(t *testing.T, links []string, apps []string) LinkedAppsInput {
	t.Helper()

	dokkuRoot := t.TempDir()
	t.Setenv("DOKKU_ROOT", dokkuRoot)

	previous := DokkuLibRoot
	DokkuLibRoot = t.TempDir()
	t.Cleanup(func() {
		DokkuLibRoot = previous
	})

	for _, app := range apps {
		if err := os.MkdirAll(filepath.Join(dokkuRoot, app), 0755); err != nil {
			t.Fatalf("failed to create the app root: %s", err)
		}
	}

	datastore := Datastores["redis"]
	serviceRoot := Folders(datastore, "lollipop").Root
	if err := os.MkdirAll(serviceRoot, 0755); err != nil {
		t.Fatalf("failed to create the service root: %s", err)
	}

	contents := ""
	for _, app := range links {
		contents += app + "\n"
	}

	if err := os.WriteFile(filepath.Join(serviceRoot, "LINKS"), []byte(contents), 0644); err != nil {
		t.Fatalf("failed to write the links file: %s", err)
	}

	return LinkedAppsInput{Datastore: datastore, ServiceName: "lollipop"}
}

func TestLiveLinkedApps(t *testing.T) {
	tests := []struct {
		name     string
		links    []string
		apps     []string
		expected []string
	}{
		{
			name:     "an app that is still there",
			links:    []string{"my-app"},
			apps:     []string{"my-app"},
			expected: []string{"my-app"},
		},
		{
			// deleting an app while this plugin was disabled leaves its name
			// behind, and counting it would make the service undeletable
			name:     "an app that has been deleted",
			links:    []string{"ghost"},
			apps:     []string{},
			expected: []string{},
		},
		{
			name:     "one of each",
			links:    []string{"ghost", "my-app"},
			apps:     []string{"my-app"},
			expected: []string{"my-app"},
		},
		{
			name:     "nothing linked",
			links:    []string{},
			apps:     []string{"my-app"},
			expected: []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := linkedService(t, test.links, test.apps)

			actual := LiveLinkedApps(t.Context(), input)
			if len(actual) != len(test.expected) {
				t.Fatalf("expected %v, got %v", test.expected, actual)
			}

			for i, app := range test.expected {
				if actual[i] != app {
					t.Errorf("expected %v, got %v", test.expected, actual)
				}
			}
		})
	}
}

// The stale entry is still in the file: nothing is rewritten behind the
// operator's back, and unlink is what removes it.
func TestLinkedAppsStillReportsADeletedApp(t *testing.T) {
	input := linkedService(t, []string{"ghost"}, []string{})

	if actual := LinkedApps(t.Context(), input); len(actual) != 1 || actual[0] != "ghost" {
		t.Errorf("expected the links file to be reported verbatim, got %v", actual)
	}
}

func TestAppExists(t *testing.T) {
	dokkuRoot := t.TempDir()
	t.Setenv("DOKKU_ROOT", dokkuRoot)

	if err := os.MkdirAll(filepath.Join(dokkuRoot, "my-app"), 0755); err != nil {
		t.Fatalf("failed to create the app root: %s", err)
	}

	if !AppExists("my-app") {
		t.Error("expected an app with a root directory to exist")
	}

	if AppExists("ghost") {
		t.Error("expected an app with no root directory not to exist")
	}
}
