package internal

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku-datastore/internal/verb"
	"github.com/dokku/dokku/plugins/common"
)

// SettableProperties are the properties a service exposes through the set command
var SettableProperties = []string{"initial-network", "post-create-network", "post-start-network", service.KeyserverProperty, service.BackupStorageClassProperty, service.BackupObjectNameProperty, service.BackupTimestampProperty, service.LogDriverProperty, service.LogOptProperty, service.RestartPolicyProperty, service.WaitTimeoutProperty, service.PortBindAddressProperty, service.PortSourceRangeProperty, service.ExposeHostProperty, service.ExposeModeProperty, service.ExportArgsProperty, service.ImportArgsProperty, service.VolumeTargetsProperty}

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
	case service.BackupStorageClassProperty:
		return service.ValidateBackupStorageClass(value)
	case service.BackupObjectNameProperty:
		return service.ValidateBackupObjectName(value)
	case service.BackupTimestampProperty:
		return service.ValidateBackupTimestamp(value)
	case service.LogDriverProperty:
		return service.ValidateLogDriver(value)
	case service.LogOptProperty:
		return service.ValidateLogOptions(value)
	case service.RestartPolicyProperty:
		return service.ValidateRestartPolicy(value)
	case service.WaitTimeoutProperty:
		return service.ValidateWaitTimeout(value)
	case service.PortBindAddressProperty:
		return service.ValidatePortBindAddress(value)
	case service.ExposeHostProperty:
		return service.ValidateExposeHost(value)
	case service.PortSourceRangeProperty:
		return service.ValidatePortSourceRange(value)
	case service.ExposeModeProperty:
		return service.ValidateExposeMode(value)
	case service.ExportArgsProperty, service.ImportArgsProperty:
		return service.ValidateExtraArgs(key, value)
	case service.VolumeTargetsProperty:
		_, err := service.ParseVolumeTargets(value)
		return err
	}

	return nil
}

// ValidateServicePropertyValue reports whether a value is one the property
// accepts for this particular service, which for some properties depends on
// more than the value: a volume can only be moved if the definition the service
// runs has it, and only to a path nothing the service mounts already holds.
// Clearing a property is always allowed.
func ValidateServicePropertyValue(s *service.Datastore, serviceName string, key string, value string) error {
	if err := ValidatePropertyValue(key, value); err != nil {
		return err
	}

	// a service exposed directly has no ambassador to hold its clients to a
	// range, so the two are refused together whichever is set second
	switch key {
	case service.ExposeModeProperty:
		if err := service.CheckExposeModeSourceRange(value, service.ServicePortSourceRange(s, serviceName)); err != nil {
			return err
		}
	case service.PortSourceRangeProperty:
		if err := service.CheckExposeModeSourceRange(service.ServiceExposeMode(s, serviceName), value); err != nil {
			return err
		}
	}

	if key != service.VolumeTargetsProperty || value == "" {
		return nil
	}

	targets, err := service.ParseVolumeTargets(value)
	if err != nil {
		return err
	}

	if err := service.CheckVolumeTargets(s.Definition, targets); err != nil {
		return err
	}

	mounts, err := service.ServiceMounts(s, serviceName)
	if err != nil {
		return err
	}

	return service.CheckMountTargets(s.Definition, targets, mounts)
}

// SetProperty writes a property for a service, or deletes it when the value is
// empty, matching how the bash datastore plugins treat an omitted value
func SetProperty(s *service.Datastore, serviceName string, key string, value string) error {
	if err := ValidateProperty(key); err != nil {
		return err
	}

	if err := ValidateServicePropertyValue(s, serviceName, key, value); err != nil {
		return err
	}

	// written the way info reports it, so the same targets always read the same
	if key == service.VolumeTargetsProperty && value != "" {
		targets, err := service.ParseVolumeTargets(value)
		if err != nil {
			return err
		}
		value = service.FormatVolumeTargets(targets)
	}

	// refused here as well as when the verb runs, so that a backup is not the
	// first thing to find out the setting can never be used. Clearing it is
	// always allowed, so one written by hand can still be taken away
	if verbName := service.ExtraArgsVerb(key); verbName != "" && value != "" && !s.AcceptsExtraArgs(verbName) {
		return verb.ErrExtraArgsRefused{Plugin: s.Definition.Dokku.Plugin, Name: verbName}
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
