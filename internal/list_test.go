package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dokku/dokku-datastore/internal/datastores"
)

// withDataRoot points the package at a temporary services root. Both variables
// are set because listing reads PluginDataRoot while Folders reads DokkuLibRoot,
// and the two are only ever consistent by construction.
func withDataRoot(t *testing.T) {
	t.Helper()

	root := t.TempDir()
	previousLib, previousData := datastores.DokkuLibRoot, datastores.PluginDataRoot
	datastores.DokkuLibRoot = root
	datastores.PluginDataRoot = filepath.Join(root, "services")

	t.Cleanup(func() {
		datastores.DokkuLibRoot = previousLib
		datastores.PluginDataRoot = previousData
	})
}

func TestListServicesSkipsStrayFiles(t *testing.T) {
	datastore := datastores.Datastores["redis"]

	withDataRoot(t)

	pluginRoot := filepath.Dir(datastores.Folders(datastore, "unused").Root)
	for _, service := range []string{"lollipop", "gobstopper"} {
		if err := os.MkdirAll(filepath.Join(pluginRoot, service), 0755); err != nil {
			t.Fatalf("failed to create the service root: %s", err)
		}
	}

	// the directory holding the services is what is enumerated to list them, so
	// anything else left here is reported as a service of its own
	if err := os.WriteFile(filepath.Join(pluginRoot, ".TMP_CRON_FILE"), []byte("* * * * *\n"), 0644); err != nil {
		t.Fatalf("failed to write the stray file: %s", err)
	}

	services, err := ListServices(t.Context(), ListServicesInput{Datastore: datastore})
	if err != nil {
		t.Fatalf("failed to list services: %s", err)
	}

	expected := map[string]bool{"gobstopper": true, "lollipop": true}
	if len(services) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, services)
	}

	for _, service := range services {
		if !expected[service] {
			t.Errorf("%q is not a service", service)
		}
	}
}

// A service root symlinked onto another disk is still a service, which is why
// the filter drops regular files rather than keeping only directories.
func TestListServicesKeepsASymlinkedServiceRoot(t *testing.T) {
	datastore := datastores.Datastores["redis"]

	withDataRoot(t)

	pluginRoot := filepath.Dir(datastores.Folders(datastore, "unused").Root)
	if err := os.MkdirAll(pluginRoot, 0755); err != nil {
		t.Fatalf("failed to create the plugin root: %s", err)
	}

	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(pluginRoot, "lollipop")); err != nil {
		t.Fatalf("failed to link the service root: %s", err)
	}

	services, err := ListServices(t.Context(), ListServicesInput{Datastore: datastore})
	if err != nil {
		t.Fatalf("failed to list services: %s", err)
	}

	if len(services) != 1 || services[0] != "lollipop" {
		t.Errorf("expected the linked service to be listed, got %v", services)
	}
}
