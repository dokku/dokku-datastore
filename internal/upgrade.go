package internal

import (
	"context"
	"fmt"
	"os"
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

	// LogDriver is the docker logging driver the service container is run with
	LogDriver *string

	// LogOptions are the docker log options the service container is run with
	LogOptions *[]string

	// RestartPolicy is the docker restart policy the service container is run
	// with
	RestartPolicy *string

	// Mounts are the host paths and docker volumes mounted into the service
	// container
	Mounts *[]service.Mount

	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

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
		i.LogDriver != nil ||
		i.LogOptions != nil ||
		i.RestartPolicy != nil ||
		i.Mounts != nil
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

	imageVersion, err := upgradeVersion(input.Datastore.Definition, recorded, input.Image, input.ImageVersion)
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
	// an upgrade to the image a service already runs is nothing to do - unless it
	// also asked to change a setting, which is a recreate whatever the image says
	if currentImage == taggedImage && !input.changesSettings() {
		input.Logger.Info(fmt.Sprintf("Service %s already running %s", input.ServiceName, taggedImage)) //nolint:errcheck
		return nil
	}

	// before the old container is taken away, for the same reason the log
	// config is checked first, and against the definition the upgrade lands on:
	// one that crosses a major version can mount its data somewhere else. The
	// mounts the service already has are checked too, since a host path removed
	// since it was mounted would otherwise only be found once there is no
	// container left to go back to
	if err := checkUpgradeMounts(input, taggedImage); err != nil {
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
	image, imageVersion, _ := definition.CutImage(taggedImage)
	if err := service.RecordImage(service.RecordImageInput{
		Datastore:    input.Datastore,
		Image:        image,
		ImageVersion: imageVersion,
		ServiceName:  input.ServiceName,
	}); err != nil {
		return err
	}

	input.Datastore = input.Datastore.ForImageVersion(imageVersion)
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

	// the config options can carry credentials, so they are kept private
	files := []struct {
		filename string
		value    *string
		mode     os.FileMode
	}{
		{filename: serviceFiles.ConfigOptions, value: input.ConfigOptions, mode: service.PrivateFileMode},
		{filename: serviceFiles.ShmSize, value: input.ShmSize, mode: 0644},
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

	if input.Mounts != nil {
		if err := service.WriteMounts(input.Datastore, input.ServiceName, *input.Mounts); err != nil {
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

// checkUpgradeMounts reports whether the mounts a service will have after an
// upgrade can be given to the container it is upgraded to: the ones the upgrade
// was asked for, and otherwise the ones the service already has.
func checkUpgradeMounts(input UpgradeServiceInput, taggedImage string) error {
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

	_, imageVersion, _ := definition.CutImage(taggedImage)
	target := input.Datastore.ForImageVersion(imageVersion)
	if err := service.CheckMounts(target.Definition, mounts); err != nil {
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
