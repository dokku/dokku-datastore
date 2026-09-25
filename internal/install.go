package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// SudoersContents returns the sudoers file a datastore plugin installs: a line
// for each privileged script its definition ships, and nothing when it ships
// none.
//
// Every line names one script and constrains no argument, which is what keeps
// the file valid under sudo-rs. A rule matching arguments would be rejected
// outright and leave the dokku group with no privileges at all.
func SudoersContents(s *service.Datastore) string {
	contents := ""
	for _, file := range PrivilegedFiles(s) {
		contents += fmt.Sprintf("%%dokku ALL=(ALL) NOPASSWD:%s\n", file.Filename)
	}

	return contents
}

// SudoersFile describes the sudoers file a datastore plugin installs. It has to
// belong to root: sudo refuses a file writable by anyone else, and the dokku
// user must not be able to rewrite the privileges it is being granted.
func SudoersFile(s *service.Datastore) common.WriteStringToFileInput {
	return common.WriteStringToFileInput{
		Content:   SudoersContents(s),
		Filename:  filepath.Join("/etc/sudoers.d", fmt.Sprintf("dokku-%s", s.Properties().CommandPrefix)),
		GroupName: "root",
		Mode:      0440,
		Username:  "root",
	}
}

// LegacyCronHelperPath is where earlier versions of the plugin installed the
// helper that moved a scheduled backup's cron file into place as root.
// Scheduled backups are now handed to dokku through the cron-entries trigger,
// so the helper is removed on install.
func LegacyCronHelperPath(s *service.Datastore) string {
	return PrivilegedPath(s.Properties().CommandPrefix, "cron")
}

// PrivilegedPath is where a definition's privileged script is installed. It has
// to be a directory root owns: being able to replace the script, or the
// directory holding it, would be a way to choose what the dokku group runs as
// root.
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
	for _, reference := range images {
		// no service name, because an install happens before any service does.
		// It is also the one caller that carries on past a disabled pull: a host
		// told not to fetch still has to end up with a plugin it can run, and
		// every command that needs one of these now fetches it when it gets
		// there rather than trusting this to have done it
		err := service.EnsureTaggedImage(ctx, service.EnsureTaggedImageInput{
			Datastore:   input.Datastore,
			TaggedImage: reference,
		})
		if errors.Is(err, service.ErrPullDisabled) {
			for _, line := range strings.Split(err.Error(), "\n") {
				input.Logger.Warn(WarnInput{Warning: line})
			}
			continue
		}
		if err != nil {
			return err
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

	privilegedFiles := PrivilegedFiles(input.Datastore)
	if len(privilegedFiles) > 0 {
		if err := os.MkdirAll(filepath.Dir(privilegedFiles[0].Filename), 0755); err != nil {
			return fmt.Errorf("unable to create %s: %w", filepath.Dir(privilegedFiles[0].Filename), err)
		}
	}

	// the scripts go in first, so the sudoers rule never names a script that is
	// not there yet
	for _, file := range privilegedFiles {
		if err := common.WriteStringToFile(file); err != nil {
			return fmt.Errorf("unable to write %s: %w", file.Filename, err)
		}
	}

	// a datastore that ships no privileged script grants nothing, so it has no
	// sudoers file. One written by an earlier version granted the cron helper
	sudoersFile := SudoersFile(input.Datastore)
	if len(privilegedFiles) > 0 {
		if err := common.WriteStringToFile(sudoersFile); err != nil {
			return fmt.Errorf("unable to write %s: %w", sudoersFile.Filename, err)
		}
	} else if err := removeIfExists(sudoersFile.Filename); err != nil {
		return err
	}

	// and the helper goes last, once no sudoers rule names it. A definition
	// shipping a privileged script of the same name has just written it there
	legacyCronHelper := LegacyCronHelperPath(input.Datastore)
	if !slices.ContainsFunc(privilegedFiles, func(file common.WriteStringToFileInput) bool {
		return file.Filename == legacyCronHelper
	}) {
		if err := removeIfExists(legacyCronHelper); err != nil {
			return err
		}
	}

	return migrateServices(ctx, input)
}

// removeIfExists removes a file, and does nothing when it is already gone
func removeIfExists(filename string) error {
	if err := os.Remove(filename); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("unable to remove %s: %w", filename, err)
	}

	return nil
}

