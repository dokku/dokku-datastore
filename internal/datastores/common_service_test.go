package datastores

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"testing"
)

// withServiceRoot points DokkuLibRoot at a temporary directory and returns the
// root folder for the named service. DokkuLibRoot is a package level variable
// assigned in init(), so it has to be swapped directly rather than through the
// environment, which means these tests cannot run in parallel.
func withServiceRoot(t *testing.T, s Datastore, serviceName string) string {
	t.Helper()

	// the service files are chowned to the dokku user, which does not exist on a
	// development machine or a CI runner, so point that at whoever is running
	current, err := user.Current()
	if err != nil {
		t.Fatalf("failed to look up the current user: %v", err)
	}
	group, err := user.LookupGroupId(current.Gid)
	if err != nil {
		t.Fatalf("failed to look up the current group: %v", err)
	}
	t.Setenv("DOKKU_SYSTEM_USER", current.Username)
	t.Setenv("DOKKU_SYSTEM_GROUP", group.Name)

	previous := DokkuLibRoot
	DokkuLibRoot = t.TempDir()
	t.Cleanup(func() {
		DokkuLibRoot = previous
	})

	serviceRoot := Folders(s, serviceName).Root
	if err := os.MkdirAll(serviceRoot, 0755); err != nil {
		t.Fatalf("failed to create service root: %v", err)
	}

	return serviceRoot
}

