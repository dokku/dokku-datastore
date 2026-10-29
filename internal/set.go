package internal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dokku/dokku-datastore/internal/definition"
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

	// effectFor is when a change reaches a service of a particular datastore,
	// for a property that one datastore takes while it runs and another only
	// once its container is made again. TakesEffect is used without one.
	effectFor func(d definition.Definition) PropertyEffect

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

	// read returns the value as write stored it, empty when nothing is. A
	// property without one is a dokku property.
	read func(s *service.Datastore, serviceName string) (string, error)

	// apply has a change take effect where it is not read again on its own
	apply func(ctx context.Context, s *service.Datastore, serviceName string) error

	// secret is the definition secret the property sets. A secret is changed
	// in the datastore and in every linked app rather than written and
	// applied, which ChangeServiceSecret does.
	secret string
}

// IsSecret reports whether the property is one of the service's passwords
func (p SettableProperty) IsSecret() bool {
	return p.secret != ""
}

// ReportedBy is the info key that reports the property
func (p SettableProperty) ReportedBy() string {
	if p.InfoKey != "" {
		return p.InfoKey
	}

	return p.Name
}

// Effect is when a change to the property reaches a service of the datastore
// the definition describes
func (p SettableProperty) Effect(d definition.Definition) PropertyEffect {
	if p.effectFor != nil {
		return p.effectFor(d)
	}

	return p.TakesEffect
}

// Warning is what the set command warns of once the property is set, for one
// that does not reach the service on its own, and empty for one that does
func (p SettableProperty) Warning(d definition.Definition, commandPrefix string, serviceName string) string {
	switch p.Effect(d) {
	case EffectRebuild:
		return fmt.Sprintf("%s takes effect the next time the container is built: run `dokku %s:stop %s` then `dokku %s:start %s`", p.Name, commandPrefix, serviceName, commandPrefix, serviceName)
	case EffectReexpose:
		return fmt.Sprintf("%s takes effect on an exposed service once it is reexposed: run `dokku %s:reexpose %s`", p.Name, commandPrefix, serviceName)
	}

	return ""
}

