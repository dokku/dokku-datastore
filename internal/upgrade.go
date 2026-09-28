package internal

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// UpgradeServiceInput is the input for the UpgradeService function.
//
// The settings a service already has are pointers rather than plain values,
// because an upgrade has to tell "leave this as it is" apart from "set this to
// nothing", and an empty string cannot say both.
type UpgradeServiceInput struct {
	// ConfigOptions are extra arguments passed to the container create command
	ConfigOptions *string

	// CustomEnv is the environment the service is started with
	CustomEnv *string

	// InitialNetwork is the network the container is attached to on create
	InitialNetwork *string

	// PostCreateNetworks are attached after the container is created
	PostCreateNetworks *[]string

	// PostStartNetworks are attached after the container is started
	PostStartNetworks *[]string

	// ShmSize is the shared memory size for the container
	ShmSize *string

	// Memory is the container memory limit in megabytes, zero for unlimited
	Memory *int

	// LogDriver is the docker logging driver the service container is run with
	LogDriver *string

	// LogOptions are the docker log options the service container is run with
	LogOptions *[]string

	// RestartPolicy is the docker restart policy the service container is run
	// with
	RestartPolicy *string

	// WaitTimeout is how long, in seconds, the service is waited on to become
	// ready
	WaitTimeout *string

	// Mounts are the host paths and docker volumes mounted into the service
	// container
	Mounts *[]service.Mount

	// VolumeTargets are the container paths the definition's volumes are
	// mounted at in place of its own, keyed by volume
	VolumeTargets *map[string]string

	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

	// Definition names the definition to move the service onto, empty to have
	// the image and version decide it
	Definition string

	// Image is the image to upgrade to
	Image string

	// ImageVersion is the image version to upgrade to
	ImageVersion string

	// RestartApps is whether to stop and start the linked apps around the upgrade
	RestartApps bool

	// ServiceName is the name of the service to upgrade
	ServiceName string

	// Logger reports progress
	Logger Ui
}

// changesSettings reports whether the upgrade was asked to change anything about
// the service besides the image it runs.
func (i UpgradeServiceInput) changesSettings() bool {
	return i.ConfigOptions != nil ||
		i.CustomEnv != nil ||
		i.InitialNetwork != nil ||
		i.PostCreateNetworks != nil ||
		i.PostStartNetworks != nil ||
		i.ShmSize != nil ||
		i.Memory != nil ||
		i.LogDriver != nil ||
		i.LogOptions != nil ||
		i.RestartPolicy != nil ||
		i.WaitTimeout != nil ||
		i.Mounts != nil ||
		i.VolumeTargets != nil
}

// upgradeVersion is the version an upgrade moves a service to: the one asked
// for, and otherwise the newest the service's own definition ships.
//
// The datastore an upgrade is handed has already been resolved to the
// definition the service runs, so the default here is the newest tag inside the
// service's major version rather than the newest the plugin has. Crossing a
// major version stays something the operator asks for by name, because it moves
// where the data is mounted and cannot be undone by pointing the version back.
//
// An image the definition does not ship has no newest to move to, so it is told
// rather than moved onto something invented. That covers the image the service
// already runs and the one an upgrade was asked to move it to alike: a version
// belongs to the repository that published it, and the definition's is no more
// applicable to a new image than to an old one. A service with no record at all
// takes the default: an upgrade was asked for in so many words, which makes it
// a choice rather than a guess, and it is what puts a service right that start
// has refused to place.
//
// Pure, so which version an upgrade lands on is pinned by a test rather than by
// a docker daemon.
func upgradeVersion(d definition.Definition, recorded service.RecordedImage, requestedImage string, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}

	image := requestedImage
	if image == "" {
		image = recorded.Image
	}

	if image != "" && image != d.DefaultImage {
		return "", service.NoImageVersionError(image, d.Dokku.Plugin)
	}

	return d.DefaultImageVersion, nil
}

