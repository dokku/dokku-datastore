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

// A dump streamed to a writer reaches it unchanged, and no copy is kept on the
// way, which for a large dump would be held in memory in full.
func TestRunWithoutABufferStreamsToTheWriterOnly(t *testing.T) {
	var written strings.Builder
	result, err := Run(t.Context(), common.ExecCommandInput{
		Command:            "printf",
		Args:               []string{"a\\nb\\n"},
		StdoutWriter:       &written,
		DisableStdioBuffer: true,
	})
	if err != nil {
		t.Fatalf("unable to run the command: %s", err)
	}

	if written.String() != "a\nb\n" {
		t.Errorf("expected the writer to get %q, got %q", "a\nb\n", written.String())
	}

	if result.Stdout != "" {
		t.Errorf("expected no copy of stdout to be kept, got %q", result.Stdout)
	}
}

// Without a buffer there is no stderr to repeat in the error, and what the
// command said already went to the writer it was given.
func TestRunWithoutABufferFailsWithoutStderr(t *testing.T) {
	var stderr strings.Builder
	_, err := Run(t.Context(), common.ExecCommandInput{
		Command:            "sh",
		Args:               []string{"-c", "echo broken >&2; exit 1"},
		StderrWriter:       &stderr,
		DisableStdioBuffer: true,
	})
	if err == nil {
		t.Fatal("expected the command to fail")
	}

	if err.Error() != "command exited non-zero" {
		t.Errorf("expected a plain error, got %q", err.Error())
	}

	if stderr.String() != "broken\n" {
		t.Errorf("expected stderr to reach its writer, got %q", stderr.String())
	}
}