// RequiresRestart is whether a change to the property only reaches the service
// once it is restarted or reexposed
func (p SettableProperty) RequiresRestart(d definition.Definition) bool {
	return p.Effect(d) != EffectImmediately
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
		read:            backupAuthReader(accessKeyIDFile),
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
		read:            backupAuthReader(secretAccessKeyFile),
	},
	{
		Name:            "backup-default-region",
		Description:     "the region backups authenticate against",
		Backup:          true,
		requiresBackups: true,
		validate:        singleLine("backup-default-region"),
		write:           backupAuthWriter(defaultRegionFile),
		read:            backupAuthReader(defaultRegionFile),
	},
	{
		Name:            "backup-signature-version",
		Description:     "the signature version backups authenticate with",
		Backup:          true,
		requiresBackups: true,
		validate:        singleLine("backup-signature-version"),
		write:           backupAuthWriter(signatureVersionFile),
		read:            backupAuthReader(signatureVersionFile),
	},
	{
		Name:            "backup-endpoint-url",
		Description:     "the endpoint of an s3 compatible service backups are shipped to",
		Backup:          true,
		requiresBackups: true,
		validate:        ValidateEndpointURL,
		write:           backupAuthWriter(endpointURLFile),
		read:            backupAuthReader(endpointURLFile),
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
		read:            backupEncryptionReader(encryptionKeyFile),
	},
	{
		Name:            "backup-public-key-id",
		Description:     "the gpg public key id backups are encrypted with",
		Backup:          true,
		requiresBackups: true,
		validate:        singleLine("backup-public-key-id"),
		write:           backupEncryptionWriter(publicKeyIDFile),
		read:            backupEncryptionReader(publicKeyIDFile),
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
		write:           writeBackupBucket,
		read:            propertyReader(BackupBucketProperty),
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
		read:            propertyReader(BackupUseIAMProperty),
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
		read:            propertyReader(service.VolumeTargetsProperty),
	},
	{
		Name:        service.ConfigOptionsProperty,
		Description: "extra arguments for the process the service container runs",
		TakesEffect: EffectRebuild,
		validate:    singleLine(service.ConfigOptionsProperty),
		read:        serviceFileReader(func(files service.ServiceFiles) string { return files.ConfigOptions }),
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
		read: serviceFileReader(func(files service.ServiceFiles) string { return files.Env }),
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
		read: serviceFileReader(func(files service.ServiceFiles) string { return files.Memory }),
		write: serviceFileWriter(func(files service.ServiceFiles) string { return files.Memory }, 0644, func(value string) string {
			if value == "" {
				return "0"
			}
			return value
		}),
	},
	{
		Name:        service.ServiceMemoryProperty,
		Description: "the memory in megabytes the datastore itself is told to use, such as a cache size or a heap, empty for its own default",
		TakesEffect: EffectRebuild,
		// a datastore that can be told while it runs is, and the rest read it
		// when the container is made
		effectFor: func(d definition.Definition) PropertyEffect {
			if d.Dokku.ServiceMemory != nil && d.Dokku.ServiceMemory.Change != nil {
				return EffectImmediately
			}
			return EffectRebuild
		},
		validate: service.ValidateServiceMemory,
		validateService: func(s *service.Datastore, serviceName string, value string) error {
			return service.CheckServiceMemory(s.Definition, value)
		},
		read: serviceFileReader(func(files service.ServiceFiles) string { return files.ServiceMemory }),
		write: serviceFileWriter(func(files service.ServiceFiles) string { return files.ServiceMemory }, 0644, func(value string) string {
			return value
		}),
		apply: func(ctx context.Context, s *service.Datastore, serviceName string) error {
			return s.ApplyServiceMemory(ctx, serviceName)
		},
	},
	{
		Name:        service.ShmSizeProperty,
		Description: "the shared memory size of the container, such as `64m`",
		TakesEffect: EffectRebuild,
		validate:    service.ValidateShmSize,
		read:        serviceFileReader(func(files service.ServiceFiles) string { return files.ShmSize }),
		write: serviceFileWriter(func(files service.ServiceFiles) string { return files.ShmSize }, 0644, func(value string) string {
			return value
		}),
	},
	{
		Name:            "password",
		Description:     "the password the service and its linked apps use, changed in the datastore and in the url every linked app holds",
		Sensitive:       true,
		InfoKey:         "password-fingerprint",
		secret:          "password",
		validate:        validateSecretValue("password"),
		validateService: secretChangeable("password", "password"),
		read:            secretReader("password"),
	},
	{
		Name:            "root-password",
		Description:     "the password of the datastore's own administrator, changed in the datastore",
		Sensitive:       true,
		InfoKey:         "root-password-fingerprint",
		secret:          "root_password",
		validate:        validateSecretValue("root-password"),
		validateService: secretChangeable("root-password", "root_password"),
		read:            secretReader("root_password"),
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
	"mounts":               "mounts are changed with mount and unmount",
	service.LabelsProperty: "labels are changed with label and unlabel",
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
	if property.IsSecret() {
		return fmt.Errorf("%s is changed in the datastore as well as on disk, which ChangeServiceSecret does", key)
	}

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

// ReadProperty returns a property as it is stored for a service, which is
// what tells a change from a value set again: a value is stored the way
// SetProperty normalizes it, so two spellings of the same value read the same
func ReadProperty(s *service.Datastore, serviceName string, key string) (string, error) {
	if err := ValidateProperty(key); err != nil {
		return "", err
	}

	property, _ := LookupSettableProperty(key)
	if property.read != nil {
		return property.read(s, serviceName)
	}

	return propertyReader(key)(s, serviceName)
}

// RestartForPropertyInput is the input for the RestartForProperty function
type RestartForPropertyInput struct {
	// Datastore is the service's datastore
	Datastore *service.Datastore

	// ServiceName is the service the property was set on
	ServiceName string

	// Property is the property that changed
	Property SettableProperty

	// Logger reports what is restarted and what is being waited on
	Logger Ui
}

// RestartForProperty has a property that changed reach the service, doing
// what its warning would otherwise ask for.
//
// A property read when the container is made has the service stopped and
// started, whether or not it was running, since the caller asked for the value
// to be in effect. A property read when the exposed ports are published has an
// exposed service reexposed, or started when it is stopped, since start
// reconciles the ports and reexpose refuses a stopped service. A service that
// is not exposed takes it up on its next expose.
func RestartForProperty(ctx context.Context, input RestartForPropertyInput) error {
	switch input.Property.Effect(input.Datastore.Definition) {
	case EffectRebuild:
		return RecreateServiceContainer(ctx, RecreateServiceContainerInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
			Logger:      input.Logger,
			Reason:      fmt.Sprintf("for %s to take effect", input.Property.Name),
		})

	case EffectReexpose:
		if !IsExposed(input.Datastore, input.ServiceName) {
			input.Logger.Info(fmt.Sprintf("Service %s is not exposed, so %s takes effect the next time it is exposed", input.ServiceName, input.Property.Name))
			return nil
		}

		switch service.Status(ctx, service.StatusInput{Datastore: input.Datastore, ServiceName: input.ServiceName}) {
		case "running", "restarting":
			input.Logger.Info(fmt.Sprintf("Reexposing %s for %s to take effect", input.ServiceName, input.Property.Name))

			// the flag is the consent a directly exposed service would otherwise
			// be asked for before it is stopped and started
			return ReexposeService(ctx, ReexposeServiceInput{
				Datastore:   input.Datastore,
				ServiceName: input.ServiceName,
				Logger:      input.Logger,
				Force:       true,
			})
		}

		input.Logger.Info(fmt.Sprintf("Starting %s for %s to take effect", input.ServiceName, input.Property.Name))
		if err := service.Start(ctx, service.StartInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
		}); err != nil {
			return fmt.Errorf("failed to start service: %w", err)
		}

		return WaitForService(ctx, WaitForServiceInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
			Logger:      input.Logger,
		})
	}

	return nil
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

