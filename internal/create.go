package internal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// CreateServiceInput is the input for the CreateService function
type CreateServiceInput struct {
	// DatastoreType is the type of datastore to create
	DatastoreType string
	// ServiceName is the name of the service to create
	ServiceName string
	// ConfigOptions is the configuration options to use for the service
	ConfigOptions string
	// CustomEnv is the custom environment variables to use for the service
	CustomEnv string
	// Image is the image to use for the service
	Image string
	// ImageVersion is the image version to use for the service
	ImageVersion string
	// Memory is the memory limit to use for the service
	Memory int
	// InitialNetwork is the initial network to use for the service
	InitialNetwork string
	// Password is the password to use for the service
	Password string
	// PostCreateNetworks is the networks to attach the service container to after service creation
	PostCreateNetworks []string
	// PostStartNetworks is the networks to attach the service container to after service start
	PostStartNetworks []string
	// ShmSize is the shared memory size to use for the service
	ShmSize string
}

// CreateService creates a new service
func CreateService(input CreateServiceInput) error {
	if input.DatastoreType == "" {
		return fmt.Errorf("datastore type is required")
	}

	serviceWrapper, ok := service.Services[input.DatastoreType]
	if !ok {
		return fmt.Errorf("datastore type %s is not supported", input.DatastoreType)
	}

	if err := service.ValidateServiceName(input.ServiceName); err != nil {
		return err
	}

	serviceFolders := service.Folders(serviceWrapper, input.ServiceName)
	serviceRoot := serviceFolders.Root
	if _, err := os.Stat(serviceRoot); err == nil {
		return fmt.Errorf("service %s already exists", input.ServiceName)
	}

	// check if the image exists
	taggedImage, err := service.ImageForService(service.ImageForServiceInput{
		DatastoreType:        input.DatastoreType,
		ServiceName:          input.ServiceName,
		ImageOverride:        input.Image,
		ImageVersionOverride: input.ImageVersion,
	})
	if err != nil {
		return fmt.Errorf("failed to get image for service: %w", err)
	}

	if err := service.ValidateTaggedImageExists(taggedImage); err != nil {
		if os.Getenv(serviceWrapper.Properties().ImagePullVariable) == "true" {
			message := []string{
				fmt.Sprintf("%s environment variable detected. Not running pull command.", serviceWrapper.Properties().ImagePullVariable),
				fmt.Sprintf("docker image pull %s", taggedImage),
				fmt.Sprintf("%s service creation failed", input.ServiceName),
			}
			return errors.New(strings.Join(message, "\n"))
		}

		// pull the image
		if _, err := service.PullTaggedImage(taggedImage); err != nil {
			return fmt.Errorf("failed to pull image %s: %w", taggedImage, err)
		}
	}

	_, err = service.CallPlugnTrigger(common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"pre-create", input.DatastoreType, input.ServiceName},
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
	linksFile := filepath.Join(serviceRoot, "LINKS")
	if err := common.TouchFile(linksFile); err != nil {
		return fmt.Errorf("failed to create service links file %s: %w", linksFile, err)
	}

	err = serviceWrapper.CreateService(input.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	if err := service.CommitServiceConfig(service.CommitServiceConfigInput{
		DatastoreType:      input.DatastoreType,
		ServiceName:        input.ServiceName,
		CustomEnv:          input.CustomEnv,
		ConfigOptions:      input.ConfigOptions,
		Memory:             input.Memory,
		ShmSize:            input.ShmSize,
		Image:              input.Image,
		ImageVersion:       input.ImageVersion,
		InitialNetwork:     input.InitialNetwork,
		PostCreateNetworks: input.PostCreateNetworks,
		PostStartNetworks:  input.PostStartNetworks,
	}); err != nil {
		return fmt.Errorf("failed to commit service config: %w", err)
	}

	if err := service.WriteDatabaseName(input.DatastoreType, input.ServiceName); err != nil {
		return fmt.Errorf("failed to write database name: %w", err)
	}

	_, err = service.CallPlugnTrigger(common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"post-create", input.DatastoreType, input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action post-create trigger: %w", err)
	}

	if err := serviceWrapper.CreateServiceContainer(input.ServiceName); err != nil {
		return fmt.Errorf("failed to create service container: %w", err)
	}

	_, err = service.CallPlugnTrigger(common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"post-create-complete", input.DatastoreType, input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action post-create-complete trigger: %w", err)
	}

	return nil
}
