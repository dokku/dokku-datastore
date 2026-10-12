package internal

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku-datastore/internal/verb"
	"github.com/dokku/dokku/plugins/common"
)

// PropertyEffect is when a property that was just set reaches the service
type PropertyEffect int

const (
	// EffectImmediately is a property read each time it is used: by the next
	// backup, export, import, wait or report, or by the crontab set rewrites
	EffectImmediately PropertyEffect = iota

	// EffectRebuild is a property read when the container is made. Restart
	// keeps the container a service has, so it takes a stop and a start.
	EffectRebuild

	// EffectReexpose is a property read when the exposed ports are published,
	// which reexpose does again for a service that is exposed
	EffectReexpose
)

// SettableProperty is a property the set command manages: what it is, where it
// is kept and when a change to it is picked up
type SettableProperty struct {
	// Name is the key the set command takes
	Name string

	// Description is what the property does, as the readme lists it
	Description string

	// TakesEffect is when a change reaches the service
	TakesEffect PropertyEffect

	// Sensitive is a credential, whose value is never printed back
	Sensitive bool

	// InfoKey is the info key that reports the property, when it is not the
	// property's own name. A credential is reported as a fingerprint.
	InfoKey string

	// Backup is part of the backup settings, which the readme leaves out for a
	// datastore that cannot be backed up
	Backup bool

	// requiresBackups refuses a value for a datastore that cannot be backed
	// up, the way the backup command it stands in for is refused
	requiresBackups bool

	// validate reports whether a value is one the property accepts, whatever
	// service it is set on. Empty, which clears the property, is accepted.
	validate func(value string) error

	// validateService reports whether a value is one the property accepts for
	// a particular service
	validateService func(s *service.Datastore, serviceName string, value string) error

	// write stores the value, clearing the property when it is empty. A
	// property without one is a dokku property.
	write func(s *service.Datastore, serviceName string, value string) error

	// apply has a change take effect where it is not read again on its own
	apply func(ctx context.Context, s *service.Datastore, serviceName string) error
}

// ReportedBy is the info key that reports the property
func (p SettableProperty) ReportedBy() string {
	if p.InfoKey != "" {
		return p.InfoKey
	}

	return p.Name
}

// Warning is what the set command warns of once the property is set, for one
// that does not reach the service on its own, and empty for one that does
func (p SettableProperty) Warning(commandPrefix string, serviceName string) string {
	switch p.TakesEffect {
	case EffectRebuild:
		return fmt.Sprintf("%s takes effect the next time the container is built: run `dokku %s:stop %s` then `dokku %s:start %s`", p.Name, commandPrefix, serviceName, commandPrefix, serviceName)
	case EffectReexpose:
		return fmt.Sprintf("%s takes effect on an exposed service once it is reexposed: run `dokku %s:reexpose %s`", p.Name, commandPrefix, serviceName)
	}

	return ""
}

