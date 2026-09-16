package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// ServiceFolderMode is the mode the service folders are created with. The data
// folder is handed to the datastore container, whose entrypoint takes ownership
// of it, so the group has to keep write access for later commands such as
// import to be able to write into it.
const ServiceFolderMode = 0775

// CreateServiceFolders creates the folders for a service. The mode is applied
// explicitly because MkdirAll is subject to the umask.
func CreateServiceFolders(folders []string, username string, groupName string) error {
	for _, folder := range folders {
		if err := os.MkdirAll(folder, ServiceFolderMode); err != nil {
			return fmt.Errorf("failed to create service folder %s: %w", folder, err)
		}

		if err := common.SetPermissions(common.SetPermissionInput{
			Filename:  folder,
			GroupName: groupName,
			Mode:      ServiceFolderMode,
			Username:  username,
		}); err != nil {
			return fmt.Errorf("failed to set permissions on service folder %s: %w", folder, err)
		}
	}

	return nil
}

// CreateServiceInput is the input for the CreateService function
type CreateServiceInput struct {
	// ConfigOptions is the configuration options to use for the service
	ConfigOptions string

	// CustomEnv is the custom environment variables to use for the service
	CustomEnv string

	// Datastore is the service to create
	Datastore *service.Datastore

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

	// Logger reports what is being waited on once the container exists
	Logger Ui
}

// CreateService creates a new service
func CreateService(ctx context.Context, input CreateServiceInput) error {
	if err := service.ValidateServiceName(input.ServiceName); err != nil {
		return err
	}

	// before anything is made, so that a host which cannot run this datastore
	// says so rather than leaving a half made service behind
	if err := CheckRequirements(input.Datastore.Definition.Dokku.Requirements); err != nil {
		return err
	}

	serviceFolders := service.Folders(input.Datastore, input.ServiceName)
	serviceRoot := serviceFolders.Root
	if _, err := os.Stat(serviceRoot); err == nil {
		return fmt.Errorf("service %s already exists", input.ServiceName)
	}

	// check if the image exists
	taggedImage, err := service.ImageForService(service.ImageForServiceInput{
		ImageOverride:        input.Image,
		ImageVersionOverride: input.ImageVersion,
		Datastore:            input.Datastore,
		ServiceName:          input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to get image for service: %w", err)
	}

	properties := input.Datastore.Properties()
	if err := service.ValidateTaggedImageExists(taggedImage); err != nil {
		if os.Getenv(properties.ImagePullVariable) == "true" {
			message := []string{
				fmt.Sprintf("%s environment variable detected. Not running pull command.", properties.ImagePullVariable),
				fmt.Sprintf("docker image pull %s", taggedImage),
				fmt.Sprintf("%s service creation failed", input.ServiceName),
			}
			return errors.New(strings.Join(message, "\n"))
		}

		// pull the image
		if _, err := service.PullTaggedImage(ctx, taggedImage); err != nil {
			return fmt.Errorf("failed to pull image %s: %w", taggedImage, err)
		}
	}

	_, err = execx.PlugnTrigger(ctx, common.PlugnTriggerInput{
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

	// whatever else the definition binds, so that docker is never the one to
	// create a path under the service root
	allServiceFolders = append(allServiceFolders, input.Datastore.BindDirectories(input.ServiceName)...)

	if err := CreateServiceFolders(allServiceFolders, hostenv.SystemUser(), hostenv.SystemGroup()); err != nil {
		return err
	}

	// create the service links file
	serviceFiles := service.Files(input.Datastore, input.ServiceName)
	if !common.FileExists(serviceFiles.Links) {
		err = common.WriteStringToFile(common.WriteStringToFileInput{
			Content:   "",
			Filename:  serviceFiles.Links,
			GroupName: hostenv.SystemGroup(),
			Mode:      0644,
			Username:  hostenv.SystemUser(),
		})
		if err != nil {
			return fmt.Errorf("failed to create service links file %s: %w", serviceFiles.Links, err)
		}
	}

	err = input.Datastore.CreateService(ctx, input.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	if err := service.CommitServiceConfig(service.CommitServiceConfigInput{
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

	if err := service.WriteDatabaseName(service.WriteDatabaseNameInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	}); err != nil {
		return fmt.Errorf("failed to write database name: %w", err)
	}

	_, err = execx.PlugnTrigger(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"post-create", input.Datastore.ServiceType(), input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action post-create trigger: %w", err)
	}

	// before the container, because what it does is usually fill a directory the
	// container then mounts over
	if err := input.Datastore.RunPreCreate(ctx, input.ServiceName); err != nil {
		return fmt.Errorf("failed to prepare the service: %w", err)
	}

	err = input.Datastore.CreateServiceContainer(ctx, service.CreateServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		TaggedImage: taggedImage,
	})
	if err != nil {
		return fmt.Errorf("failed to create service container: %w", err)
	}

	_, err = execx.PlugnTrigger(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"post-create-complete", input.Datastore.ServiceType(), input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action post-create-complete trigger: %w", err)
	}

	// waited on here rather than by the caller, so that every path which creates
	// a service gets a service that answers rather than one that merely exists
	if err := WaitForService(ctx, WaitForServiceInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		Logger:      input.Logger,
	}); err != nil {
		return err
	}

	// after the wait, because the step needs a service that answers, and before
	// anything is told the service exists
	if err := input.Datastore.RunPostCreate(ctx, input.ServiceName); err != nil {
		return fmt.Errorf("failed to set up the service: %w", err)
	}

	return nil
}
