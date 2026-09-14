package internal

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dokku/dokku-datastore/internal/datastores"
	"github.com/dokku/dokku/plugins/common"
)

// SettableProperties are the properties a service exposes through the set command
var SettableProperties = []string{"initial-network", "post-create-network", "post-start-network"}

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

// SetProperty writes a property for a service, or deletes it when the value is
// empty, matching how the bash datastore plugins treat an omitted value
func SetProperty(s datastores.Datastore, serviceName string, key string, value string) error {
	if err := ValidateProperty(key); err != nil {
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
