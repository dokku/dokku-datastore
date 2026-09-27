package service

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// WaitTimeoutProperty is how long, in seconds, the readiness probe waits for a
// service to answer before giving up on it. Empty leaves it to the host, then
// to the definition, then to the probe's own default.
//
// Read at the moment the probe runs rather than when a container is made, so a
// change lands on the next start without the container being rebuilt.
const WaitTimeoutProperty = "wait-timeout"

// ValidateWaitTimeout reports whether a value is a usable wait timeout. An
// empty value is valid and means unset.
//
// Zero is refused rather than read as unset, so that clearing the setting is
// only ever done one way.
func ValidateWaitTimeout(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return nil
	}

	return fmt.Errorf("invalid %s value %q, must be a whole number of seconds greater than zero", WaitTimeoutProperty, value)
}

// ServiceWaitTimeout is the wait timeout a service was given, empty when it was
// given none, so that what is reported is what was set rather than what was
// inherited.
func ServiceWaitTimeout(s *Datastore, serviceName string) string {
	return strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, WaitTimeoutProperty))
}

// WaitTimeout is the timeout, in seconds, a service's readiness probe is run
// with, zero leaving it to the probe's own default.
//
// The most specific answer wins: the service's own property, then the host's
// variable for the datastore, then the definition's default. A malformed value
// is an error rather than skipped, since skipping it would wait for a time the
// operator never asked for and fail the same way the setting was meant to fix.
func WaitTimeout(s *Datastore, serviceName string) (int, error) {
	if value := ServiceWaitTimeout(s, serviceName); value != "" {
		return parseWaitTimeout(value, fmt.Sprintf("the %s property", WaitTimeoutProperty))
	}

	variable := s.Properties().WaitTimeoutVariable
	if value := strings.TrimSpace(os.Getenv(variable)); value != "" {
		return parseWaitTimeout(value, fmt.Sprintf("the %s environment variable", variable))
	}

	return s.Definition.Dokku.WaitTimeout, nil
}

// parseWaitTimeout reads a wait timeout, naming where it came from when it is
// not one
func parseWaitTimeout(value string, source string) (int, error) {
	if err := ValidateWaitTimeout(value); err != nil {
		return 0, fmt.Errorf("unable to read %s: %w", source, err)
	}

	return strconv.Atoi(value)
}