// SettableProperties are the properties a service exposes through the set
// command, in the order the error listing them and the readme name them
var SettableProperties = []SettableProperty{
	{
		Name:        "initial-network",
		Description: "the network the service container is attached to when it is made",
		TakesEffect: EffectRebuild,
	},
	{
		Name:        "post-create-network",
		Description: "a comma-separated list of networks the container is attached to once it is made",
		TakesEffect: EffectRebuild,
	},
	{
		Name:        "post-start-network",
		Description: "a comma-separated list of networks the container is attached to once it is started",
		TakesEffect: EffectRebuild,
	},
	{
		Name:        service.KeyserverProperty,
		Description: "the keyserver a public key for backup encryption is fetched from",
		Backup:      true,
	},
	{
		Name:        service.BackupStorageClassProperty,
		Description: "the s3 storage class backups are uploaded with",
		Backup:      true,
		validate:    service.ValidateBackupStorageClass,
	},
	{
		Name:        service.BackupObjectNameProperty,
		Description: "the name backups are uploaded under",
		Backup:      true,
		validate:    service.ValidateBackupObjectName,
	},
	{
		Name:        service.BackupTimestampProperty,
		Description: "`false` to upload every backup to the same key, without a timestamp",
		Backup:      true,
		validate:    service.ValidateBackupTimestamp,
	},
	{
		Name:        service.BackupMailtoProperty,
		Description: "who cron mails the output of scheduled backups to",
		Backup:      true,
		validate:    service.ValidateBackupMailto,
		apply:       regenerateCrontabIfScheduled,
	},
	{
		Name:            backupAccessKeyIDProperty,
		Description:     "the access key id backups authenticate with",
		Sensitive:       true,
		InfoKey:         "backup-access-key-id-fingerprint",
		Backup:          true,
		requiresBackups: true,
		validate:        singleLine(backupAccessKeyIDProperty),
		write:           backupAuthWriter(accessKeyIDFile),
	},
	{
		Name:            backupSecretAccessKeyProperty,
		Description:     "the secret access key backups authenticate with",
		Sensitive:       true,
		InfoKey:         "backup-secret-access-key-fingerprint",
		Backup:          true,
		requiresBackups: true,
		validate:        singleLine(backupSecretAccessKeyProperty),
		write:           backupAuthWriter(secretAccessKeyFile),
	},
	{
		Name:            "backup-default-region",
		Description:     "the region backups authenticate against",
		Backup:          true,
		requiresBackups: true,
		validate:        singleLine("backup-default-region"),
		write:           backupAuthWriter(defaultRegionFile),
	},
	{
		Name:            "backup-signature-version",
		Description:     "the signature version backups authenticate with",
		Backup:          true,
		requiresBackups: true,
		validate:        singleLine("backup-signature-version"),
		write:           backupAuthWriter(signatureVersionFile),
	},
	{
		Name:            "backup-endpoint-url",
		Description:     "the endpoint of an s3 compatible service backups are shipped to",
		Backup:          true,
		requiresBackups: true,
		validate:        ValidateEndpointURL,
		write:           backupAuthWriter(endpointURLFile),
	},
	{
		Name:            backupEncryptionPassphraseProperty,
		Description:     "the passphrase backups are encrypted with",
		Sensitive:       true,
		InfoKey:         "backup-encryption-fingerprint",
		Backup:          true,
		requiresBackups: true,
		validate:        singleLine(backupEncryptionPassphraseProperty),
		write:           backupEncryptionWriter(encryptionKeyFile),
	},
	{
		Name:            "backup-public-key-id",
		Description:     "the gpg public key id backups are encrypted with",
		Backup:          true,
		requiresBackups: true,
		validate:        singleLine("backup-public-key-id"),
		write:           backupEncryptionWriter(publicKeyIDFile),
	},
	{
		Name:            BackupBucketProperty,
		Description:     "the bucket the scheduled backup is shipped to",
		Backup:          true,
		requiresBackups: true,
		validate: func(value string) error {
			if value == "" {
				return nil
			}
			return ValidateBucketName(value)
		},
		validateService: validateBackupBucket,
		apply:           regenerateCrontabIfScheduled,
	},
	{
		Name:            BackupUseIAMProperty,
		Description:     "`true` to have the scheduled backup authenticate with an instance role",
		Backup:          true,
		requiresBackups: true,
		validate:        validateBackupUseIAM,
		validateService: requireBackupSchedule(BackupUseIAMProperty),
		write:           writeBackupUseIAM,
		apply:           regenerateCrontabIfScheduled,
	},
	{
		Name:        service.LogDriverProperty,
		Description: "the docker logging driver the container is run with",
		TakesEffect: EffectRebuild,
		validate:    service.ValidateLogDriver,
	},
	{
		Name:        service.LogOptProperty,
		Description: "a comma-separated list of docker log options the container is run with",
		TakesEffect: EffectRebuild,
		validate:    service.ValidateLogOptions,
	},
	{
		Name:        service.RestartPolicyProperty,
		Description: "the docker restart policy the container is run with",
		TakesEffect: EffectRebuild,
		validate:    service.ValidateRestartPolicy,
	},
	{
		Name:        service.WaitTimeoutProperty,
		Description: "the seconds the service is waited on to become ready",
		validate:    service.ValidateWaitTimeout,
	},
	{
		Name:        service.PortBindAddressProperty,
		Description: "the address exposed ports without one of their own are published on",
		TakesEffect: EffectReexpose,
		validate:    service.ValidatePortBindAddress,
	},
	{
		Name:        service.PortSourceRangeProperty,
		Description: "the only IP address or CIDR the exposed ports accept clients from",
		TakesEffect: EffectReexpose,
		validate:    service.ValidatePortSourceRange,
		validateService: func(s *service.Datastore, serviceName string, value string) error {
			return service.CheckExposeModeSourceRange(service.ServiceExposeMode(s, serviceName), value)
		},
	},
	{
		Name:        service.ExposeHostProperty,
		Description: "the host the exposed dsn names",
		validate:    service.ValidateExposeHost,
	},
	{
		Name:        service.ExposeModeProperty,
		Description: "`direct` to publish the exposed ports on the service container rather than through an ambassador",
		TakesEffect: EffectReexpose,
		validate:    service.ValidateExposeMode,
		validateService: func(s *service.Datastore, serviceName string, value string) error {
			return service.CheckExposeModeSourceRange(value, service.ServicePortSourceRange(s, serviceName))
		},
	},
	{
		Name:            service.ExportArgsProperty,
		Description:     "extra arguments every export of the service is run with",
		validate:        extraArgsValidator(service.ExportArgsProperty),
		validateService: extraArgsAccepted(service.ExportArgsProperty),
	},
	{
		Name:            service.ImportArgsProperty,
		Description:     "extra arguments every import into the service is run with",
		validate:        extraArgsValidator(service.ImportArgsProperty),
		validateService: extraArgsAccepted(service.ImportArgsProperty),
	},
	{
		Name:        service.VolumeTargetsProperty,
		Description: "the container paths the definition's volumes are mounted at in place of its own",
		TakesEffect: EffectRebuild,
		validate: func(value string) error {
			_, err := service.ParseVolumeTargets(value)
			return err
		},
		validateService: validateVolumeTargets,
		write:           writeVolumeTargets,
	},
	{
		Name:        service.ConfigOptionsProperty,
		Description: "extra arguments for the process the service container runs",
		TakesEffect: EffectRebuild,
		validate:    singleLine(service.ConfigOptionsProperty),
		write: serviceFileWriter(func(files service.ServiceFiles) string { return files.ConfigOptions }, service.PrivateFileMode, func(value string) string {
			return value
		}),
	},
	{
		Name:        service.CustomEnvProperty,
		Description: "semicolon-delimited environment variables the container is run with",
		TakesEffect: EffectRebuild,
		validate:    singleLine(service.CustomEnvProperty),
		// stored one per line, and given semicolon delimited, the way create
		// takes it
		write: serviceFileWriter(func(files service.ServiceFiles) string { return files.Env }, service.PrivateFileMode, func(value string) string {
			return strings.Join(strings.Split(value, ";"), "\n")
		}),
	},
	{
		Name:        service.MemoryProperty,
		Description: "the container memory limit in megabytes, `0` for no limit",
		TakesEffect: EffectRebuild,
		validate:    service.ValidateMemory,
		// cleared to what create writes for no limit
		write: serviceFileWriter(func(files service.ServiceFiles) string { return files.Memory }, 0644, func(value string) string {
			if value == "" {
				return "0"
			}
			return value
		}),
	},
	{
		Name:        service.ShmSizeProperty,
		Description: "the shared memory size of the container, such as `64m`",
		TakesEffect: EffectRebuild,
		validate:    service.ValidateShmSize,
		write: serviceFileWriter(func(files service.ServiceFiles) string { return files.ShmSize }, 0644, func(value string) string {
			return value
		}),
	},
}

