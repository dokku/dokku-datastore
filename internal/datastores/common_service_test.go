package datastores

import (
	"context"
	"os"
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

func ptr(s string) *string {
	return &s
}
