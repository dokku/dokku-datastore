//go:build linux || darwin

package backend

import (
	"io"
	"os"
	"testing"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// writeThrough writes to the terminal side of a pty and reads back what comes
// out of the other side, which is what ssh would send to the client
func writeThrough(t *testing.T, ptmx *os.File, tty *os.File, written string, expected int) string {
	t.Helper()

	if _, err := tty.WriteString(written); err != nil {
		t.Fatalf("unable to write to the terminal: %v", err)
	}

	read := make([]byte, expected)
	if _, err := io.ReadFull(ptmx, read); err != nil {
		t.Fatalf("unable to read from the terminal: %v", err)
	}

	return string(read)
}

func TestRawOutputWritesNewlinesThroughATerminalUnchanged(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("unable to open a pty: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	// what a terminal does by default, and what corrupted a dump through ssh -t
	if got := writeThrough(t, ptmx, tty, "a\n", 3); got != "a\r\n" {
		t.Fatalf("expected a terminal to add a carriage return by default, got %q", got)
	}

	restore, err := RawOutput(tty)
	if err != nil {
		t.Fatalf("unable to turn off output processing: %v", err)
	}

	if got := writeThrough(t, ptmx, tty, "b\nc\n", 4); got != "b\nc\n" {
		t.Errorf("expected newlines to come through unchanged, got %q", got)
	}

	if err := restore(); err != nil {
		t.Fatalf("unable to restore output processing: %v", err)
	}

	if got := writeThrough(t, ptmx, tty, "d\n", 3); got != "d\r\n" {
		t.Errorf("expected the carriage return back once restored, got %q", got)
	}
}

func TestRawOutputRestoresTheTerminalAsItWas(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("unable to open a pty: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	before, err := unix.IoctlGetTermios(int(tty.Fd()), ioctlReadTermios)
	if err != nil {
		t.Fatalf("unable to read the terminal's settings: %v", err)
	}

	restore, err := RawOutput(tty)
	if err != nil {
		t.Fatalf("unable to turn off output processing: %v", err)
	}

	during, err := unix.IoctlGetTermios(int(tty.Fd()), ioctlReadTermios)
	if err != nil {
		t.Fatalf("unable to read the terminal's settings: %v", err)
	}

	if during.Oflag&unix.OPOST != 0 {
		t.Error("expected output processing to be off")
	}

	if during.Lflag != before.Lflag || during.Iflag != before.Iflag {
		t.Error("expected only the output flags to change")
	}

	if err := restore(); err != nil {
		t.Fatalf("unable to restore output processing: %v", err)
	}

	after, err := unix.IoctlGetTermios(int(tty.Fd()), ioctlReadTermios)
	if err != nil {
		t.Fatalf("unable to read the terminal's settings: %v", err)
	}

	if after.Oflag != before.Oflag {
		t.Errorf("expected output flags %#x after restoring, got %#x", before.Oflag, after.Oflag)
	}
}

func TestRawOutputLeavesAFileThatIsNotATerminalAlone(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("unable to open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("unable to create a pipe: %v", err)
	}
	defer reader.Close()
	defer writer.Close()

	for name, file := range map[string]*os.File{os.DevNull: devNull, "a pipe": writer} {
		restore, err := RawOutput(file)
		if err != nil {
			t.Errorf("expected nothing to be changed on %s, got %v", name, err)
			continue
		}

		if err := restore(); err != nil {
			t.Errorf("expected nothing to restore on %s, got %v", name, err)
		}
	}
}
