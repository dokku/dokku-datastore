package backend

import (
	"os"
)

// RawOutput turns off a terminal's output processing until the returned func
// is called, which puts it back the way it was.
//
// A terminal rewrites what is written to it on the way through: by default
// every newline comes out as a carriage return and a newline. ssh -t, which the
// dokku client runs whenever its stdin is a terminal, makes the remote stdout
// one, so a dump written to it comes back with a byte added for every newline
// in it, and a binary dump cannot be read back at all. This is what the bash
// plugins did with stty -opost around an export.
//
// A file that is not a terminal is written to as is, and is left alone.
func RawOutput(file *os.File) (func() error, error) {
	if !HasTerminal(file) {
		return func() error { return nil }, nil
	}

	return rawOutput(int(file.Fd()))
}
