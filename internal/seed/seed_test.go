package seed

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/dokku/dokku-datastore/internal/render"
)

// seedInput owns the files as the user running the test, because the common
// package falls back to the dokku user when neither is named and no such user
// exists on a developer's machine.
func seedInput(t *testing.T, configs []render.Config) Input {
	t.Helper()

	current, err := user.Current()
	if err != nil {
		t.Fatalf("failed to look up the current user: %s", err)
	}

	group, err := user.LookupGroupId(current.Gid)
	if err != nil {
		t.Fatalf("failed to look up the current group: %s", err)
	}

	return Input{Configs: configs, Username: current.Username, GroupName: group.Name}
}

func TestConfigsWritesAMissingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config", "redis.conf")

	err := Configs(seedInput(t, []render.Config{
		{Path: path, Content: "requirepass hunter2\n", Mode: 0644},
	}))
	if err != nil {
		t.Fatalf("unable to seed: %s", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("unable to read the seeded file: %s", err)
	}

	if expected := "requirepass hunter2\n"; string(contents) != expected {
		t.Errorf("expected %q, got %q", expected, string(contents))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("unable to stat the seeded file: %s", err)
	}

	if info.Mode().Perm() != 0644 {
		t.Errorf("expected mode 0644, got %v", info.Mode().Perm())
	}
}

// The whole reason configs deviate from compose semantics: a docker config is
// re-projected on every start, but an operator edits a datastore's config file
// and expects the edit to be there after a restart.
func TestConfigsLeavesAnExistingFileAlone(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "redis.conf")

	if err := os.WriteFile(path, []byte("maxmemory 2gb\n"), 0644); err != nil {
		t.Fatalf("unable to write the operator's file: %s", err)
	}

	err := Configs(seedInput(t, []render.Config{
		{Path: path, Content: "requirepass hunter2\n", Mode: 0644},
	}))
	if err != nil {
		t.Fatalf("unable to seed: %s", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("unable to read the file: %s", err)
	}

	if expected := "maxmemory 2gb\n"; string(contents) != expected {
		t.Errorf("the operator's file was clobbered with %q", string(contents))
	}
}

// The create path already made the service folders with the mode and ownership
// they need, and re-applying one here would quietly narrow them.
func TestConfigsDoesNotRemodeAnExistingDirectory(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "config")

	if err := os.Mkdir(directory, 0775); err != nil {
		t.Fatalf("unable to create the directory: %s", err)
	}

	if err := os.Chmod(directory, 0775); err != nil {
		t.Fatalf("unable to set the directory mode: %s", err)
	}

	err := Configs(seedInput(t, []render.Config{
		{Path: filepath.Join(directory, "redis.conf"), Content: "requirepass hunter2\n", Mode: 0644},
	}))
	if err != nil {
		t.Fatalf("unable to seed: %s", err)
	}

	info, err := os.Stat(directory)
	if err != nil {
		t.Fatalf("unable to stat the directory: %s", err)
	}

	if info.Mode().Perm() != 0775 {
		t.Errorf("expected the directory to keep mode 0775, got %v", info.Mode().Perm())
	}
}

func TestConfigsCreatesAMissingDirectory(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "config", "conf.d")

	err := Configs(seedInput(t, []render.Config{
		{Path: filepath.Join(directory, "tuning.conf"), Content: "maxmemory 2gb\n", Mode: 0644},
	}))
	if err != nil {
		t.Fatalf("unable to seed: %s", err)
	}

	info, err := os.Stat(directory)
	if err != nil {
		t.Fatalf("unable to stat the directory: %s", err)
	}

	// applied explicitly, because MkdirAll is subject to the umask
	if info.Mode().Perm() != 0775 {
		t.Errorf("expected mode 0775, got %v", info.Mode().Perm())
	}
}

// MkdirAll creates the intermediate directories too, and one of those left
// root owned is a directory the dokku user cannot write into later.
func TestConfigsOwnsEveryDirectoryItCreates(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "config", "conf.d", "tuning")

	err := Configs(seedInput(t, []render.Config{
		{Path: filepath.Join(directory, "memory.conf"), Content: "maxmemory 2gb\n", Mode: 0644},
	}))
	if err != nil {
		t.Fatalf("unable to seed: %s", err)
	}

	for _, created := range []string{
		filepath.Join(root, "config"),
		filepath.Join(root, "config", "conf.d"),
		directory,
	} {
		info, err := os.Stat(created)
		if err != nil {
			t.Fatalf("unable to stat %s: %s", created, err)
		}

		if info.Mode().Perm() != 0775 {
			t.Errorf("expected %s to have mode 0775, got %v", created, info.Mode().Perm())
		}
	}
}
