package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// RestartPolicyProperty is the docker restart policy a service's containers are
// made with. Empty leaves them on the default, which is what every container
// had before there was anything to say here.
//
// Read at the moment a container is made, like the log properties, so a change
// lands on the next container rather than on the running one.
const RestartPolicyProperty = "restart-policy"

// restartPolicies are the policies docker takes by name, which is the same set
// dokku core accepts for an app's restart-policy
var restartPolicies = []string{"no", "always", "unless-stopped", "on-failure"}

// ValidateRestartPolicy reports whether a value is a restart policy docker will
// take. An empty value is valid and means the default.
//
// The vocabulary is dokku core's, with one difference: core accepts anything
// after on-failure:, and docker refuses to make a container from a retry count
// that is not a whole number, so the service would be left unable to start.
func ValidateRestartPolicy(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	for _, policy := range restartPolicies {
		if value == policy {
			return nil
		}
	}

	if count, found := strings.CutPrefix(value, "on-failure:"); found {
		if retries, err := strconv.Atoi(count); err == nil && retries >= 0 {
			return nil
		}
	}

	return fmt.Errorf("invalid %s value %q, must be one of [%s] or on-failure:<max-retries>", RestartPolicyProperty, value, strings.Join(restartPolicies, ", "))
}

// ServiceRestartPolicy is the restart policy a service was given, empty when it
// was given none. The default is applied where a container is made, so that
// what is reported is what was set rather than what was inherited.
func ServiceRestartPolicy(s *Datastore, serviceName string) string {
	return strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, RestartPolicyProperty))
}
