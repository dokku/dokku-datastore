package internal

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dokku/dokku-datastore/internal/backend"
	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// CloneServiceInput is the input for the CloneService function.
//
// The settings a clone can be given are pointers rather than plain values,
// because a clone starts from the settings of the service it copies and has to
// tell "keep what the source has" apart from "set this to nothing", and an
// empty string cannot say both.
type CloneServiceInput struct {
	// ConfigOptions are extra arguments passed to the container create command
	ConfigOptions *string

	// CustomEnv is the environment the new service is started with
	CustomEnv *string

	// Datastore is the datastore both services belong to
	Datastore *service.Datastore

	// Image is the image the new service runs, empty for the source's. Plain
	// rather than a pointer like the settings, since a service always has an
	// image and there is no clearing one
	Image string

	// ImageVersion is the image version the new service runs, empty for the
	// source's
	ImageVersion string

	// Logger reports progress
	Logger Ui

	// NewServiceName is the name of the service to create
	NewServiceName string

	// Password overrides the generated service password
	Password string

	// RootPassword overrides the generated root password, for a datastore that
	// has one
	RootPassword string

	// ServiceName is the name of the service to copy from
	ServiceName string

	// ShmSize overrides the shared memory size of the new container
	ShmSize *string

	// Memory limits the memory of the new container
	Memory *int

	// ServiceMemory is the memory the new service's datastore is told to use
	// for itself
	ServiceMemory *string

	// InitialNetwork is the network the new container is attached to on create
	InitialNetwork *string

	// LogDriver is the docker logging driver the new container is run with
	LogDriver *string

	// LogOptions are the docker log options the new container is run with
	LogOptions *[]string

	// RestartPolicy is the docker restart policy the new container is run with
	RestartPolicy *string

	// WaitTimeout is how long, in seconds, the new service is waited on to
	// become ready
	WaitTimeout *string

	// Mounts are the host paths and docker volumes mounted into the new
	// container
	Mounts *[]service.Mount

	// VolumeTargets are the container paths the definition's volumes are
	// mounted at in the new container in place of its own, keyed by volume
	VolumeTargets *map[string]string

	// PostCreateNetworks are attached after the new container is created
	PostCreateNetworks *[]string

	// PostStartNetworks are attached after the new container is started
	PostStartNetworks *[]string
}

// serviceSettings are the settings a service runs with that belong to the
// service itself, and so are what a clone of it starts from.
//
// What is left out is left out on purpose. The passwords are generated for each
// service unless the clone is given its own, the database name comes from the service name, an exposed port
// would clash with the source's on the host, links belong to the apps, and the
// backup credentials, schedule and encryption are secrets whose copy would
// ship a second set of backups to the same bucket. The backup object name is
// left out for the same reason: a clone backed up to the source's bucket under
// the source's name would replace the source's backups with its own.
type serviceSettings struct {
	BackupMailto       string
	BackupStorageClass string
	BackupTimestamp    string
	ConfigOptions      string
	CustomEnv          string
	ExportArgs         string
	ImportArgs         string
	InitialNetwork     string
	Keyserver          string
	LogDriver          string
	LogOptions         []string
	Memory             int
	Mounts             []service.Mount
	PostCreateNetworks []string
	PostStartNetworks  []string
	RestartPolicy      string
	ServiceMemory      string
	ShmSize            string
	VolumeTargets      map[string]string
	WaitTimeout        string
}