// the set keys for backup settings that have no property of their own to be
// named after
const (
	backupAccessKeyIDProperty          = "backup-access-key-id"
	backupSecretAccessKeyProperty      = "backup-secret-access-key"
	backupEncryptionPassphraseProperty = "backup-encryption-passphrase"
)

// ownedElsewhere are the settings a service has that the set command refuses,
// each with the reason and the command that changes it instead
var ownedElsewhere = map[string]string{
	"image":                "the image a service runs is changed with upgrade",
	"image-version":        "the image version a service runs is changed with upgrade",
	"definition":           "the definition a service runs on is changed with upgrade",
	"password":             "the password is kept by the datastore itself, and changing the file holding it would not change it",
	"root-password":        "the root password is kept by the datastore itself, and changing the file holding it would not change it",
	"mounts":               "mounts are changed with mount and unmount",
	BackupScheduleProperty: "a scheduled backup is changed with backup-schedule and backup-unschedule",
}

// SettablePropertyNames are the keys the set command takes, in the order they
// are declared
func SettablePropertyNames() []string {
	names := make([]string, 0, len(SettableProperties))
	for _, property := range SettableProperties {
		names = append(names, property.Name)
	}

	return names
}

// LookupSettableProperty finds the property the set command manages under a key
func LookupSettableProperty(key string) (SettableProperty, bool) {
	for _, property := range SettableProperties {
		if property.Name == key {
			return property, true
		}
	}

	return SettableProperty{}, false
}

