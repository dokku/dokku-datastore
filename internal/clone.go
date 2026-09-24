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

	// PostCreateNetworks are attached after the new container is created
	PostCreateNetworks *[]string

	// PostStartNetworks are attached after the new container is started
	PostStartNetworks *[]string
}

// serviceSettings are the settings a service runs with that belong to the
// service itself, and so are what a clone of it starts from.
//
// What is left out is left out on purpose. The password is generated for each
// service, the database name comes from the service name, an exposed port
// would clash with the source's on the host, links belong to the apps, and the
// backup credentials, schedule and encryption are secrets whose copy would
// ship a second set of backups to the same bucket.
type serviceSettings struct {
	ConfigOptions      string
	CustomEnv          string
	InitialNetwork     string
	Keyserver          string
	LogDriver          string
	LogOptions         []string
	Memory             int
	PostCreateNetworks []string
	PostStartNetworks  []string
	RestartPolicy      string
	ShmSize            string
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

	return serviceSettings{
		ConfigOptions:      service.ConfigOptions(datastore, serviceName),
		CustomEnv:          customEnv(serviceFiles.Env),
		InitialNetwork:     service.InitialNetwork(datastore, serviceName),
		Keyserver:          service.Keyserver(datastore, serviceName),
		LogDriver:          common.PropertyGet(commandPrefix, serviceName, service.LogDriverProperty),
		LogOptions:         splitList(common.PropertyGet(commandPrefix, serviceName, service.LogOptProperty)),
		Memory:             memory,
		PostCreateNetworks: splitList(service.PostCreateNetwork(datastore, serviceName)),
		PostStartNetworks:  splitList(service.PostStartNetwork(datastore, serviceName)),
		RestartPolicy:      service.ServiceRestartPolicy(datastore, serviceName),
		ShmSize:            common.ReadFirstLine(serviceFiles.ShmSize),
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

	// the same way for the rest of what the source runs with: a clone made
	// without repeating every flag would otherwise land on the defaults
	sourceSettings, err := readServiceSettings(input.Datastore, input.ServiceName)
	if err != nil {
		return err
	}
	settings := sourceSettings.withOverrides(input)

	input.Logger.Header2(fmt.Sprintf("Cloning %s to %s @ %s", input.ServiceName, input.NewServiceName, sourceImage)) //nolint:errcheck

	if err := CreateService(ctx, CreateServiceInput{
		ConfigOptions:      settings.ConfigOptions,
		CustomEnv:          settings.CustomEnv,
		Datastore:          input.Datastore,
		Image:              image,
		ImageVersion:       imageVersion,
		InitialNetwork:     settings.InitialNetwork,
		LogDriver:          settings.LogDriver,
		LogOptions:         settings.LogOptions,
		Memory:             settings.Memory,
		Password:           input.Password,
		PostCreateNetworks: settings.PostCreateNetworks,
		PostStartNetworks:  settings.PostStartNetworks,
		RestartPolicy:      settings.RestartPolicy,
		ServiceName:        input.NewServiceName,
		ShmSize:            settings.ShmSize,
		Logger:             input.Logger,
	}); err != nil {
		return err
	}

	// create has no flag for it, since it is only ever read when a backup runs,
	// so it is written onto the clone once the clone exists
	if settings.Keyserver != "" {
		if err := SetProperty(input.Datastore, input.NewServiceName, service.KeyserverProperty, settings.Keyserver); err != nil {
			return fmt.Errorf("failed to write the %s property: %w", service.KeyserverProperty, err)
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