// readServiceSettings reads the settings a service was created or set with.
//
// Each is read the way the rest of the plugin reads it, so a clone starts from
// the same values a container rebuilt for the source would be made with.
func readServiceSettings(datastore *service.Datastore, serviceName string) (serviceSettings, error) {
	serviceFiles := service.Files(datastore, serviceName)
	commandPrefix := datastore.Properties().CommandPrefix

	// refused rather than read as unlimited, so a clone never quietly drops a
	// limit the source has
	memory := 0
	if value := common.ReadFirstLine(serviceFiles.Memory); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return serviceSettings{}, fmt.Errorf("unable to read the memory limit of %s: %w", serviceName, err)
		}
		memory = parsed
	}

	mounts, err := service.ServiceMounts(datastore, serviceName)
	if err != nil {
		return serviceSettings{}, err
	}

	volumeTargets, err := service.ServiceVolumeTargets(datastore, serviceName)
	if err != nil {
		return serviceSettings{}, err
	}

	return serviceSettings{
		BackupMailto:       service.BackupMailto(datastore, serviceName),
		BackupStorageClass: service.BackupStorageClass(datastore, serviceName),
		BackupTimestamp:    strings.TrimSpace(common.PropertyGet(commandPrefix, serviceName, service.BackupTimestampProperty)),
		ConfigOptions:      service.ConfigOptions(datastore, serviceName),
		CustomEnv:          customEnv(serviceFiles.Env),
		ExportArgs:         service.ServiceExtraArgs(datastore, serviceName, service.ExportArgsProperty),
		ImportArgs:         service.ServiceExtraArgs(datastore, serviceName, service.ImportArgsProperty),
		InitialNetwork:     service.InitialNetwork(datastore, serviceName),
		Keyserver:          service.Keyserver(datastore, serviceName),
		LogDriver:          common.PropertyGet(commandPrefix, serviceName, service.LogDriverProperty),
		LogOptions:         splitList(common.PropertyGet(commandPrefix, serviceName, service.LogOptProperty)),
		Memory:             memory,
		Mounts:             mounts,
		PostCreateNetworks: splitList(service.PostCreateNetwork(datastore, serviceName)),
		PostStartNetworks:  splitList(service.PostStartNetwork(datastore, serviceName)),
		RestartPolicy:      service.ServiceRestartPolicy(datastore, serviceName),
		ServiceMemory:      common.ReadFirstLine(serviceFiles.ServiceMemory),
		ShmSize:            common.ReadFirstLine(serviceFiles.ShmSize),
		VolumeTargets:      volumeTargets,
		WaitTimeout:        service.ServiceWaitTimeout(datastore, serviceName),
	}, nil
}

// withOverrides is the settings a clone is made with: the source's, with each
// one a flag was given for replaced by it, an empty flag included.
//
// Pure, so which settings a clone ends up with is pinned by a test rather than
// by a docker daemon.
func (s serviceSettings) withOverrides(input CloneServiceInput) serviceSettings {
	values := []struct {
		value    *string
		override *string
	}{
		{value: &s.ConfigOptions, override: input.ConfigOptions},
		{value: &s.CustomEnv, override: input.CustomEnv},
		{value: &s.InitialNetwork, override: input.InitialNetwork},
		{value: &s.LogDriver, override: input.LogDriver},
		{value: &s.RestartPolicy, override: input.RestartPolicy},
		{value: &s.ServiceMemory, override: input.ServiceMemory},
		{value: &s.ShmSize, override: input.ShmSize},
		{value: &s.WaitTimeout, override: input.WaitTimeout},
	}
	for _, setting := range values {
		if setting.override != nil {
			*setting.value = *setting.override
		}
	}

	lists := []struct {
		value    *[]string
		override *[]string
	}{
		{value: &s.LogOptions, override: input.LogOptions},
		{value: &s.PostCreateNetworks, override: input.PostCreateNetworks},
		{value: &s.PostStartNetworks, override: input.PostStartNetworks},
	}
	for _, setting := range lists {
		if setting.override != nil {
			*setting.value = *setting.override
		}
	}

	if input.Memory != nil {
		s.Memory = *input.Memory
	}

	if input.Mounts != nil {
		s.Mounts = *input.Mounts
	}

	if input.VolumeTargets != nil {
		s.VolumeTargets = *input.VolumeTargets
	}

	return s
}

// splitList reads a comma separated property the way it was written, with an
// unset one read as no entries rather than as one empty entry
func splitList(value string) []string {
	if value == "" {
		return nil
	}

	return strings.Split(value, ",")
}