// InvalidPropertyError reports a property the set command does not manage
func InvalidPropertyError() error {
	return fmt.Errorf("Invalid key specified, valid keys include: %s", strings.Join(SettablePropertyNames(), ", ")) //nolint:staticcheck // matches the bash datastore plugins
}

// ValidateProperty reports whether a property is one the set command manages.
// A setting changed by another command is refused with the name of that command.
func ValidateProperty(key string) error {
	if reason, ok := ownedElsewhere[key]; ok {
		return fmt.Errorf("%s cannot be changed with set: %s", key, reason)
	}

	if _, ok := LookupSettableProperty(key); !ok {
		return InvalidPropertyError()
	}

	return nil
}

// ValidatePropertyValue reports whether a value is one the property accepts.
//
// A network name that does not exist is a network that does not exist yet, so
// some properties take any string at all. Most do not: docker refuses to make a
// container from a malformed log option, and the service would be left unable
// to start by a command that said it had succeeded.
func ValidatePropertyValue(key string, value string) error {
	property, ok := LookupSettableProperty(key)
	if !ok || property.validate == nil {
		return nil
	}

	return property.validate(value)
}

// ValidateServicePropertyValue reports whether a value is one the property
// accepts for this particular service, which for some properties depends on
// more than the value: a volume can only be moved if the definition the service
// runs has it, and a backup setting only means something for a datastore that
// can be backed up.
func ValidateServicePropertyValue(s *service.Datastore, serviceName string, key string, value string) error {
	if err := ValidatePropertyValue(key, value); err != nil {
		return err
	}

	property, ok := LookupSettableProperty(key)
	if !ok {
		return nil
	}

	// clearing is always allowed, so one written by hand can still be taken away
	if property.requiresBackups && value != "" && !service.Implements(s, "backup") {
		return fmt.Errorf("the %s datastore does not support backups, so %s cannot be set", s.Definition.Dokku.Plugin, key)
	}

	if property.validateService == nil {
		return nil
	}

	return property.validateService(s, serviceName, value)
}

// SetProperty writes a property for a service, or clears it when the value is
// empty, matching how the bash datastore plugins treat an omitted value
func SetProperty(s *service.Datastore, serviceName string, key string, value string) error {
	if err := ValidateProperty(key); err != nil {
		return err
	}

	if err := ValidateServicePropertyValue(s, serviceName, key, value); err != nil {
		return err
	}

	property, _ := LookupSettableProperty(key)
	if property.write != nil {
		return property.write(s, serviceName, value)
	}

	return writeProperty(s, serviceName, key, value)
}

// ApplyProperty has a property that was just set take effect where it is not
// read again on its own, such as the dokku crontab a scheduled backup is in
func ApplyProperty(ctx context.Context, s *service.Datastore, serviceName string, key string) error {
	property, ok := LookupSettableProperty(key)
	if !ok || property.apply == nil {
		return nil
	}

	return property.apply(ctx, s, serviceName)
}

