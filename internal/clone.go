package internal

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
// ship a second set of backups to the same bucket.
type serviceSettings struct {
	BackupStorageClass string
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
		BackupStorageClass: service.BackupStorageClass(datastore, serviceName),
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

// CloneService creates a new service on the same image as an existing one and
// copies the data across
func CloneService(ctx context.Context, input CloneServiceInput) error {
	// the clone runs on whatever image the source service is on, so that the
	// copied data is never handed to a different version than it came from.
	// Taken from the source's record rather than from its container, which is
	// what every other command is placed by; a source that never recorded one
	// has it written down here from the container it is running, since a clone
	// already requires the source to be up.
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

	sourceImage := recorded.Tagged()
	image, imageVersion := recorded.Image, recorded.ImageVersion

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

	input.Logger.Header2(fmt.Sprintf("Cloning %s to %s @ %s", input.ServiceName, input.NewServiceName, sourceImage)) //nolint:errcheck

	// placed on the definition the source is pinned to rather than the one its
	// image resolves to, which differ for a service created with --definition
	// or one pinned before its image had definitions of its own
	if err := CreateService(ctx, CreateServiceInput{
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
		service.BackupStorageClassProperty: settings.BackupStorageClass,
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