// cloneImage is the image a clone runs: the source's, with the image and the
// version it was given laid over it.
//
// A version left out stays the source's while the image does, and otherwise is
// decided the way upgrade decides it: the definition's own image falls back on
// the version the definition ships, and any other image has none to fall back
// on, since a version belongs to the repository that published it.
//
// Pure, so which image a clone lands on is pinned by a test rather than by a
// docker daemon.
func cloneImage(d definition.Definition, recorded service.RecordedImage, image string, imageVersion string) (service.RecordedImage, error) {
	if image == "" {
		image = recorded.Image
	}

	if imageVersion == "" && image == recorded.Image {
		imageVersion = recorded.ImageVersion
	}

	imageVersion, err := upgradeVersion(d, recorded, image, imageVersion)
	if err != nil {
		return service.RecordedImage{}, err
	}

	return service.RecordedImage{Image: image, ImageVersion: imageVersion}, nil
}

// checkCloneDefinition refuses a clone onto an image that resolves to another
// definition than the one its source is pinned to.
//
// A clone is placed on the source's definition, so one onto a different major
// version or flavor would mount its data where the wrong definition keeps it.
// Moving a service across definitions is what upgrade is for, since it is the
// one that knows how to carry the data across. Decided the way upgrade decides
// it, so that a source pinned to a definition its image does not resolve to
// keeps its pin on the clone as well.
//
// Pure, so which clones are refused is pinned by a test rather than by a docker
// daemon.
func checkCloneDefinition(s *service.Datastore, serviceName string, recorded service.RecordedImage, clone service.RecordedImage) error {
	target := upgradeTarget(s, nil, recorded, clone.Image, clone.ImageVersion)
	if target.DefinitionName() == s.DefinitionName() {
		return nil
	}

	return fmt.Errorf("unable to clone %s onto %s, which runs on the %s definition rather than the %s definition %s runs; upgrade %s onto it first, then clone it",
		serviceName, clone.Tagged(), target.DefinitionName(), s.DefinitionName(), serviceName, serviceName)
}