// propertyReader reads a dokku property, as writeProperty wrote it
func propertyReader(key string) func(s *service.Datastore, serviceName string) (string, error) {
	return func(s *service.Datastore, serviceName string) (string, error) {
		return common.PropertyGet(s.Properties().CommandPrefix, serviceName, key), nil
	}
}

// serviceFileReader reads one of the files create keeps a container setting in,
// as serviceFileWriter wrote it
func serviceFileReader(file func(service.ServiceFiles) string) func(s *service.Datastore, serviceName string) (string, error) {
	return func(s *service.Datastore, serviceName string) (string, error) {
		return readStoredFile(file(service.Files(s, serviceName)))
	}
}

// backupAuthReader reads one of the files backup-auth stores the credentials
// backups are shipped with in
func backupAuthReader(name string) func(s *service.Datastore, serviceName string) (string, error) {
	return func(s *service.Datastore, serviceName string) (string, error) {
		return readStoredFile(filepath.Join(service.Folders(s, serviceName).Backup, name))
	}
}

// backupEncryptionReader reads one of the files the backup encryption commands
// store how backups are encrypted in
func backupEncryptionReader(name string) func(s *service.Datastore, serviceName string) (string, error) {
	return func(s *service.Datastore, serviceName string) (string, error) {
		return readStoredFile(filepath.Join(service.Folders(s, serviceName).BackupEncryption, name))
	}
}

