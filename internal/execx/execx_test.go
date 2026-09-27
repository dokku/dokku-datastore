package execx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dokku/dokku/plugins/common"
)

func TestTracing(t *testing.T) {
	for value, expected := range map[string]bool{"": false, "1": true, "0": false, "true": false} {
		t.Setenv("DOKKU_TRACE", value)
		if actual := Tracing(); actual != expected {
			t.Errorf("expected %q to be %v, got %v", value, expected, actual)
		}
	}
}

// captureStderr runs a function and returns what it wrote to stderr.
func captureStderr(t *testing.T, run func()) string {
	t.Helper()

	filename := filepath.Join(t.TempDir(), "stderr")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatalf("unable to create %s: %s", filename, err)
	}

	previous := os.Stderr
	os.Stderr = file
	defer func() {
		os.Stderr = previous
	}()

	run()
	file.Close() //nolint:errcheck

	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("unable to read %s: %s", filename, err)
	}

	return string(contents)
}

// What a command streams goes to stderr when stdout is kept for a json document.
func TestRunStreamsStdoutToStderr(t *testing.T) {
	StreamStdoutToStderr(true)
	t.Cleanup(func() {
		StreamStdoutToStderr(false)
	})

	stderr := captureStderr(t, func() {
		if _, err := Run(t.Context(), common.ExecCommandInput{
			Command:      "echo",
			Args:         []string{"streamed"},
			StreamStdout: true,
		}); err != nil {
			t.Fatalf("unable to run: %s", err)
		}
	})

	if !strings.Contains(stderr, "streamed") {
		t.Errorf("expected the streamed stdout on stderr, got %q", stderr)
	}
}

// --trace is handed on as DOKKU_TRACE, which is what makes every command echo
func TestRunEchoesTheCommandWhenTracing(t *testing.T) {
	t.Setenv("DOKKU_TRACE", "1")

	stderr := captureStderr(t, func() {
		if _, err := Run(t.Context(), common.ExecCommandInput{Command: "true"}); err != nil {
			t.Fatalf("unable to run: %s", err)
		}
	})

	if !strings.Contains(stderr, "exec: true") {
		t.Errorf("expected the command to be echoed, got %q", stderr)
	}
}
