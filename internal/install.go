package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dokku/dokku-datastore/internal/datastores"
	"github.com/dokku/dokku/plugins/common"
)

// sudoersTemplate grants the dokku group the handful of root commands the
// backup schedule needs. The cron directory belongs to root, so the file has to
// be moved into place and given its ownership through sudo.
const sudoersTemplate = `%%dokku ALL=(ALL) NOPASSWD:/bin/rm -f /etc/cron.d/dokku-%[1]s-*
%%dokku ALL=(ALL) NOPASSWD:/bin/chown root\:root /etc/cron.d/dokku-%[1]s-*
%%dokku ALL=(ALL) NOPASSWD:/bin/chmod 644 /etc/cron.d/dokku-%[1]s-*
%%dokku ALL=(ALL) NOPASSWD:/bin/mv %[2]s/.TMP_CRON_FILE /etc/cron.d/dokku-%[1]s-*
%%dokku ALL=(ALL) NOPASSWD:/bin/chown 8983 %[2]s/*
%%dokku ALL=(ALL) NOPASSWD:/bin/chgrp 8983 %[2]s/*
`

// SudoersContents returns the sudoers file a datastore plugin installs
func SudoersContents(s datastores.Datastore) string {
	commandPrefix := s.Properties().CommandPrefix
	return fmt.Sprintf(sudoersTemplate, commandPrefix, filepath.Join(datastores.PluginDataRoot, commandPrefix))
}

// InstallInput is the input for the Install function
type InstallInput struct {
	// Datastore is the datastore being installed
	Datastore datastores.Datastore

	// Logger reports progress
	Logger Ui
}

// Install prepares the host for a datastore plugin. It is run on both install
// and update, so every step has to be safe to repeat.
func Install(ctx context.Context, input InstallInput) error {
	properties := input.Datastore.Properties()
	commandPrefix := properties.CommandPrefix

	if err := common.PropertySetup(commandPrefix); err != nil {
		return fmt.Errorf("unable to set up plugin properties: %w", err)
	}

	images := []string{
		fmt.Sprintf("%s:%s", properties.DefaultImage, properties.DefaultImageVersion),
		datastores.PluginBusyboxImage,
		datastores.PluginAmbassadorImage,
		datastores.PluginS3BackupImage,
		datastores.PluginWaitImage,
	}
	for _, image := range images {
		if err := datastores.ValidateTaggedImageExists(image); err == nil {
			continue
		}

		if os.Getenv(properties.ImagePullVariable) == "true" {
			input.Logger.Warn(WarnInput{
				Warning: fmt.Sprintf("%s environment variable detected. Not running pull command.", properties.ImagePullVariable),
			})
			input.Logger.Warn(WarnInput{Warning: fmt.Sprintf("docker image pull %s", image)})
			continue
		}

		if _, err := datastores.PullTaggedImage(ctx, image); err != nil {
			return fmt.Errorf("failed to pull image %s: %w", image, err)
		}
	}

	folders := []string{
		filepath.Join(datastores.PluginDataRoot, commandPrefix),
		filepath.Join(datastores.DokkuLibRoot, "config", commandPrefix),
		filepath.Join(datastores.DokkuLibRoot, "data", commandPrefix),
	}
	if err := CreateServiceFolders(folders, datastores.SystemUser(), datastores.SystemGroup()); err != nil {
		return err
	}

	sudoersFile := filepath.Join("/etc/sudoers.d", fmt.Sprintf("dokku-%s", commandPrefix))
	if err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:  SudoersContents(input.Datastore),
		Filename: sudoersFile,
		Mode:     0440,
	}); err != nil {
		return fmt.Errorf("unable to write %s: %w", sudoersFile, err)
	}

	return migrateServices(ctx, input)
}

// migrateServices brings services created by older versions of the plugin up to
// the layout the current one expects
func migrateServices(ctx context.Context, input InstallInput) error {
	services, err := ListServices(ctx, ListServicesInput{Datastore: input.Datastore})
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	properties := input.Datastore.Properties()
	for _, serviceName := range services {
		serviceFiles := datastores.Files(input.Datastore, serviceName)

		// older services recorded the image only on the container, so recover it
		// onto disk where everything else now looks for it
		if !common.FileExists(serviceFiles.Image) || !common.FileExists(serviceFiles.ImageVersion) {
			taggedImage := datastores.Version(ctx, datastores.VersionInput{
				Datastore:   input.Datastore,
				ServiceName: serviceName,
			})
			if image, imageVersion, found := strings.Cut(taggedImage, ":"); found {
				if err := writeServiceFile(serviceFiles.Image, image); err != nil {
					return err
				}
				if err := writeServiceFile(serviceFiles.ImageVersion, imageVersion); err != nil {
					return err
				}
			}
		}

		// the config options file used to be named after the plugin variable
		legacyConfigOptions := filepath.Join(datastores.Folders(input.Datastore, serviceName).Root,
			fmt.Sprintf("%s_CONFIG_OPTIONS", properties.PluginVariable))
		if common.FileExists(legacyConfigOptions) {
			if err := os.Rename(legacyConfigOptions, serviceFiles.ConfigOptions); err != nil {
				return fmt.Errorf("unable to rename %s: %w", legacyConfigOptions, err)
			}
			if err := common.SetPermissions(common.SetPermissionInput{
				Filename:  serviceFiles.ConfigOptions,
				GroupName: datastores.SystemGroup(),
				Mode:      0644,
				Username:  datastores.SystemUser(),
			}); err != nil {
				return fmt.Errorf("unable to set permissions on %s: %w", serviceFiles.ConfigOptions, err)
			}
		}
	}

	return nil
}

// writeServiceFile writes one of a service's metadata files
func writeServiceFile(filename string, contents string) error {
	if err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   contents,
		Filename:  filename,
		GroupName: datastores.SystemGroup(),
		Mode:      0644,
		Username:  datastores.SystemUser(),
	}); err != nil {
		return fmt.Errorf("unable to write %s: %w", filename, err)
	}

	return nil
}
