//go:build !linux && !darwin

package backend

// rawOutput leaves the terminal alone where there is no termios to change.
// Nothing is exported from a dokku host there, so there is no dump to protect.
func rawOutput(fd int) (func() error, error) {
	return func() error { return nil }, nil
}
