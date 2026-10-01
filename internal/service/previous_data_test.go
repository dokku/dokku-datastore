package service

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The directory old data is kept in names the definition that reads it and when
// it was moved, in a form that sorts by time and that a shell glob or a mount
// takes literally.
func TestPreviousDataName(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 30, 5, 0, time.FixedZone("EDT", -4*60*60))
	if actual := PreviousDataName("postgres-17", now); actual != "data.postgres-17.20261001T163005" {
		t.Errorf("expected the time in utc, got %q", actual)
	}
}

// Moving data aside leaves an empty data directory in its place, and putting it
// back restores the data exactly, so a migration that fails loses nothing.
func TestMoveDataAsideAndBack(t *testing.T) {
	s := postgresDatastore(t)
	root := withServiceRoot(t, s, "lollipop")

	data := filepath.Join(root, "data")
	if err := os.MkdirAll(data, 0755); err != nil {
		t.Fatalf("unable to create the data directory: %s", err)
	}
	if err := os.WriteFile(filepath.Join(data, "PG_VERSION"), []byte("17\n"), 0600); err != nil {
		t.Fatalf("unable to write the data: %s", err)
	}

	name := PreviousDataName("postgres-17", time.Now())
	if err := MoveDataAside(s, "lollipop", name); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	entries, err := os.ReadDir(data)
	if err != nil {
		t.Fatalf("expected an empty data directory, got %s", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected an empty data directory, got %v", entries)
	}

	if _, err := os.Stat(filepath.Join(root, name, "PG_VERSION")); err != nil {
		t.Errorf("expected the data to be kept aside: %s", err)
	}

	previous, err := PreviousDataDirectories(root)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !slices.Equal(previous, []string{name}) {
		t.Errorf("expected %s to be listed, got %v", name, previous)
	}

	// moving aside onto a directory already there would merge two upgrades'
	// data, so it is refused
	if err := MoveDataAside(s, "lollipop", name); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected a second move onto %s to be refused, got %v", name, err)
	}

	if err := RestoreDataAside(s, "lollipop", name); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	contents, err := os.ReadFile(filepath.Join(data, "PG_VERSION"))
	if err != nil || string(contents) != "17\n" {
		t.Errorf("expected the data back where it was, got %q (%v)", contents, err)
	}

	if previous, _ := PreviousDataDirectories(root); len(previous) != 0 {
		t.Errorf("expected nothing kept aside, got %v", previous)
	}
}

// Only the directories an upgrade names are previous data: the data directory
// itself, the certificates and a file that happens to share the prefix are not.
func TestPreviousDataDirectoriesOnlyListsWhatAnUpgradeKept(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"data", "certs", "data.postgres-18.20261002T000000", "data.postgres-17.20261001T000000"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0755); err != nil {
			t.Fatalf("unable to create %s: %s", directory, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "data.txt"), nil, 0600); err != nil {
		t.Fatalf("unable to write a file: %s", err)
	}

	actual, err := PreviousDataDirectories(root)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []string{"data.postgres-17.20261001T000000", "data.postgres-18.20261002T000000"}
	if !slices.Equal(actual, expected) {
		t.Errorf("expected %v, got %v", expected, actual)
	}

	if missing, err := PreviousDataDirectories(filepath.Join(root, "missing")); err != nil || len(missing) != 0 {
		t.Errorf("expected a missing root to have nothing kept aside, got %v (%v)", missing, err)
	}
}
