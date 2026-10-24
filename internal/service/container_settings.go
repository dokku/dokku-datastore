package service

import (
	"fmt"
	"strconv"
	"strings"

	units "github.com/docker/go-units"

	"github.com/dokku/dokku-datastore/internal/definition"
)

// the container settings create writes into files of their own in the service
// root, named after the info keys that report them and the set keys that change
// them
const (
	ConfigOptionsProperty = "config-options"
	CustomEnvProperty     = "custom-env"
	MemoryProperty        = "memory"
	ServiceMemoryProperty = "service-memory"
	ShmSizeProperty       = "shm-size"
)

// ValidateServiceMemory reports whether a value is a memory size a datastore
// can be told to use for itself: a whole number of megabytes greater than zero.
// Empty leaves the size to the datastore.
func ValidateServiceMemory(value string) error {
	if value == "" {
		return nil
	}

	if megabytes, err := strconv.Atoi(value); err == nil && megabytes > 0 {
		return nil
	}

	return fmt.Errorf("invalid %s value %q, must be a whole number of megabytes greater than zero", ServiceMemoryProperty, value)
}

// CheckServiceMemory refuses a service memory for a datastore with no way to
// be told it, which would otherwise be recorded and never reach the datastore
func CheckServiceMemory(d definition.Definition, value string) error {
	if value == "" || d.Dokku.ServiceMemory != nil {
		return nil
	}

	return fmt.Errorf("the %s datastore has no service memory for --%s to set", d.Dokku.Plugin, ServiceMemoryProperty)
}

// DefaultServiceMemory is the service memory a new service is created with:
// the one it was given, or the datastore's default when it was given none. A
// value given empty stays empty, so that a clone of a service without one is
// made without one too.
func DefaultServiceMemory(d definition.Definition, given *string) string {
	if given != nil {
		return *given
	}

	if d.Dokku.ServiceMemory == nil || d.Dokku.ServiceMemory.Default == 0 {
		return ""
	}

	return strconv.Itoa(d.Dokku.ServiceMemory.Default)
}

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
