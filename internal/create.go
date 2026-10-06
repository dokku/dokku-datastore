package internal

import (
	"context"
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal/definition"
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

	// Definition names the definition to place the service on, empty to have
	// the image and version decide it
	Definition string

	// Image is the image to use for the service
	Image string

	// ImageVersion is the image version to use for the service
	ImageVersion string

	// InitialNetwork is the initial network to use for the service
	InitialNetwork string

	// LogDriver is the docker logging driver to run the service container with
	LogDriver string

	// LogOptions are the docker log options for the service container
	LogOptions []string

	// RestartPolicy is the docker restart policy for the service container,
	// empty for the default
	RestartPolicy string

	// WaitTimeout is how long, in seconds, the service is waited on to become
	// ready, empty for the default
	WaitTimeout string

	// Mounts are the host paths and docker volumes to mount into the service
	// container beyond the definition's own
	Mounts []service.Mount

	// VolumeTargets are the container paths the definition's volumes are
	// mounted at in place of its own, keyed by volume
	VolumeTargets map[string]string

	// Memory is the memory limit to use for the service
	Memory int

	// Password overrides the generated service password
	Password string

	// PostCreateNetworks is the networks to attach the service container to after service creation
	PostCreateNetworks []string

	// PostStartNetworks is the networks to attach the service container to after service start
	PostStartNetworks []string

	// RootPassword overrides the generated root password, for a datastore that
	// has one
	RootPassword string

	// ServiceName is the name of the service to create
	ServiceName string

	// ShmSize is the shared memory size to use for the service
	ShmSize string

	// Logger reports what is being waited on once the container exists
	Logger Ui
}

// secretOverrides are the passwords the caller gave, keyed by the environment
// variable a definition's secret names. A password left empty is generated.
func (input CreateServiceInput) secretOverrides() map[string]string {
	overrides := map[string]string{}
	if input.Password != "" {
		overrides[service.PasswordEnv] = input.Password
	}

	if input.RootPassword != "" {
		overrides[service.RootPasswordEnv] = input.RootPassword
	}

	return overrides
}

// resolveDefinition is the datastore a create runs on: the definition named
// outright, or else the one its image and version select. With neither, that is
// the newest of the datastore's own definitions and never one of its flavors.
func (input CreateServiceInput) resolveDefinition() (*service.Datastore, error) {
	if input.Definition != "" {
		return input.Datastore.WithDefinitionNamed(input.Definition)
	}

	return input.Datastore.ForImage(input.Image, input.ImageVersion), nil
}

// CreateService creates a new service
func CreateService(ctx context.Context, input CreateServiceInput) error {
	if err := service.ValidateServiceName(input.ServiceName); err != nil {
		return err
	}

	// the image and version decide the definition before anything else is
	// settled: a datastore split by major version mounts its data somewhere
	// different in each, a flavor such as pgvector has definitions of its own, and
	// the requirements checked below are the definition's own. A definition
	// named outright wins over all of that, and the image and version it ships
	// become the defaults the flags are laid over
	resolved, err := input.resolveDefinition()
	if err != nil {
		return err
	}
	input.Datastore = resolved

	// the name decides the database, so one the datastore keeps for itself is
	// refused before anything is made for it
	if err := service.CheckReservedServiceName(input.Datastore.Definition, input.ServiceName); err != nil {
		return err
	}

	// before anything is made, so that a host which cannot run this datastore
	// says so rather than leaving a half made service behind
	if err := CheckRequirements(input.Datastore.Definition.Dokku.Requirements); err != nil {
		return err
	}

	// and for the same reason: a log option docker will not accept is a service
	// that cannot start, which is worse found here than after its directories,
	// its credentials and its config files have been written
	if err := CheckLogConfig(input.LogDriver, input.LogOptions); err != nil {
		return err
	}

	if err := service.ValidateRestartPolicy(input.RestartPolicy); err != nil {
		return err
	}

	if err := service.ValidateWaitTimeout(input.WaitTimeout); err != nil {
		return err
	}

	// against the definition the version settled on, which is the one whose
	// volumes are moved and whose volumes a mount must not land on
	if err := service.CheckVolumeTargets(input.Datastore.Definition, input.VolumeTargets); err != nil {
		return err
	}
	if err := service.CheckMounts(input.Datastore.Definition, input.VolumeTargets, input.Mounts); err != nil {
		return err
	}
	if err := service.CheckMountsOnHost(ctx, service.Folders(input.Datastore, input.ServiceName).HostRoot, input.Mounts); err != nil {
		return err
	}

	// a password the definition has no secret for would be dropped, and the
	// service would start on one nobody was told
	secrets := input.secretOverrides()
	if err := service.CheckSecretOverrides(input.Datastore.Definition, secrets); err != nil {
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
		return fmt.Errorf("unable to create %s: %w", input.ServiceName, err)
	}

	if err := service.EnsureTaggedImage(ctx, service.EnsureTaggedImageInput{
		Action:      "creation",
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		TaggedImage: taggedImage,
	}); err != nil {
		return err
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

	// before anything is rendered: a config file written below is seeded once and
	// never rendered again, so one that named the database before it was recorded
	// would carry the unsanitized service name for the life of the service
	if err := service.WriteDatabaseName(service.WriteDatabaseNameInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	}); err != nil {
		return fmt.Errorf("failed to write database name: %w", err)
	}

	err = input.Datastore.CreateService(ctx, input.ServiceName, secrets)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	// what was resolved rather than what was asked for, the way upgrade records
	// it. The two are the same whenever the caller filled the flags in first,
	// and when it did not this is the difference between a service that records
	// the image it runs and one that records nothing and is placed by whatever
	// the plugin ships the next time its container has to be made.
	recordedImage, recordedImageVersion, _ := definition.CutImage(taggedImage)
	if err := service.CommitServiceConfig(service.CommitServiceConfigInput{
		ConfigOptions:      input.ConfigOptions,
		CustomEnv:          input.CustomEnv,
		Datastore:          input.Datastore,
		Image:              recordedImage,
		ImageVersion:       recordedImageVersion,
		InitialNetwork:     input.InitialNetwork,
		LogDriver:          input.LogDriver,
		LogOptions:         input.LogOptions,
		Memory:             input.Memory,
		PostCreateNetworks: input.PostCreateNetworks,
		PostStartNetworks:  input.PostStartNetworks,
		RestartPolicy:      input.RestartPolicy,
		WaitTimeout:        input.WaitTimeout,
		Mounts:             input.Mounts,
		VolumeTargets:      input.VolumeTargets,
		ServiceName:        input.ServiceName,
		ShmSize:            input.ShmSize,
	}); err != nil {
		return fmt.Errorf("failed to commit service config: %w", err)
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
