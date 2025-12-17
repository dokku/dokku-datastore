package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dokku/dokku-datastore/internal/datastores"
	"github.com/dokku/dokku/plugins/common"
)

// CreateServiceInput is the input for the CreateService function
type CreateServiceInput struct {
	// ConfigOptions is the configuration options to use for the service
	ConfigOptions string

	// CustomEnv is the custom environment variables to use for the service
	CustomEnv string

	// Datastore is the service to create
	Datastore datastores.Datastore

	// Image is the image to use for the service
	Image string

	// ImageVersion is the image version to use for the service
	ImageVersion string

	// InitialNetwork is the initial network to use for the service
	InitialNetwork string

	// Memory is the memory limit to use for the service
	Memory int

	// Password is the password to use for the service
	Password string

	// PostCreateNetworks is the networks to attach the service container to after service creation
	PostCreateNetworks []string

	// PostStartNetworks is the networks to attach the service container to after service start
	PostStartNetworks []string

	// ServiceName is the name of the service to create
	ServiceName string

	// ShmSize is the shared memory size to use for the service
	ShmSize string
}

// CreateService creates a new service
func CreateService(ctx context.Context, input CreateServiceInput) error {
	if err := datastores.ValidateServiceName(input.ServiceName); err != nil {
		return err
	}

	serviceFolders := datastores.Folders(input.Datastore, input.ServiceName)
	serviceRoot := serviceFolders.Root
	if _, err := os.Stat(serviceRoot); err == nil {
		return fmt.Errorf("service %s already exists", input.ServiceName)
	}

	// check if the image exists
	taggedImage, err := datastores.ImageForService(datastores.ImageForServiceInput{
		ImageOverride:        input.Image,
		ImageVersionOverride: input.ImageVersion,
		Datastore:            input.Datastore,
		ServiceName:          input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to get image for service: %w", err)
	}

	properties := input.Datastore.Properties()
	if err := datastores.ValidateTaggedImageExists(taggedImage); err != nil {
		if os.Getenv(properties.ImagePullVariable) == "true" {
			message := []string{
				fmt.Sprintf("%s environment variable detected. Not running pull command.", properties.ImagePullVariable),
				fmt.Sprintf("docker image pull %s", taggedImage),
				fmt.Sprintf("%s service creation failed", input.ServiceName),
			}
			return errors.New(strings.Join(message, "\n"))
		}

		// pull the image
		if _, err := datastores.PullTaggedImage(ctx, taggedImage); err != nil {
			return fmt.Errorf("failed to pull image %s: %w", taggedImage, err)
		}
	}

	_, err = datastores.CallPlugnTriggerWithContext(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"pre-create", input.Datastore.ServiceType(), input.ServiceName},
		Env:          map[string]string{},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action pre-create trigger: %w", err)
	}

	allServiceFolders := []string{
		serviceFolders.Root,
		serviceFolders.Config,
		serviceFolders.Data,
	}

	// create the service folders
	for _, folder := range allServiceFolders {
		if err := os.MkdirAll(folder, 0755); err != nil {
			return fmt.Errorf("failed to create service folder %s: %w", folder, err)
		}
	}

	// create the service links file
	serviceFiles := datastores.Files(input.Datastore, input.ServiceName)
	if !common.FileExists(serviceFiles.Links) {
		err = common.WriteStringToFile(common.WriteStringToFileInput{
			Content:   "",
			Filename:  serviceFiles.Links,
			GroupName: datastores.SystemGroup(),
			Mode:      0644,
			Username:  datastores.SystemUser(),
		})
		if err != nil {
			return fmt.Errorf("failed to create service links file %s: %w", serviceFiles.Links, err)
		}
	}

	err = input.Datastore.CreateService(ctx, input.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	if err := datastores.CommitServiceConfig(datastores.CommitServiceConfigInput{
		ConfigOptions:      input.ConfigOptions,
		CustomEnv:          input.CustomEnv,
		Datastore:          input.Datastore,
		Image:              input.Image,
		ImageVersion:       input.ImageVersion,
		InitialNetwork:     input.InitialNetwork,
		Memory:             input.Memory,
		PostCreateNetworks: input.PostCreateNetworks,
		PostStartNetworks:  input.PostStartNetworks,
		ServiceName:        input.ServiceName,
		ShmSize:            input.ShmSize,
	}); err != nil {
		return fmt.Errorf("failed to commit service config: %w", err)
	}

	if err := datastores.WriteDatabaseName(datastores.WriteDatabaseNameInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	}); err != nil {
		return fmt.Errorf("failed to write database name: %w", err)
	}

	_, err = datastores.CallPlugnTriggerWithContext(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"post-create", input.Datastore.ServiceType(), input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action post-create trigger: %w", err)
	}

	err = input.Datastore.CreateServiceContainer(ctx, datastores.CreateServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		TaggedImage: taggedImage,
	})
	if err != nil {
		return fmt.Errorf("failed to create service container: %w", err)
	}

	_, err = datastores.CallPlugnTriggerWithContext(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"post-create-complete", input.Datastore.ServiceType(), input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action post-create-complete trigger: %w", err)
	}

	return nil
}