// UpgradeService recreates a service's container on a different image
func UpgradeService(ctx context.Context, input UpgradeServiceInput) error {
	// before anything, because an upgrade takes the old container away before it
	// makes the new one, and a log option or a restart policy docker will not
	// accept would leave the service with neither
	driver := ""
	if input.LogDriver != nil {
		driver = *input.LogDriver
	}
	options := []string{}
	if input.LogOptions != nil {
		options = *input.LogOptions
	}
	if err := CheckLogConfig(driver, options); err != nil {
		return err
	}
	if input.RestartPolicy != nil {
		if err := service.ValidateRestartPolicy(*input.RestartPolicy); err != nil {
			return err
		}
	}
	if input.WaitTimeout != nil {
		if err := service.ValidateWaitTimeout(*input.WaitTimeout); err != nil {
			return err
		}
	}

	// a definition named outright is refused before anything is touched, and
	// otherwise supplies the image and version the flags are laid over, the
	// way it does at create: the service's recorded image belongs to the
	// definition it is being moved off
	var named *service.Datastore
	versionedBy := input.Datastore.Definition
	if input.Definition != "" {
		found, err := input.Datastore.WithDefinitionNamed(input.Definition)
		if err != nil {
			return err
		}

		named = found
		versionedBy = named.Definition
		if input.Image == "" {
			input.Image = named.Definition.DefaultImage
		}
	}

	// before the version is decided, because deciding it needs to know which
	// image the service runs, and a service that never recorded one only knows
	// while its container is still there
	recorded, err := service.RecoverRecordedImage(ctx, service.RecoverRecordedImageInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return err
	}

	imageVersion, err := upgradeVersion(versionedBy, recorded, input.Image, input.ImageVersion)
	if err != nil {
		return fmt.Errorf("unable to upgrade %s: %w", input.ServiceName, err)
	}

	taggedImage, err := service.ImageForService(service.ImageForServiceInput{
		ImageOverride:        input.Image,
		ImageVersionOverride: imageVersion,
		Datastore:            input.Datastore,
		ServiceName:          input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("unable to upgrade %s: %w", input.ServiceName, err)
	}

	if err := service.EnsureTaggedImage(ctx, service.EnsureTaggedImageInput{
		Action:      "upgrade",
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		TaggedImage: taggedImage,
	}); err != nil {
		return err
	}

	currentImage := service.Version(ctx, service.VersionInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	image, imageVersion, _ := definition.CutImage(taggedImage)
	target := upgradeTarget(input.Datastore, named, recorded, image, imageVersion)

	// an upgrade to the image a service already runs is nothing to do - unless it
	// also asked to change a setting, or named a definition other than the one it
	// runs, which is a recreate whatever the image says
	movesDefinition := named != nil && named.DefinitionName() != input.Datastore.DefinitionName()
	if currentImage == taggedImage && !input.changesSettings() && !movesDefinition {
		input.Logger.Info(fmt.Sprintf("Service %s already running %s", input.ServiceName, taggedImage)) //nolint:errcheck
		return nil
	}

	// before the old container is taken away, for the same reason the log
	// config is checked first, and against the definition the upgrade lands on:
	// one that crosses a major version can mount its data somewhere else. The
	// mounts the service already has are checked too, since a host path removed
	// since it was mounted would otherwise only be found once there is no
	// container left to go back to
	if err := checkUpgradeMounts(ctx, input, target); err != nil {
		return err
	}

	linkedApps := service.LinkedApps(ctx, service.LinkedAppsInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})

	if input.RestartApps {
		input.Logger.Header2(fmt.Sprintf("Stopping all linked apps for %s", input.ServiceName)) //nolint:errcheck
		if err := changeAppState(ctx, "stop", linkedApps); err != nil {
			return err
		}
	}

	input.Logger.Header2(fmt.Sprintf("Upgrading %s to %s", input.ServiceName, taggedImage)) //nolint:errcheck
	if err := service.RemoveServiceContainer(ctx, service.RemoveServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	}); err != nil {
		return err
	}

	// an upgrade that crosses a major version moves the service onto the other
	// definition, which is a different data path rather than a different tag.
	// Recorded together with the image it was resolved from: a container rebuilt
	// later is placed by these two files and nothing else, so a pin that moved
	// without the image would mount the new path at the old version.
	if err := service.RecordImage(service.RecordImageInput{
		Datastore:    input.Datastore,
		Image:        image,
		ImageVersion: imageVersion,
		ServiceName:  input.ServiceName,
	}); err != nil {
		return err
	}

	input.Datastore = target
	if err := service.PinDefinition(input.Datastore, input.ServiceName); err != nil {
		return err
	}

	// the container about to be made is built from the service's own files and
	// properties, so anything the upgrade was asked to change has to be written
	// before it rather than passed to it
	if err := applyUpgradeSettings(input); err != nil {
		return err
	}

	if err := input.Datastore.CreateServiceContainer(ctx, service.CreateServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		TaggedImage: taggedImage,
	}); err != nil {
		return err
	}

	if err := service.Start(ctx, service.StartInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	}); err != nil {
		return err
	}

	// before the linked apps are started again, so they come back to a datastore
	// that answers on the version they were stopped for
	if err := WaitForService(ctx, WaitForServiceInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		Logger:      input.Logger,
	}); err != nil {
		return err
	}

	if input.RestartApps {
		input.Logger.Header2(fmt.Sprintf("Starting all linked apps for %s", input.ServiceName)) //nolint:errcheck
		if err := changeAppState(ctx, "start", linkedApps); err != nil {
			return err
		}
	}

	input.Logger.Header2("Done") //nolint:errcheck
	return nil
}

