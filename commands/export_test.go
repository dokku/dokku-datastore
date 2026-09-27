package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file named with --file is written on the dokku host in place of stdout, and
// only replaces what is at the path once the export is committed.
func TestExportDestinationWrites(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.dump")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", existing, err)
	}

	tests := []struct {
		name string
		path string
	}{
		{name: "a new file is written", path: filepath.Join(dir, "new.dump")},
		{name: "an existing file is replaced", path: existing},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			destination, err := exportDestination(test.path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer destination.Abort()

			if _, err := destination.WriteString("known\x00binary"); err != nil {
				t.Fatalf("failed to write: %v", err)
			}
			if err := destination.Commit(); err != nil {
				t.Fatalf("failed to commit: %v", err)
			}

			contents, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatalf("failed to read %s: %v", test.path, err)
			}
			if string(contents) != "known\x00binary" {
				t.Errorf("expected the dump, got %q", contents)
			}
		})
	}
}

// A symlink is written through the way a redirection would, rather than being
// replaced by a regular file.
func TestExportDestinationFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.dump")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", target, err)
	}
	link := filepath.Join(dir, "link.dump")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("failed to link %s: %v", link, err)
	}

	destination, err := exportDestination(link)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer destination.Abort()

	if _, err := destination.WriteString("new"); err != nil {
		t.Fatalf("failed to write: %v", err)
	}
	if err := destination.Commit(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	stat, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("failed to stat %s: %v", link, err)
	}
	if stat.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected %s to still be a symlink", link)
	}

	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("failed to read %s: %v", target, err)
	}
	if string(contents) != "new" {
		t.Errorf("expected the link's target to hold the dump, got %q", contents)
	}
}

// A path that cannot be written is refused before anything is exported, and the
// error names it.
func TestExportDestinationRefuses(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", file, err)
	}

	dangling := filepath.Join(dir, "dangling.dump")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), dangling); err != nil {
		t.Fatalf("failed to link %s: %v", dangling, err)
	}

	readOnly := filepath.Join(dir, "read-only")
	if err := os.Mkdir(readOnly, 0o555); err != nil {
		t.Fatalf("failed to create %s: %v", readOnly, err)
	}
	t.Cleanup(func() { os.Chmod(readOnly, 0o755) }) //nolint:errcheck

	tests := []struct {
		name          string
		path          string
		expectedError string
		skipAsRoot    bool
	}{
		{name: "a directory", path: dir, expectedError: "not a regular file"},
		{name: "a dangling symlink", path: dangling, expectedError: "dangling.dump on the dokku host"},
		{name: "a missing directory", path: filepath.Join(dir, "missing", "data.dump"), expectedError: "missing/data.dump on the dokku host"},
		{name: "a file as the directory", path: filepath.Join(file, "data.dump"), expectedError: "file/data.dump on the dokku host"},
		{name: "a directory that cannot be written", path: filepath.Join(readOnly, "data.dump"), expectedError: "read-only/data.dump on the dokku host", skipAsRoot: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.skipAsRoot && os.Geteuid() == 0 {
				t.Skip("root writes a directory whatever its mode")
			}

			destination, err := exportDestination(test.path)
			if err == nil {
				destination.Abort()
				t.Fatalf("expected an error containing %q, got none", test.expectedError)
			}
			if !strings.Contains(err.Error(), test.expectedError) {
				t.Fatalf("expected an error containing %q, got %q", test.expectedError, err)
			}
		})
	}
}
