//go:build linux || darwin

package backend

import (
	"golang.org/x/sys/unix"
)

// rawOutput clears OPOST on a terminal, which turns off every output
// translation it makes, and returns a func putting back what it was.
func rawOutput(fd int) (func() error, error) {
	termios, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return nil, err
	}

	original := *termios
	termios.Oflag &^= unix.OPOST
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, termios); err != nil {
		return nil, err
	}

	return func() error {
		return unix.IoctlSetTermios(fd, ioctlWriteTermios, &original)
	}, nil
}
