package internal

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dokku/dokku-datastore/internal/service"
)

// CloneServiceInput is the input for the CloneService function
type CloneServiceInput struct {
	// ConfigOptions are extra arguments passed to the container create command
	ConfigOptions string

	// CustomEnv is the environment the new service is started with
	CustomEnv string

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
	ShmSize string

	// Memory limits the memory of the new container
	Memory int

	// InitialNetwork is the network the new container is attached to on create
	InitialNetwork string

	// PostCreateNetworks are attached after the new container is created
	PostCreateNetworks []string

	// PostStartNetworks are attached after the new container is started
	PostStartNetworks []string
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

	input.Logger.Header2(fmt.Sprintf("Cloning %s to %s @ %s", input.ServiceName, input.NewServiceName, sourceImage)) //nolint:errcheck

	if err := CreateService(ctx, CreateServiceInput{
		ConfigOptions:      input.ConfigOptions,
		CustomEnv:          input.CustomEnv,
		Datastore:          input.Datastore,
		Image:              image,
		ImageVersion:       imageVersion,
		InitialNetwork:     input.InitialNetwork,
		Memory:             input.Memory,
		Password:           input.Password,
		PostCreateNetworks: input.PostCreateNetworks,
		PostStartNetworks:  input.PostStartNetworks,
		ServiceName:        input.NewServiceName,
		ShmSize:            input.ShmSize,
		Logger:             input.Logger,
	}); err != nil {
		return err
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