// applyUpgradeSettings writes the settings an upgrade was asked to change, and
// only those: a service keeps whatever it already had for the rest.
//
// CommitServiceConfig is not used here on purpose. It writes every field it is
// given, so preserving what was not asked about would mean reading each value
// back and handing it over again - and the environment would have to survive a
// round trip through two different separators to do it.
func applyUpgradeSettings(input UpgradeServiceInput) error {
	serviceFiles := service.Files(input.Datastore, input.ServiceName)

	// the memory file holds what create writes to it, zero for unlimited
	var memory *string
	if input.Memory != nil {
		value := strconv.Itoa(*input.Memory)
		memory = &value
	}

	// the config options can carry credentials, so they are kept private
	files := []struct {
		filename string
		value    *string
		mode     os.FileMode
	}{
		{filename: serviceFiles.ConfigOptions, value: input.ConfigOptions, mode: service.PrivateFileMode},
		{filename: serviceFiles.ShmSize, value: input.ShmSize, mode: 0644},
		{filename: serviceFiles.Memory, value: memory, mode: 0644},
	}
	for _, file := range files {
		if file.value == nil {
			continue
		}

		if err := writeServiceFile(file.filename, *file.value, file.mode); err != nil {
			return err
		}
	}

	// stored one per line, and given semi-colon delimited, the same way create
	// takes it. Private for the same reason the config options are
	if input.CustomEnv != nil {
		lines := strings.Join(strings.Split(*input.CustomEnv, ";"), "\n")
		if err := writeServiceFile(serviceFiles.Env, lines, service.PrivateFileMode); err != nil {
			return err
		}
	}

	plugin := input.Datastore.Properties().CommandPrefix
	properties := map[string]*string{}
	if input.InitialNetwork != nil {
		properties["initial-network"] = input.InitialNetwork
	}
	if input.PostCreateNetworks != nil {
		joined := strings.Join(*input.PostCreateNetworks, ",")
		properties["post-create-network"] = &joined
	}
	if input.PostStartNetworks != nil {
		joined := strings.Join(*input.PostStartNetworks, ",")
		properties["post-start-network"] = &joined
	}
	if input.LogDriver != nil {
		properties[service.LogDriverProperty] = input.LogDriver
	}
	if input.LogOptions != nil {
		joined := strings.Join(*input.LogOptions, ",")
		properties[service.LogOptProperty] = &joined
	}
	if input.RestartPolicy != nil {
		properties[service.RestartPolicyProperty] = input.RestartPolicy
	}
	if input.WaitTimeout != nil {
		trimmed := strings.TrimSpace(*input.WaitTimeout)
		properties[service.WaitTimeoutProperty] = &trimmed
	}

	if input.Mounts != nil {
		if err := service.WriteMounts(input.Datastore, input.ServiceName, *input.Mounts); err != nil {
			return err
		}
	}

	if input.VolumeTargets != nil {
		if err := service.WriteVolumeTargets(input.Datastore, input.ServiceName, *input.VolumeTargets); err != nil {
			return err
		}
	}

	for key, value := range properties {
		if err := common.PropertyWrite(plugin, input.ServiceName, key, *value); err != nil {
			return fmt.Errorf("failed to write the %s property: %w", key, err)
		}
	}

	return nil
}

