package commands

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file named with --file is read on the dokku host in place of stdin, which is
// how a dump already there is imported over ssh.
func TestImportSourceFile(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, "data.dump")
	if err := os.WriteFile(dump, []byte("known\x00binary"), 0o600); err != nil {
		t.Fatalf("failed to write dump: %v", err)
	}

	stdin := devNull(t)

	tests := []struct {
		name          string
		path          string
		expected      string
		expectedError string
	}{
		{name: "a regular file is read back", path: dump, expected: "known\x00binary"},
		{name: "a missing file names the path", path: filepath.Join(dir, "missing.dump"), expectedError: "missing.dump on the dokku host"},
		{name: "a directory is refused", path: dir, expectedError: "not a regular file"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader, err := importSource(test.path, stdin)
			if test.expectedError != "" {
				if err == nil {
					reader.Close() //nolint:errcheck
					t.Fatalf("expected an error containing %q, got none", test.expectedError)
				}
				if !strings.Contains(err.Error(), test.expectedError) {
					t.Fatalf("expected an error containing %q, got %q", test.expectedError, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer reader.Close() //nolint:errcheck

			actual, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("failed to read: %v", err)
			}
			if string(actual) != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// Without --file nothing changes: a terminal is refused, so the service is not
// truncated when nothing was piped in, and anything else is read.
func TestImportSourceStdin(t *testing.T) {
	t.Run("a char device is refused", func(t *testing.T) {
		_, err := importSource("", devNull(t))
		if err == nil || err.Error() != "No data provided on stdin." {
			t.Fatalf("expected the no data error, got %v", err)
		}
	})

	t.Run("a pipe is read", func(t *testing.T) {
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatalf("failed to create pipe: %v", err)
		}
		defer read.Close() //nolint:errcheck

		if _, err := write.WriteString("piped"); err != nil {
			t.Fatalf("failed to write to pipe: %v", err)
		}
		write.Close() //nolint:errcheck

		reader, err := importSource("", read)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		actual, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("failed to read: %v", err)
		}
		if string(actual) != "piped" {
			t.Errorf("expected %q, got %q", "piped", actual)
		}
	})
}

// devNull is a char device, which is what stdin is when nothing was piped in.
func devNull(t *testing.T) *os.File {
	t.Helper()
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("failed to open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { file.Close() }) //nolint:errcheck
	return file
}
