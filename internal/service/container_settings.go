package service

import (
	"fmt"
	"strconv"
	"strings"

	units "github.com/docker/go-units"
)

// the container settings create writes into files of their own in the service
// root, named after the info keys that report them and the set keys that change
// them
const (
	ConfigOptionsProperty = "config-options"
	CustomEnvProperty     = "custom-env"
	MemoryProperty        = "memory"
	ShmSizeProperty       = "shm-size"
)

// ValidateMemory reports whether a value is a memory limit the container can be
// made with: a whole number of megabytes, where zero is no limit. Empty is no
// limit as well.
func ValidateMemory(value string) error {
	if value == "" {
		return nil
	}

	if megabytes, err := strconv.Atoi(value); err == nil && megabytes >= 0 {
		return nil
	}

	return fmt.Errorf("invalid %s value %q, must be a whole number of megabytes, or 0 for no limit", MemoryProperty, value)
}

// ValidateShmSize reports whether a value is a shared memory size docker
// accepts. It is read by the parser docker reads --shm-size with, so a value
// accepted here is one the container can be made with. Empty leaves the size to
// docker.
func ValidateShmSize(value string) error {
	if value == "" {
		return nil
	}

	if _, err := units.RAMInBytes(value); err != nil {
		return fmt.Errorf("invalid %s value %q, must be a size such as 64m or 1g", ShmSizeProperty, value)
	}

	return nil
}

// ValidateSingleLine reports whether a value fits the file it is stored in,
// which is read back by its first line alone. The value is left out of the
// error, since it may be a credential.
func ValidateSingleLine(key string, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("invalid %s value, must not contain a newline", key)
	}

	return nil
}
