package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dokku/dokku-datastore/internal/cron"
	"github.com/dokku/dokku-datastore/internal/datastores"
	"github.com/dokku/dokku/plugins/common"
)

// StagedCronFile is where a cron entry is written before the helper moves it
// into the root owned cron directory. It lives inside the service rather than
// beside it: the directory holding the services is enumerated to list them, so
// a staged file there is reported as a service of its own, which is what an
// interrupted schedule used to leave behind.
func StagedCronFile(s datastores.Datastore, serviceName string) string {
	return filepath.Join(datastores.Folders(s, serviceName).Root, ".TMP_CRON_FILE")
}

// SudoersContents returns the sudoers file a datastore plugin installs
func SudoersContents(s datastores.Datastore) string {
	return cron.SudoersContents(s.Properties().CommandPrefix)
}

// SudoersFile describes the sudoers file a datastore plugin installs.
func SudoersFile(s datastores.Datastore) common.WriteStringToFileInput {
	return cron.SudoersFile(s.Properties().CommandPrefix)
}

// CronHelperFile describes the helper script the sudoers file grants.
func CronHelperFile(s datastores.Datastore) common.WriteStringToFileInput {
	return cron.HelperFile(s.Properties().CommandPrefix, filepath.Join(datastores.PluginDataRoot, s.Properties().CommandPrefix))
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

	helperFile := CronHelperFile(input.Datastore)
	if err := os.MkdirAll(filepath.Dir(helperFile.Filename), 0755); err != nil {
		return fmt.Errorf("unable to create %s: %w", filepath.Dir(helperFile.Filename), err)
	}

	// the helper goes in first, so the sudoers rule never names a script that is
	// not there yet
	if err := common.WriteStringToFile(helperFile); err != nil {
		return fmt.Errorf("unable to write %s: %w", helperFile.Filename, err)
	}

	sudoersFile := SudoersFile(input.Datastore)
	if err := common.WriteStringToFile(sudoersFile); err != nil {
		return fmt.Errorf("unable to write %s: %w", sudoersFile.Filename, err)
	}

	return migrateServices(ctx, input)
}

// migrateServices brings services created by older versions of the plugin up to
// the layout the current one expects
func migrateServices(ctx context.Context, input InstallInput) error {
	// earlier versions staged a cron entry beside the services rather than
	// inside one, where listing the services reported it as a service of its
	// own. An interrupted schedule left one behind, so clear it.
	strayCronFile := filepath.Join(datastores.PluginDataRoot, input.Datastore.Properties().CommandPrefix, ".TMP_CRON_FILE")
	if common.FileExists(strayCronFile) {
		if err := os.Remove(strayCronFile); err != nil {
			return fmt.Errorf("unable to remove %s: %w", strayCronFile, err)
		}
	}

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
