package internal

import (
	"fmt"
	"strings"

	"github.com/dokku/dokku-datastore/internal/service"
)

// GlobalProperty is a property the set command manages for every service of a
// datastore at once, rather than for one of them.
//
// These stand in for the environment variables a host used to export for the
// same thing. A host whose sudo drops the caller's environment never hands
// those to the binary, so a setting that has to hold for every command,
// including the ones dokku runs on its own such as install, is kept on disk.
type GlobalProperty struct {
	// Name is the key the set command takes
	Name string

	// Description is what the property does, as the readme lists it
	Description string

	// validate reports whether a value is one the property accepts. Empty,
	// which clears the property, is accepted.
	validate func(value string) error
}

// GlobalProperties are the properties the set command takes with --global, in
// the order the error listing them and the readme name them
var GlobalProperties = []GlobalProperty{
	{
		Name:        service.DisablePullProperty,
		Description: "`true` to stop the plugin from pulling any image it does not already have",
		validate:    validateDisablePull,
	},
	{
		Name:        service.WaitTimeoutProperty,
		Description: "how long, in seconds, every service of the datastore is waited on, unless it sets its own",
		validate:    service.ValidateWaitTimeout,
	},
}

// GlobalPropertyNames are the keys the set command takes with --global, in the
// order they are declared
func GlobalPropertyNames() []string {
	names := make([]string, 0, len(GlobalProperties))
	for _, property := range GlobalProperties {
		names = append(names, property.Name)
	}

	return names
}

// LookupGlobalProperty finds the property the set command manages with
// --global under a key
func LookupGlobalProperty(key string) (GlobalProperty, bool) {
	for _, property := range GlobalProperties {
		if property.Name == key {
			return property, true
		}
	}

	return GlobalProperty{}, false
}

// ValidateGlobalProperty reports whether a key and value are ones the set
// command takes with --global
func ValidateGlobalProperty(key string, value string) error {
	property, ok := LookupGlobalProperty(key)
	if !ok {
		return fmt.Errorf("Invalid global key specified, valid keys include: %s", strings.Join(GlobalPropertyNames(), ", ")) //nolint:staticcheck // matches the per-service error
	}

	if property.validate == nil {
		return nil
	}

	return property.validate(value)
}

// SetGlobalProperty writes a property for every service of a datastore, or
// clears it when the value is empty
func SetGlobalProperty(s *service.Datastore, key string, value string) error {
	value = strings.TrimSpace(value)
	if err := ValidateGlobalProperty(key, value); err != nil {
		return err
	}

	return service.WriteGlobalProperty(s, key, value)
}

// ReadGlobalProperty returns a property set for every service of a datastore,
// empty when nothing is
func ReadGlobalProperty(s *service.Datastore, key string) (string, error) {
	if err := ValidateGlobalProperty(key, ""); err != nil {
		return "", err
	}

	return service.GlobalProperty(s, key), nil
}

// validateDisablePull reports whether a value is one disable-pull takes
func validateDisablePull(value string) error {
	switch value {
	case "", "true", "false":
		return nil
	}

	return fmt.Errorf("invalid %s value %q, must be true or false", service.DisablePullProperty, value)
}