func TestLinkedApps(t *testing.T) {
	tests := []struct {
		name     string
		links    *string
		expected []string
	}{
		{
			name:     "no links file",
			links:    nil,
			expected: []string{},
		},
		{
			name:     "empty links file",
			links:    ptr(""),
			expected: []string{},
		},
		{
			name:     "a single linked app",
			links:    ptr("my-app\n"),
			expected: []string{"my-app"},
		},
		{
			name:     "several linked apps",
			links:    ptr("my-app\nother-app\n"),
			expected: []string{"my-app", "other-app"},
		},
	}

	datastore := Datastores["redis"]
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceRoot := withServiceRoot(t, datastore, "lollipop")
			if test.links != nil {
				if err := os.WriteFile(filepath.Join(serviceRoot, "LINKS"), []byte(*test.links), 0644); err != nil {
					t.Fatalf("failed to write links file: %v", err)
				}
			}

			actual := LinkedApps(context.Background(), LinkedAppsInput{
				Datastore:   datastore,
				ServiceName: "lollipop",
			})
			if !slices.Equal(actual, test.expected) {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

// multiPortDatastore is a redis service with a second port, covering the multi
// port paths that no real datastore exercises yet
type multiPortDatastore struct {
	RedisService
}

func (m *multiPortDatastore) Properties() ServiceStruct {
	properties := m.RedisService.Properties()
	properties.Ports = []int{6379, 6380}
	return properties
}

func TestExposedHostPorts(t *testing.T) {
	tests := []struct {
		name     string
		portFile *string
		expected []string
	}{
		{
			name:     "no port file",
			portFile: nil,
			expected: []string{},
		},
		{
			name:     "empty port file",
			portFile: ptr(""),
			expected: []string{},
		},
		{
			name:     "a single port",
			portFile: ptr("33201\n"),
			expected: []string{"33201"},
		},
		{
			name:     "several ports on one line",
			portFile: ptr("33201 33202\n"),
			expected: []string{"33201", "33202"},
		},
		{
			name:     "no trailing newline",
			portFile: ptr("33201 33202"),
			expected: []string{"33201", "33202"},
		},
	}

	datastore := Datastores["redis"]
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceRoot := withServiceRoot(t, datastore, "lollipop")
			if test.portFile != nil {
				if err := os.WriteFile(filepath.Join(serviceRoot, "PORT"), []byte(*test.portFile), 0644); err != nil {
					t.Fatalf("failed to write port file: %v", err)
				}
			}

			actual := ExposedHostPorts(datastore, "lollipop")
			if !slices.Equal(actual, test.expected) {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

func TestExposedPorts(t *testing.T) {
	tests := []struct {
		name      string
		datastore Datastore
		portFile  *string
		expected  string
	}{
		{
			name:      "not exposed",
			datastore: Datastores["redis"],
			portFile:  nil,
			expected:  "-",
		},
		{
			name:      "empty port file",
			datastore: Datastores["redis"],
			portFile:  ptr(""),
			expected:  "-",
		},
		{
			name:      "a single port",
			datastore: Datastores["redis"],
			portFile:  ptr("33201\n"),
			expected:  "6379->33201",
		},
		{
			name:      "several ports on one line",
			datastore: &multiPortDatastore{},
			portFile:  ptr("33201 33202\n"),
			expected:  "6379->33201 6380->33202",
		},
		{
			name:      "more ports than the datastore declares",
			datastore: Datastores["redis"],
			portFile:  ptr("33201 33202\n"),
			expected:  "6379->33201",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceRoot := withServiceRoot(t, test.datastore, "lollipop")
			if test.portFile != nil {
				if err := os.WriteFile(filepath.Join(serviceRoot, "PORT"), []byte(*test.portFile), 0644); err != nil {
					t.Fatalf("failed to write port file: %v", err)
				}
			}

			if actual := ExposedPorts(test.datastore, "lollipop"); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

func ptr(s string) *string {
	return &s
}

func TestAddAndRemoveLinkedApp(t *testing.T) {
	datastore := Datastores["redis"]
	ctx := context.Background()
	input := LinkedAppsInput{Datastore: datastore, ServiceName: "lollipop"}

	withServiceRoot(t, datastore, "lollipop")

	// adding to a service with no links file at all
	if err := AddLinkedApp(ctx, input, "my-app"); err != nil {
		t.Fatalf("failed to add a linked app: %v", err)
	}
	if actual := LinkedApps(ctx, input); !slices.Equal(actual, []string{"my-app"}) {
		t.Fatalf("expected [my-app], got %v", actual)
	}

	// a second app, kept sorted
	if err := AddLinkedApp(ctx, input, "another-app"); err != nil {
		t.Fatalf("failed to add a second linked app: %v", err)
	}
	if actual := LinkedApps(ctx, input); !slices.Equal(actual, []string{"another-app", "my-app"}) {
		t.Fatalf("expected [another-app my-app], got %v", actual)
	}

	// adding the same app again does not duplicate it
	if err := AddLinkedApp(ctx, input, "my-app"); err != nil {
		t.Fatalf("failed to re-add a linked app: %v", err)
	}
	if actual := LinkedApps(ctx, input); !slices.Equal(actual, []string{"another-app", "my-app"}) {
		t.Fatalf("expected [another-app my-app], got %v", actual)
	}

	// removing leaves the rest alone
	if err := RemoveLinkedApp(ctx, input, "my-app"); err != nil {
		t.Fatalf("failed to remove a linked app: %v", err)
	}
	if actual := LinkedApps(ctx, input); !slices.Equal(actual, []string{"another-app"}) {
		t.Fatalf("expected [another-app], got %v", actual)
	}

	// removing an app that was never linked is not an error
	if err := RemoveLinkedApp(ctx, input, "never-linked"); err != nil {
		t.Fatalf("expected removing an unlinked app to succeed, got: %v", err)
	}

	// removing the last app empties the file rather than leaving a stale entry
	if err := RemoveLinkedApp(ctx, input, "another-app"); err != nil {
		t.Fatalf("failed to remove the last linked app: %v", err)
	}
	if actual := LinkedApps(ctx, input); len(actual) != 0 {
		t.Fatalf("expected no linked apps, got %v", actual)
	}
}

func TestRemoveLinkedAppWithoutALinksFile(t *testing.T) {
	datastore := Datastores["redis"]
	withServiceRoot(t, datastore, "lollipop")

	err := RemoveLinkedApp(context.Background(), LinkedAppsInput{Datastore: datastore, ServiceName: "lollipop"}, "my-app")
	if err != nil {
		t.Errorf("expected removing from a missing links file to succeed, got: %v", err)
	}
}
