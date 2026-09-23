package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dokku/dokku-datastore/internal/cron"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// StagedCronFile is where a cron entry is written before the helper moves it
// into the root owned cron directory. It lives inside the service rather than
// beside it: the directory holding the services is enumerated to list them, so
// a staged file there is reported as a service of its own, which is what an
// interrupted schedule used to leave behind.
func StagedCronFile(s *service.Datastore, serviceName string) string {
	return filepath.Join(service.Folders(s, serviceName).Root, ".TMP_CRON_FILE")
}

// SudoersContents returns the sudoers file a datastore plugin installs: the
// cron helper every datastore has, and a line for each privileged script its
// definition ships.
//
// Every line names one script and constrains no argument, which is what keeps
// the file valid under sudo-rs. A rule matching arguments would be rejected
// outright and leave the dokku group with no privileges at all.
func SudoersContents(s *service.Datastore) string {
	contents := cron.SudoersContents(s.Properties().CommandPrefix)
	for _, file := range PrivilegedFiles(s) {
		contents += fmt.Sprintf("%%dokku ALL=(ALL) NOPASSWD:%s\n", file.Filename)
	}

	return contents
}

// SudoersFile describes the sudoers file a datastore plugin installs.
func SudoersFile(s *service.Datastore) common.WriteStringToFileInput {
	file := cron.SudoersFile(s.Properties().CommandPrefix)
	file.Content = SudoersContents(s)

	return file
}

// PrivilegedPath is where a definition's privileged script is installed. It is
// the same root owned directory the cron helper goes in, and for the same
// reason: being able to replace the script, or the directory holding it, would
// be a way to choose what the dokku group runs as root.
func PrivilegedPath(plugin string, name string) string {
	return filepath.Join("/usr/local/bin", fmt.Sprintf("dokku-%s-%s", plugin, name))
}

// PrivilegedFiles describes the privileged scripts a datastore installs, sorted
// so that the sudoers file they are granted in is written the same way twice.
//
// Taken across every definition the datastore is made of, because installing is
// a plugin wide step that happens before any service is named: a script only one
// major version ships still has to be there for the services on that version.
func PrivilegedFiles(s *service.Datastore) []common.WriteStringToFileInput {
	scripts := map[string][]byte{}
	for _, found := range s.Definitions() {
		for name, contents := range found.Privileged {
			scripts[name] = contents
		}
	}

	if len(scripts) == 0 {
		return nil
	}

	names := make([]string, 0, len(scripts))
	for name := range scripts {
		names = append(names, name)
	}
	sort.Strings(names)

	files := make([]common.WriteStringToFileInput, 0, len(names))
	for _, name := range names {
		files = append(files, common.WriteStringToFileInput{
			Content:   string(scripts[name]),
			Filename:  PrivilegedPath(s.Properties().CommandPrefix, name),
			GroupName: "root",
			Mode:      0755,
			Username:  "root",
		})
	}

	return files
}

// CronHelperFile describes the helper script the sudoers file grants.
func CronHelperFile(s *service.Datastore) common.WriteStringToFileInput {
	return cron.HelperFile(s.Properties().CommandPrefix, filepath.Join(service.PluginDataRoot, s.Properties().DataDirectory))
}

// InstallInput is the input for the Install function
type InstallInput struct {
	// Datastore is the datastore being installed
	Datastore *service.Datastore

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
		hostenv.BusyboxImage,
		hostenv.AmbassadorImage,
		hostenv.S3BackupImage,
		hostenv.WaitImage,
	}
	for _, image := range images {
		if err := service.ValidateTaggedImageExists(image); err == nil {
			continue
		}

		if os.Getenv(properties.ImagePullVariable) == "true" {
			input.Logger.Warn(WarnInput{
				Warning: fmt.Sprintf("%s environment variable detected. Not running pull command.", properties.ImagePullVariable),
			})
			input.Logger.Warn(WarnInput{Warning: fmt.Sprintf("docker image pull %s", image)})
			continue
		}

		if _, err := service.PullTaggedImage(ctx, image); err != nil {
			return fmt.Errorf("failed to pull image %s: %w", image, err)
		}
	}

	folders := []string{
		// where its services go, which is not always its name
		filepath.Join(service.PluginDataRoot, properties.DataDirectory),
		// and where dokku keeps the plugin's own config and its binary, which
		// always are: moving these would put the binary somewhere the plugin
		// that runs it does not look
		filepath.Join(service.DokkuLibRoot, "config", commandPrefix),
		filepath.Join(service.DokkuLibRoot, "data", commandPrefix),
	}
	if err := CreateServiceFolders(folders, hostenv.SystemUser(), hostenv.SystemGroup()); err != nil {
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

	// and so does anything the definition ships, for the same reason
	for _, file := range PrivilegedFiles(input.Datastore) {
		if err := common.WriteStringToFile(file); err != nil {
			return fmt.Errorf("unable to write %s: %w", file.Filename, err)
		}
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
	strayCronFile := filepath.Join(service.PluginDataRoot, input.Datastore.Properties().DataDirectory, ".TMP_CRON_FILE")
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
		serviceFiles := service.Files(input.Datastore, serviceName)

		// older services recorded the image only on the container, so recover it
		// onto disk where everything else now looks for it. Gated on what the
		// files say rather than on whether they exist, because an empty file is
		// read as saying nothing everywhere else and was never repaired here.
		if _, err := service.RecoverRecordedImage(ctx, service.RecoverRecordedImageInput{
			Datastore:   input.Datastore,
			ServiceName: serviceName,
		}); err != nil {
			return err
		}

		// a service created before the pin existed gets the definition its
		// recorded version resolves to, which for a datastore split by major
		// version is not the one it has been running: every such service was
		// placed on the newest definition whatever it was created with
		//
		// A service already naming a definition this plugin does not ship keeps
		// the name it has. Overwriting it with the fallback would throw away the
		// only record of what the service was created with, which is the one
		// thing needed to put it right.
		pinned, unresolved := input.Datastore.ForService(serviceName)
		if unresolved != nil {
			input.Logger.Warn(WarnInput{Warning: unresolved.Error()})
		} else if err := service.PinDefinition(pinned, serviceName); err != nil {
			return err
		}

		// the config options file used to be named after the plugin variable
		legacyConfigOptions := filepath.Join(service.Folders(input.Datastore, serviceName).Root,
			fmt.Sprintf("%s_CONFIG_OPTIONS", properties.PluginVariable))
		if common.FileExists(legacyConfigOptions) {
			if err := os.Rename(legacyConfigOptions, serviceFiles.ConfigOptions); err != nil {
				return fmt.Errorf("unable to rename %s: %w", legacyConfigOptions, err)
			}
			if err := common.SetPermissions(common.SetPermissionInput{
				Filename:  serviceFiles.ConfigOptions,
				GroupName: hostenv.SystemGroup(),
				Mode:      0644,
				Username:  hostenv.SystemUser(),
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
		GroupName: hostenv.SystemGroup(),
		Mode:      0644,
		Username:  hostenv.SystemUser(),
	}); err != nil {
		return fmt.Errorf("unable to write %s: %w", filename, err)
	}

	return nil
}