// migrateLegacyCronFile moves a scheduled backup an earlier version of the
// plugin wrote to the cron directory onto the properties the cron-entries
// trigger reads, and reports whether anything changed.
//
// A schedule cron cannot run is dropped rather than moved. It never ran where it
// was, and carried into the dokku crontab it would stop every other task in it
// from running too, since the crontab is refused as a whole.
func migrateLegacyCronFile(input InstallInput, serviceName string) (bool, error) {
	cronFile := service.LegacyCronFile(input.Datastore, serviceName)
	if !common.FileExists(cronFile) {
		return false, nil
	}

	commandPrefix := input.Datastore.Properties().CommandPrefix
	schedule, ok := ParseCronEntry(commandPrefix, common.ReadFirstLine(cronFile))
	if !ok {
		input.Logger.Warn(WarnInput{Warning: fmt.Sprintf("Unable to read the scheduled backup for %s from %s, leaving it in place", serviceName, cronFile)})
		return false, nil
	}

	if err := schedule.Validate(); err != nil {
		input.Logger.Warn(WarnInput{Warning: fmt.Sprintf("Removing the scheduled backup for %s, which cron could not run: %s", serviceName, err)})
		input.Logger.Warn(WarnInput{Warning: fmt.Sprintf("Schedule it again with: dokku %s:backup-schedule %s <schedule> <bucket-name>", commandPrefix, serviceName)})
	} else if err := writeBackupSchedule(input.Datastore, serviceName, schedule); err != nil {
		return false, err
	}

	if err := os.Remove(cronFile); err != nil {
		return false, fmt.Errorf("unable to remove %s: %w", cronFile, err)
	}

	return true, nil
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
	crontabChanged := false
	for _, serviceName := range services {
		serviceFiles := service.Files(input.Datastore, serviceName)

		// the cron helper staged a cron file inside the service before moving
		// it into place, and an interrupted schedule left it there
		if err := removeIfExists(filepath.Join(service.Folders(input.Datastore, serviceName).Root, ".TMP_CRON_FILE")); err != nil {
			return err
		}

		changed, err := migrateLegacyCronFile(input, serviceName)
		if err != nil {
			return err
		}
		crontabChanged = crontabChanged || changed

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
			pinned = input.Datastore
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
				Mode:      service.PrivateFileMode,
				Username:  hostenv.SystemUser(),
			}); err != nil {
				return fmt.Errorf("unable to set permissions on %s: %w", serviceFiles.ConfigOptions, err)
			}
		}

		if err := restrictServiceSecrets(pinned, serviceName); err != nil {
			return err
		}
	}

	if crontabChanged {
		return regenerateCrontab(ctx)
	}

	return nil
}

// restrictServiceSecrets takes away access by other users to every file of a
// service that holds a secret. Older versions of the plugin, and the bash
// plugins before them, left backup credentials, the custom environment and the
// rendered compose file readable by everyone, and writing them privately from
// now on does nothing for a file that is never written again.
func restrictServiceSecrets(s *service.Datastore, serviceName string) error {
	serviceFolders := service.Folders(s, serviceName)
	serviceFiles := service.Files(s, serviceName)

	restrict := func(filename string, mode os.FileMode) error {
		if _, err := os.Stat(filename); errors.Is(err, os.ErrNotExist) {
			return nil
		}

		if err := common.SetPermissions(common.SetPermissionInput{
			Filename:  filename,
			GroupName: hostenv.SystemGroup(),
			Mode:      mode,
			Username:  hostenv.SystemUser(),
		}); err != nil {
			return fmt.Errorf("unable to set permissions on %s: %w", filename, err)
		}

		return nil
	}

	for _, folder := range []string{serviceFolders.Backup, serviceFolders.BackupEncryption} {
		entries, err := os.ReadDir(folder)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("unable to read %s: %w", folder, err)
		}

		if err := restrict(folder, BackupFolderMode); err != nil {
			return err
		}

		for _, entry := range entries {
			if !entry.Type().IsRegular() {
				continue
			}

			if err := restrict(filepath.Join(folder, entry.Name()), service.PrivateFileMode); err != nil {
				return err
			}
		}
	}

	files := []string{serviceFiles.Compose, serviceFiles.Env, serviceFiles.ConfigOptions}
	for _, secret := range s.Definition.Dokku.Secrets {
		files = append(files, filepath.Join(serviceFolders.Root, secret.File))
	}

	for _, filename := range files {
		if err := restrict(filename, service.PrivateFileMode); err != nil {
			return err
		}
	}

	return nil
}

// writeServiceFile writes one of a service's metadata files. It replaces the
// file rather than rewriting it, so that a file tightened to a private mode is
// never left holding its new contents under the mode it had before.
func writeServiceFile(filename string, contents string, mode os.FileMode) error {
	if err := service.ReplaceFileAtomically(filename, contents, mode); err != nil {
		return fmt.Errorf("unable to write %s: %w", filename, err)
	}

	return nil
}