// upgradeTarget is the datastore an upgrade leaves a service running.
//
// The definition moves only when the image the service recorded and the one it
// is upgraded to resolve to different definitions, which is an upgrade across a
// major version or onto another flavor. Anything else keeps the definition the
// service is pinned to, even where the new image alone would resolve elsewhere:
// a service created with pgvector/pgvector:pg17 before pgvector had definitions
// of its own was pinned to postgres-18, and its data is where that definition
// mounts it rather than where postgres-pgvector-pg17 would look for it.
//
// A definition the upgrade named outright is where it lands, whatever the image
// says, since a tag that does not carry its major version is exactly what it is
// named for.
//
// Pure, so which definition an upgrade lands on is pinned by a test rather than
// by a docker daemon.
func upgradeTarget(s *service.Datastore, named *service.Datastore, recorded service.RecordedImage, image string, imageVersion string) *service.Datastore {
	if named != nil {
		return named
	}

	target := s.ForImage(image, imageVersion)
	if recorded.ImageVersion == "" {
		return target
	}

	if s.ForImage(recorded.Image, recorded.ImageVersion).DefinitionName() == target.DefinitionName() {
		return s
	}

	return target
}

// checkUpgradeMounts reports whether the mounts a service will have after an
// upgrade can be given to the container it is upgraded to: the ones the upgrade
// was asked for, and otherwise the ones the service already has. The same goes
// for where the service has the definition's volumes: a volume moved on one
// definition has to be one the definition it is upgraded to also mounts.
func checkUpgradeMounts(ctx context.Context, input UpgradeServiceInput, target *service.Datastore) error {
	var volumeTargets map[string]string
	if input.VolumeTargets != nil {
		volumeTargets = *input.VolumeTargets
	} else {
		stored, err := service.ServiceVolumeTargets(input.Datastore, input.ServiceName)
		if err != nil {
			return err
		}
		volumeTargets = stored
	}

	if err := service.CheckVolumeTargets(target.Definition, volumeTargets); err != nil {
		return fmt.Errorf("unable to upgrade %s: %w; clear the %s property or give --volume-target to move it", input.ServiceName, err, service.VolumeTargetsProperty)
	}

	var mounts []service.Mount
	if input.Mounts != nil {
		mounts = *input.Mounts
	} else {
		stored, err := service.ServiceMounts(input.Datastore, input.ServiceName)
		if err != nil {
			return err
		}
		mounts = stored
	}

	if err := service.CheckMounts(target.Definition, volumeTargets, mounts); err != nil {
		return fmt.Errorf("unable to upgrade %s: %w", input.ServiceName, err)
	}
	if err := service.CheckMountsOnHost(ctx, service.Folders(target, input.ServiceName).HostRoot, mounts); err != nil {
		return fmt.Errorf("unable to upgrade %s: %w", input.ServiceName, err)
	}

	return nil
}

// changeAppState stops or starts every app linked to a service
func changeAppState(ctx context.Context, action string, appNames []string) error {
	for _, appName := range appNames {
		if appName == "" || appName == "-" {
			continue
		}

		if _, err := execx.Run(ctx, common.ExecCommandInput{
			Command:      "dokku",
			Args:         []string{fmt.Sprintf("ps:%s", action), appName},
			StreamStderr: true,
			StreamStdout: true,
		}); err != nil {
			return fmt.Errorf("unable to %s app %s: %w", action, appName, err)
		}
	}

	return nil
}