// readStoredFile reads a file a setting is stored in, empty when there is none
func readStoredFile(filename string) (string, error) {
	contents, err := os.ReadFile(filename)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("unable to read %s: %w", filename, err)
	}

	return string(contents), nil
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
// Clearing is always allowed. Either is refused once the service has a schedule
// other than the default one, because these keys only name that one.
func requireBackupSchedule(key string) func(s *service.Datastore, serviceName string, value string) error {
	return func(s *service.Datastore, serviceName string, value string) error {
		if err := refuseUnnamedScheduleEdit(s, serviceName, key); err != nil {
			return err
		}

		if value == "" {
			return nil
		}

		if _, ok := ReadBackupSchedule(s, serviceName); !ok {
			return fmt.Errorf("There is no scheduled backup for %s, so %s cannot be set. Schedule one with backup-schedule", serviceName, key) //nolint:staticcheck // matches the bash datastore plugins
		}

		return nil
	}
}

// refuseUnnamedScheduleEdit refuses backup-bucket and backup-use-iam once any
// schedule other than the default one exists. Those keys change the default
// schedule, and they do not name one of several.
func refuseUnnamedScheduleEdit(s *service.Datastore, serviceName string, key string) error {
	schedules, err := ReadBackupSchedules(s, serviceName)
	if err != nil {
		return err
	}

	for _, schedule := range schedules {
		if schedule.ID != BackupScheduleIDDefault {
			return fmt.Errorf("%s changes the default schedule, and %s has another scheduled backup. Change a schedule with backup-schedule --id", key, serviceName)
		}
	}

	return nil
}

// validateBackupBucket refuses a bucket for a service with no scheduled backup,
// and refuses to clear the one a scheduled backup is shipped to, since its
// crontab entry cannot be written without it
func validateBackupBucket(s *service.Datastore, serviceName string, value string) error {
	if err := refuseUnnamedScheduleEdit(s, serviceName, BackupBucketProperty); err != nil {
		return err
	}

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

	if err := writeProperty(s, serviceName, BackupUseIAMProperty, value); err != nil {
		return err
	}

	identity, _ := readDefaultBackupSchedule(s, serviceName)
	return syncDefaultScheduleIdentity(s, serviceName, identity)
}

// writeBackupBucket records the bucket the default schedule is shipped to, and
// keeps that schedule's object in the schedules file in step with it.
func writeBackupBucket(s *service.Datastore, serviceName string, value string) error {
	if err := writeProperty(s, serviceName, BackupBucketProperty, value); err != nil {
		return err
	}

	identity, _ := readDefaultBackupSchedule(s, serviceName)
	return syncDefaultScheduleIdentity(s, serviceName, identity)
}

// regenerateCrontabIfScheduled has dokku write its crontab again for a service
// with a scheduled backup, which is where the backup settings the crontab
// carries are read. Any schedule is enough, including one that is not the default.
func regenerateCrontabIfScheduled(ctx context.Context, s *service.Datastore, serviceName string) error {
	schedules, err := ReadBackupSchedules(s, serviceName)
	if len(schedules) > 0 {
		if regenErr := regenerateCrontab(ctx); regenErr != nil {
			return regenErr
		}
	}

	return err
}

// scheduleOverrideProperties are the settings a schedule can set for itself.
// The cron expression, bucket and instance role are changed with backup-schedule.
func scheduleOverrideProperty(key string) bool {
	switch key {
	case service.BackupStorageClassProperty, service.BackupObjectNameProperty, service.BackupTimestampProperty, service.BackupMailtoProperty, service.KeyserverProperty, service.ExportArgsProperty, backupEncryptionPassphraseProperty, "backup-public-key-id", backupAccessKeyIDProperty, backupSecretAccessKeyProperty, "backup-default-region", "backup-signature-version", "backup-endpoint-url":
		return true
	default:
		return false
	}
}

// schedulePairProperty reports whether a key is one half of the access key pair.
// The pair is written with backup-auth and cleared with backup-deauth. set
// rotates one half once both are already stored.
func schedulePairProperty(key string) bool {
	return key == backupAccessKeyIDProperty || key == backupSecretAccessKeyProperty
}