// CloneService creates a new service on the image of an existing one, or on
// another image of the same definition, and copies the data across
func CloneService(ctx context.Context, input CloneServiceInput) error {
	// the clone runs on whatever image the source service is on unless it was
	// given another. Taken from the source's record rather than from its
	// container, which is what every other command is placed by; a source that
	// never recorded one has it written down here from the container it is
	// running, since a clone already requires the source to be up.
	recorded, err := service.RecoverRecordedImage(ctx, service.RecoverRecordedImageInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return err
	}
	if !recorded.Complete() {
		return fmt.Errorf("unable to determine the image %s runs", input.ServiceName)
	}

	cloned, err := cloneImage(input.Datastore.Definition, recorded, input.Image, input.ImageVersion)
	if err != nil {
		return fmt.Errorf("unable to clone %s: %w", input.ServiceName, err)
	}

	// before anything is announced or made, so that a clone handed an image of
	// another definition never gets as far as a service of its own
	if err := checkCloneDefinition(input.Datastore, input.ServiceName, recorded, cloned); err != nil {
		return err
	}

	taggedImage := cloned.Tagged()
	image, imageVersion := cloned.Image, cloned.ImageVersion

	// create refuses it too, but checked here as well so that a refused clone is
	// never announced as though it had started. Against the definition the
	// source runs, which is the one the clone is placed on below
	if err := service.CheckReservedServiceName(input.Datastore.Definition, input.NewServiceName); err != nil {
		return err
	}

	// the same way for the rest of what the source runs with: a clone made
	// without repeating every flag would otherwise land on the defaults
	sourceSettings, err := readServiceSettings(input.Datastore, input.ServiceName)
	if err != nil {
		return err
	}
	settings := sourceSettings.withOverrides(input)

	input.Logger.Header2(fmt.Sprintf("Cloning %s to %s @ %s", input.ServiceName, input.NewServiceName, taggedImage)) //nolint:errcheck

	// placed on the definition the source is pinned to rather than the one its
	// image resolves to, which differ for a service created with --definition
	// or one pinned before its image had definitions of its own. The service
	// memory is always given, so that a source without one is cloned without
	// one rather than handed the datastore's default. It is made with the
	// backend the source was, so a clone is addressed the way its source is
	if err := CreateService(ctx, CreateServiceInput{
		Backend:            backend.Select(backend.SelectInput{Recorded: input.Datastore.RecordedBackend(input.ServiceName)}),
		ConfigOptions:      settings.ConfigOptions,
		CustomEnv:          settings.CustomEnv,
		Datastore:          input.Datastore,
		Definition:         input.Datastore.DefinitionName(),
		Image:              image,
		ImageVersion:       imageVersion,
		InitialNetwork:     settings.InitialNetwork,
		LogDriver:          settings.LogDriver,
		LogOptions:         settings.LogOptions,
		Memory:             settings.Memory,
		Mounts:             settings.Mounts,
		Password:           input.Password,
		PostCreateNetworks: settings.PostCreateNetworks,
		PostStartNetworks:  settings.PostStartNetworks,
		RestartPolicy:      settings.RestartPolicy,
		RootPassword:       input.RootPassword,
		ServiceName:        input.NewServiceName,
		ServiceMemory:      &settings.ServiceMemory,
		ShmSize:            settings.ShmSize,
		VolumeTargets:      settings.VolumeTargets,
		WaitTimeout:        settings.WaitTimeout,
		Logger:             input.Logger,
	}); err != nil {
		return err
	}

	// create has no flag for these, since they are only ever read when a backup
	// runs, so they are written onto the clone once the clone exists
	for property, value := range map[string]string{
		service.KeyserverProperty:          settings.Keyserver,
		service.BackupMailtoProperty:       settings.BackupMailto,
		service.BackupStorageClassProperty: settings.BackupStorageClass,
		service.BackupTimestampProperty:    settings.BackupTimestamp,
	} {
		if value == "" {
			continue
		}

		if err := SetProperty(input.Datastore, input.NewServiceName, property, value); err != nil {
			return fmt.Errorf("failed to write the %s property: %w", property, err)
		}
	}

	// the same for the extra arguments, which are written before the data is
	// copied so that the import below already runs with the clone's own
	for property, value := range map[string]string{
		service.ExportArgsProperty: settings.ExportArgs,
		service.ImportArgsProperty: settings.ImportArgs,
	} {
		if value == "" {
			continue
		}

		if err := SetProperty(input.Datastore, input.NewServiceName, property, value); err != nil {
			return fmt.Errorf("failed to write the %s property: %w", property, err)
		}
	}

	input.Logger.Info(fmt.Sprintf("Copying data from %s to %s", input.ServiceName, input.NewServiceName))

	// the dump is staged on disk rather than piped, so that a failed export does
	// not hand a truncated stream to the import
	dumpFile, err := os.CreateTemp("", "dokku-datastore-clone")
	if err != nil {
		return fmt.Errorf("unable to create a temporary file: %w", err)
	}
	defer os.Remove(dumpFile.Name())

	// the service's own database alone, unlike a backup. A dump of every
	// database loads each one under the name it was exported from, so the
	// source's own would land beside the new service's rather than in it.
	if err := input.Datastore.ExportService(ctx, service.ExportServiceInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		Writer:      dumpFile,
	}); err != nil {
		dumpFile.Close()
		return fmt.Errorf("unable to export %s: %w", input.ServiceName, err)
	}

	if _, err := dumpFile.Seek(0, io.SeekStart); err != nil {
		dumpFile.Close()
		return fmt.Errorf("unable to rewind %s: %w", filepath.Base(dumpFile.Name()), err)
	}

	if err := input.Datastore.ImportService(ctx, service.ImportServiceInput{
		Datastore:   input.Datastore,
		Reader:      dumpFile,
		ServiceName: input.NewServiceName,
	}); err != nil {
		dumpFile.Close()
		return fmt.Errorf("unable to import into %s: %w", input.NewServiceName, err)
	}

	if err := dumpFile.Close(); err != nil {
		return fmt.Errorf("unable to close the temporary file: %w", err)
	}

	input.Logger.Header2("Done") //nolint:errcheck
	return nil
}