// writeProperty writes a dokku property, or deletes it when the value is empty
func writeProperty(s *service.Datastore, serviceName string, key string, value string) error {
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

// singleLine refuses a value its file could not hold
func singleLine(key string) func(value string) error {
	return func(value string) error {
		return service.ValidateSingleLine(key, value)
	}
}

// serviceFileWriter writes one of the files create keeps a container setting
// in. Create always writes them, so a cleared setting is a rewritten file
// rather than a missing one, written as format renders it.
func serviceFileWriter(file func(service.ServiceFiles) string, mode os.FileMode, format func(value string) string) func(s *service.Datastore, serviceName string, value string) error {
	return func(s *service.Datastore, serviceName string, value string) error {
		return writeServiceFile(file(service.Files(s, serviceName)), format(value), mode)
	}
}

// backupAuthWriter writes one of the files backup-auth stores the credentials
// backups are shipped with in, or removes it when the value is empty
func backupAuthWriter(name string) func(s *service.Datastore, serviceName string, value string) error {
	return func(s *service.Datastore, serviceName string, value string) error {
		return writeBackupSetting(service.Folders(s, serviceName).Backup, name, value)
	}
}

// backupEncryptionWriter writes one of the files the backup encryption commands
// store how backups are encrypted in, or removes it when the value is empty
func backupEncryptionWriter(name string) func(s *service.Datastore, serviceName string, value string) error {
	return func(s *service.Datastore, serviceName string, value string) error {
		return writeBackupSetting(service.Folders(s, serviceName).BackupEncryption, name, value)
	}
}

// writeBackupSetting writes a backup settings file, or removes it when the
// value is empty
func writeBackupSetting(folder string, name string, value string) error {
	if value == "" {
		return removeBackupFile(folder, name)
	}

	return writeBackupFile(folder, name, value)
}

// requireBackupSchedule refuses a value for a schedule setting on a service with
// no scheduled backup, since the setting would have nothing to apply to.
// Clearing is always allowed.
func requireBackupSchedule(key string) func(s *service.Datastore, serviceName string, value string) error {
	return func(s *service.Datastore, serviceName string, value string) error {
		if value == "" {
			return nil
		}

		if _, ok := ReadBackupSchedule(s, serviceName); !ok {
			return fmt.Errorf("There is no scheduled backup for %s, so %s cannot be set. Schedule one with backup-schedule", serviceName, key) //nolint:staticcheck // matches the bash datastore plugins
		}

		return nil
	}
}

// validateBackupBucket refuses a bucket for a service with no scheduled backup,
// and refuses to clear the one a scheduled backup is shipped to, since its
// crontab entry cannot be written without it
func validateBackupBucket(s *service.Datastore, serviceName string, value string) error {
	if value != "" {
		return requireBackupSchedule(BackupBucketProperty)(s, serviceName, value)
	}

	if _, ok := ReadBackupSchedule(s, serviceName); ok {
		return fmt.Errorf("%s cannot be cleared while %s has a scheduled backup, remove it with backup-unschedule", BackupBucketProperty, serviceName)
	}

	return nil
}

// validateBackupUseIAM reports whether a value says whether the scheduled backup
// authenticates with an instance role
func validateBackupUseIAM(value string) error {
	switch value {
	case "", "true", "false":
		return nil
	}

	return fmt.Errorf("invalid %s value %q, must be true or false", BackupUseIAMProperty, value)
}

// writeBackupUseIAM records whether the scheduled backup authenticates with an
// instance role, the way backup-schedule records it: only when it does
func writeBackupUseIAM(s *service.Datastore, serviceName string, value string) error {
	if value != "true" {
		value = ""
	}

	return writeProperty(s, serviceName, BackupUseIAMProperty, value)
}

// regenerateCrontabIfScheduled has dokku write its crontab again for a service
// with a scheduled backup, which is where the backup settings the crontab
// carries are read
func regenerateCrontabIfScheduled(ctx context.Context, s *service.Datastore, serviceName string) error {
	if _, ok := ReadBackupSchedule(s, serviceName); !ok {
		return nil
	}

	return regenerateCrontab(ctx)
}

// extraArgsValidator reports whether a value splits into the arguments an
// export or import is run with
func extraArgsValidator(key string) func(value string) error {
	return func(value string) error {
		return service.ValidateExtraArgs(key, value)
	}
}

// extraArgsAccepted refuses extra arguments for a datastore whose tools would
// drop them, so that a backup is not the first thing to find out the setting
// can never be used
func extraArgsAccepted(key string) func(s *service.Datastore, serviceName string, value string) error {
	return func(s *service.Datastore, serviceName string, value string) error {
		if verbName := service.ExtraArgsVerb(key); verbName != "" && value != "" && !s.AcceptsExtraArgs(verbName) {
			return verb.ErrExtraArgsRefused{Plugin: s.Definition.Dokku.Plugin, Name: verbName}
		}

		return nil
	}
}

// validateVolumeTargets refuses to move a volume the definition the service
// runs does not have, or onto a path something the service mounts already holds
func validateVolumeTargets(s *service.Datastore, serviceName string, value string) error {
	if value == "" {
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

// writeVolumeTargets writes the targets the way info reports them, so the same
// targets always read the same
func writeVolumeTargets(s *service.Datastore, serviceName string, value string) error {
	if value != "" {
		targets, err := service.ParseVolumeTargets(value)
		if err != nil {
			return err
		}
		value = service.FormatVolumeTargets(targets)
	}

	return writeProperty(s, serviceName, service.VolumeTargetsProperty, value)
}