// validateSchedulePairEdit reports whether set may change one half of a
// schedule's credential pair. A schedule without both keys is given them with
// backup-auth --id. Clearing one half while the other remains is refused, so
// the pair is dropped with backup-deauth --id.
func validateSchedulePairEdit(s *service.Datastore, serviceName string, id string, key string, value string) error {
	schedule, err := LookupBackupSchedule(s, serviceName, id)
	if err != nil {
		return err
	}

	other := schedule.SecretAccessKey
	if key == backupSecretAccessKeyProperty {
		other = schedule.AccessKeyID
	}
	if value == "" {
		if other != "" {
			return fmt.Errorf("clear %s and %s together with backup-deauth --id", backupAccessKeyIDProperty, backupSecretAccessKeyProperty)
		}

		return nil
	}

	if schedule.AccessKeyID == "" || schedule.SecretAccessKey == "" {
		return fmt.Errorf("%s is set with backup-auth --id", key)
	}

	return nil
}

// ValidateBackupScheduleOverride reports whether a setting can be recorded on
// one schedule. An empty value clears that schedule's own setting and leaves
// the service's value in place.
func ValidateBackupScheduleOverride(s *service.Datastore, serviceName string, id string, key string, value string) error {
	if err := ValidateBackupScheduleID(id); err != nil {
		return err
	}

	if schedulePairProperty(key) {
		if err := validateSchedulePairEdit(s, serviceName, id, key, value); err != nil {
			return err
		}
	}

	switch {
	case key == BackupBucketProperty || key == BackupUseIAMProperty:
		return fmt.Errorf("%s is part of the schedule itself, change it with backup-schedule --id", key)
	case !scheduleOverrideProperty(key):
		return fmt.Errorf("%s cannot be set on a schedule", key)
	}

	if _, err := LookupBackupSchedule(s, serviceName, id); err != nil {
		return err
	}

	if value == "" {
		return nil
	}

	property, _ := LookupSettableProperty(key)
	if property.validate != nil {
		if err := property.validate(value); err != nil {
			return err
		}
	}
	if property.validateService != nil {
		if err := property.validateService(s, serviceName, value); err != nil {
			return err
		}
	}

	return nil
}

// SetBackupScheduleOverride records a setting on one schedule, or clears the
// schedule's own value when value is empty, and has dokku write its crontab
// again when the service has a schedule.
func SetBackupScheduleOverride(ctx context.Context, s *service.Datastore, serviceName string, id string, key string, value string) error {
	if err := ValidateBackupScheduleOverride(s, serviceName, id, key, value); err != nil {
		return err
	}

	schedule, err := LookupBackupSchedule(s, serviceName, id)
	if err != nil {
		return err
	}

	if err := schedule.setOverride(key, value); err != nil {
		return err
	}

	if err := saveBackupSchedule(s, serviceName, schedule); err != nil {
		return err
	}

	return regenerateCrontabIfScheduled(ctx, s, serviceName)
}

// setOverride records one setting on a schedule, or clears it. The value has
// already been accepted.
func (b *BackupSchedule) setOverride(key string, value string) error {
	switch key {
	case service.BackupStorageClassProperty:
		b.StorageClass = value
	case service.BackupObjectNameProperty:
		b.ObjectName = value
	case service.BackupTimestampProperty:
		b.Timestamp = value
	case service.BackupMailtoProperty:
		b.Mailto = value
	case service.KeyserverProperty:
		b.Keyserver = value
	case service.ExportArgsProperty:
		b.ExportArgs = value
	case backupEncryptionPassphraseProperty:
		b.EncryptionPassphrase = value
	case "backup-public-key-id":
		b.PublicKeyID = value
	case backupAccessKeyIDProperty:
		b.AccessKeyID = value
	case backupSecretAccessKeyProperty:
		b.SecretAccessKey = value
	case "backup-default-region":
		b.DefaultRegion = value
	case "backup-signature-version":
		b.SignatureVersion = value
	case "backup-endpoint-url":
		b.EndpointURL = value
	default:
		return fmt.Errorf("%s cannot be set on a schedule", key)
	}

	return nil
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
