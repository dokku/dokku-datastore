package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dokku/dokku-datastore/internal/hostenv"
)

// A broken override is reported rather than panicked on, since the first load
// happens before anything embedding the binary can say to ignore overrides, and
// one that does ignores it on the next load
func TestLoadRecordsABrokenOverride(t *testing.T) {
	// registered before the environment is changed, so that it runs after the
	// environment is put back and reloads what every other test expects
	t.Cleanup(func() {
		hostenv.SetCommandNamespace("")
		Load() //nolint:errcheck
	})

	base := t.TempDir()
	override := filepath.Join(base, "redis", "datastore", "redis")
	if err := os.MkdirAll(override, 0755); err != nil {
		t.Fatalf("unable to create the override: %s", err)
	}

	if err := os.WriteFile(filepath.Join(override, "docker-compose.yml"), []byte("services: [not, a, map"), 0644); err != nil {
		t.Fatalf("unable to write the override: %s", err)
	}

	t.Setenv("PLUGIN_BASE_PATH", base)
	t.Setenv("PLUGIN_COMMAND_PREFIX", "redis")

	if err := Load(); err == nil {
		t.Fatal("expected the broken override to fail the load")
	}

	if LoadError == nil {
		t.Error("expected the failure to be recorded")
	}

	if len(Datastores) != 0 {
		t.Errorf("expected no datastores from a failed load, got %d", len(Datastores))
	}

	hostenv.SetCommandNamespace("datastore")
	if err := Load(); err != nil {
		t.Fatalf("expected the namespace to ignore the override, got %s", err)
	}

	if LoadError != nil {
		t.Errorf("expected the earlier failure to be cleared, got %s", LoadError)
	}

	if _, ok := Datastores["redis"]; !ok {
		t.Error("expected redis from the embedded definitions")
	}
}

func TestTypesAreSorted(t *testing.T) {
	types := Types()
	if len(types) != len(Datastores) {
		t.Fatalf("expected one type per datastore, got %d for %d", len(types), len(Datastores))
	}

	for index := 1; index < len(types); index++ {
		if types[index-1] >= types[index] {
			t.Errorf("expected the types sorted, got %v", types)
		}
	}
}
