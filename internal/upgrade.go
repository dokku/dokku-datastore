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

// UpgradeServiceInput is the input for the UpgradeService function
type UpgradeServiceInput struct {
	// ConfigOptions are extra arguments passed to the container create command
	ConfigOptions string

	// CustomEnv is the environment the service is started with
	CustomEnv string

	// Datastore is the datastore the service belongs to
	Datastore datastores.Datastore

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

// UpgradeService recreates a service's container on a different image
func UpgradeService(ctx context.Context, input UpgradeServiceInput) error {
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
				fmt.Sprintf("%s service upgrade failed", input.ServiceName),
			}
			return errors.New(strings.Join(message, "\n"))
		}

		if _, err := datastores.PullTaggedImage(ctx, taggedImage); err != nil {
			return fmt.Errorf("failed to pull image %s: %w", taggedImage, err)
		}
	}

	currentImage := datastores.Version(ctx, datastores.VersionInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if currentImage == taggedImage {
		input.Logger.Info(fmt.Sprintf("Service %s already running %s", input.ServiceName, taggedImage)) //nolint:errcheck
		return nil
	}

	linkedApps := datastores.LinkedApps(ctx, datastores.LinkedAppsInput{
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
	if err := datastores.RemoveServiceContainer(ctx, datastores.RemoveServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	}); err != nil {
		return err
	}

	if err := input.Datastore.CreateServiceContainer(ctx, datastores.CreateServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		TaggedImage: taggedImage,
	}); err != nil {
		return err
	}

	if err := datastores.Start(ctx, datastores.StartInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
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

// changeAppState stops or starts every app linked to a service
func changeAppState(ctx context.Context, action string, appNames []string) error {
	for _, appName := range appNames {
		if appName == "" || appName == "-" {
			continue
		}

		if _, err := common.CallExecCommandWithContext(ctx, common.ExecCommandInput{
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
