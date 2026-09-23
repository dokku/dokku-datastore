package internal

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// SettableProperties are the properties a service exposes through the set command
var SettableProperties = []string{"initial-network", "post-create-network", "post-start-network", service.KeyserverProperty, service.LogDriverProperty, service.LogOptProperty, service.RestartPolicyProperty}

// InvalidPropertyError reports a property the set command does not manage
func InvalidPropertyError() error {
	return fmt.Errorf("Invalid key specified, valid keys include: %s", strings.Join(SettableProperties, ", ")) //nolint:staticcheck // matches the bash datastore plugins
}

// ValidateProperty reports whether a property is one the set command manages
func ValidateProperty(key string) error {
	if !slices.Contains(SettableProperties, key) {
		return InvalidPropertyError()
	}

	return nil
}

// ValidatePropertyValue reports whether a value is one the property accepts.
//
// The first property validation there has been: every key before these took any
// string at all, because a network name that does not exist is a network that
// does not exist yet and nothing else could be said about it. A log option is
// different - docker refuses to make a container from a malformed one, so the
// service would be left unable to start by a command that said it had succeeded.
func ValidatePropertyValue(key string, value string) error {
	switch key {
	case service.LogDriverProperty:
		return service.ValidateLogDriver(value)
	case service.LogOptProperty:
		return service.ValidateLogOptions(value)
	case service.RestartPolicyProperty:
		return service.ValidateRestartPolicy(value)
	}

	return nil
}

// SetProperty writes a property for a service, or deletes it when the value is
// empty, matching how the bash datastore plugins treat an omitted value
func SetProperty(s *service.Datastore, serviceName string, key string, value string) error {
	if err := ValidateProperty(key); err != nil {
		return err
	}

	if err := ValidatePropertyValue(key, value); err != nil {
		return err
	}

	commandPrefix := s.Properties().CommandPrefix
	if value == "" {
		if err := common.PropertyDelete(commandPrefix, serviceName, key); err != nil {
			return fmt.Errorf("unable to unset %s: %w", key, err)
		}

		return nil
	}

	if err := common.PropertyWrite(commandPrefix, serviceName, key, value); err != nil {
		return fmt.Errorf("unable to set %s: %w", key, err)
	}

	return nil
}
