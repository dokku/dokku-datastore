package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dokku/dokku-datastore/internal/backend"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
	"gopkg.in/yaml.v3"
)

const (
	// ServiceBackupManifest is the file under a scope's data directory that
	// records what a service was made with, which is what import reads back
	ServiceBackupManifest = "service.json"

	// serviceBackupSeedRoot is the directory under a scope's data directory
	// holding the files copied out of the service root, laid out as they were
	serviceBackupSeedRoot = "root"

	// serviceBackupDumps is the directory under a scope's data directory
	// holding the logical dumps of the service's data
	serviceBackupDumps = "dumps"

	// serviceBackupOwnDump is the dump of the service's own database, or of
	// everything a datastore with a fixed set of databases holds
	serviceBackupOwnDump = "service.dump"

	// serviceBackupDatabaseDumps is the directory under the dumps holding one
	// dump for each database made in the service, named after it
	serviceBackupDatabaseDumps = "databases"
)

// serviceBackupSeedFolders are the folders under a service root a backup
// copies whole: the config files the definition seeded, which an operator may
// have edited since, and the backup credentials and encryption settings
var serviceBackupSeedFolders = []string{"config", "backup", "backup-encryption"}

// ServiceBackup is what a backup records about a service beside its data, so
// that a restore makes it again with what its linked apps were handed.
//
// The definition, image, version and backend place the new service where the
// old one ran, and the settings are what clone carries over. The properties
// are every property of the service, written back as they were, which covers
// the link config keys and accounts, the expose and backup settings and the
// extra arguments. The ports are the ones the service was exposed on, the
// users are the accounts made in it with their passwords, and the databases
// are the ones made in it beside its own.
//
// The secrets, the links file, the database name and the folders in
// serviceBackupSeedFolders are not recorded here but copied as files into the
// scope's data directory under serviceBackupSeedRoot, and the container id,
// rendered compose file, pre-create record and the data volume are left out:
// the first three are made again with the container, and the data travels as
// a logical dump instead.
type ServiceBackup struct {
	// Type is the datastore the service belongs to
	Type string `json:"type"`

	// Name is the name of the service
	Name string `json:"name"`

	// Definition is the definition the service runs
	Definition string `json:"definition"`

	// Image is the image the service runs, without its tag
	Image string `json:"image"`

	// ImageVersion is the tag of the image the service runs
	ImageVersion string `json:"image_version"`

	// Backend is the execution backend the service was created with
	Backend string `json:"backend,omitempty"`

	// Settings are the settings the service runs with
	Settings serviceSettings `json:"settings"`

	// Properties are every property of the service, keyed by name
	Properties map[string]string `json:"properties,omitempty"`

	// Ports are the host ports the service was exposed on
	Ports []string `json:"ports,omitempty"`

	// Users are the accounts made in the service, keyed by name, holding each
	// one's password
	Users map[string]string `json:"users,omitempty"`

	// Databases are the databases made in the service beside its own
	Databases []string `json:"databases,omitempty"`

	// Dumped is whether the backup holds a dump of the service's data, which
	// it does not for a datastore whose definition cannot export one or for a
	// service that was not running when it was backed up. A restore of a
	// backup without one makes the service with no data
	Dumped bool `json:"dumped"`
}

// BackupServiceInput is the input for ExportServiceBackup and
// ImportServiceBackup
type BackupServiceInput struct {
	TriggerInput

	// ServiceType is the datastore type dokku names the service by
	ServiceType string

	// ServiceName is the name of the service
	ServiceName string

	// ScopeDir is the directory the backup gives the service, with recipe
	// slices under its config directory and everything else under its data
	// directory
	ScopeDir string

	// Force is whether a restore may destroy a service that already exists to
	// make it again from the backup, which dokku asks for by setting
	// DOKKU_BACKUP_FORCE when backup:import is given --force
	Force bool
}

// BackupForceEnv is the environment variable dokku sets to 1 on the
// backup-service-import trigger when backup:import is given --force and the
// service already exists
const BackupForceEnv = "DOKKU_BACKUP_FORCE"

// BackupForceRequested reports whether dokku asked for a restore to replace a
// service that already exists. Only 1 asks for it.
func BackupForceRequested() bool {
	return os.Getenv(BackupForceEnv) == "1"
}

// destroyExistingService destroys the service a forced restore replaces. A
// variable so that a test can see the destroy asked for without a docker
// daemon to run it against.
var destroyExistingService = DestroyService

// handles reports whether the input names a service of the datastore the
// trigger runs for, which every other datastore leaves alone
func (input BackupServiceInput) handles() bool {
	return input.ServiceType == input.Datastore.Properties().CommandPrefix
}

// validate refuses an input that names this datastore but not a service or
// somewhere to put it
func (input BackupServiceInput) validate() error {
	if input.ServiceName == "" {
		return service.ErrMissingServiceName
	}

	if err := service.ValidateServiceName(input.ServiceName); err != nil {
		return err
	}

	if input.ScopeDir == "" {
		return errors.New("the backup scope directory is required")
	}

	return nil
}

// dataDir is where everything but the recipe slice is written
func (input BackupServiceInput) dataDir() string {
	return filepath.Join(input.ScopeDir, "data")
}

// configDir is where the recipe slice is written
func (input BackupServiceInput) configDir() string {
	return filepath.Join(input.ScopeDir, "config")
}

// ExportServiceBackup writes a service into the scope directory dokku's
// backup:export gives it, and does nothing for a service of another datastore.
//
// The scope's data directory gets the ServiceBackup record as
// ServiceBackupManifest, the secrets, links file, database name and the
// serviceBackupSeedFolders under serviceBackupSeedRoot, and the logical dumps
// under serviceBackupDumps: one of the service's own database, and one for each
// database made in it. A definition that cannot export has no dumps, and its
// service is restored with its settings but none of its data. A service that is
// not running has no dumps either, and is only warned about, so that one
// stopped service does not stop the rest of a backup. The scope's
// config directory gets a recipe slice that creates the service and links it to
// its apps, which is there to be read and is never read back.
func ExportServiceBackup(ctx context.Context, input BackupServiceInput) error {
	if !input.handles() {
		return nil
	}

	if err := input.validate(); err != nil {
		return err
	}

	datastore, err := input.Datastore.ForService(input.ServiceName)
	if err != nil {
		return err
	}
	input.Datastore = datastore

	if !service.Exists(ctx, datastore, input.ServiceName) {
		return fmt.Errorf("service %s does not exist", input.ServiceName)
	}

	record, err := readServiceBackup(ctx, datastore, input.ServiceName)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(input.dataDir(), 0700); err != nil {
		return fmt.Errorf("unable to create %s: %w", input.dataDir(), err)
	}

	if err := writeSeedRoot(datastore, input.ServiceName, filepath.Join(input.dataDir(), serviceBackupSeedRoot)); err != nil {
		return err
	}

	switch {
	case !datastore.Definition.Implements("export"):
		input.Logger.Warn(WarnInput{
			Warning: fmt.Sprintf("the %s datastore cannot export its data, so only the settings of %s are backed up", datastore.Definition.Dokku.Plugin, input.ServiceName),
		})
	case !serviceRunning(ctx, datastore, input.ServiceName):
		input.Logger.Warn(WarnInput{
			Warning: fmt.Sprintf("%s service %s is not running, so only its settings are backed up and not its data; start it and back it up again to include its data", input.ServiceType, input.ServiceName),
		})
	default:
		if err := exportServiceDumps(ctx, input, record); err != nil {
			return err
		}
		record.Dumped = true
	}

	if err := writeServiceBackupManifest(filepath.Join(input.dataDir(), ServiceBackupManifest), record); err != nil {
		return err
	}

	return writeServiceRecipe(input.configDir(), record, service.LinkedApps(ctx, linkedAppsInput(datastore, input.ServiceName)))
}

// readServiceBackup reads what a backup records about a service beside its
// files and its data
func readServiceBackup(ctx context.Context, datastore *service.Datastore, serviceName string) (ServiceBackup, error) {
	recorded, err := service.RecoverRecordedImage(ctx, service.RecoverRecordedImageInput{
		Datastore:   datastore,
		ServiceName: serviceName,
	})
	if err != nil {
		return ServiceBackup{}, err
	}
	if !recorded.Complete() {
		return ServiceBackup{}, fmt.Errorf("unable to determine the image %s runs", serviceName)
	}

	settings, err := readServiceSettings(datastore, serviceName)
	if err != nil {
		return ServiceBackup{}, err
	}

	properties, err := common.PropertyGetAll(datastore.Properties().CommandPrefix, serviceName)
	if err != nil {
		return ServiceBackup{}, fmt.Errorf("unable to read the properties of %s: %w", serviceName, err)
	}

	users := map[string]string{}
	for _, user := range service.CreatedUsers(datastore, serviceName) {
		users[user] = service.UserPassword(datastore, serviceName, user)
	}

	databases := []string{}
	if datastore.Definition.Dokku.Databases.Makes() {
		databases = service.CreatedDatabases(datastore, serviceName)
	}

	return ServiceBackup{
		Type:         datastore.Properties().CommandPrefix,
		Name:         serviceName,
		Definition:   datastore.DefinitionName(),
		Image:        recorded.Image,
		ImageVersion: recorded.ImageVersion,
		Backend:      datastore.RecordedBackend(serviceName),
		Settings:     settings,
		Properties:   properties,
		Ports:        service.ExposedHostPorts(datastore, serviceName),
		Users:        users,
		Databases:    databases,
	}, nil
}

// writeSeedRoot copies the files of a service root a restore seeds the new
// service with: every secret the definition declares, the links file, the
// database name and the serviceBackupSeedFolders. A file the service does not
// have is skipped, and the database name is written from what the service
// reads it as when it has none recorded.
func writeSeedRoot(datastore *service.Datastore, serviceName string, destination string) error {
	root := service.Folders(datastore, serviceName).Root
	files := service.Files(datastore, serviceName)

	names := []string{filepath.Base(files.Links)}
	for _, secret := range datastore.Definition.Dokku.Secrets {
		names = append(names, secret.File)
	}
	slices.Sort(names)

	if err := os.MkdirAll(destination, 0700); err != nil {
		return fmt.Errorf("unable to create %s: %w", destination, err)
	}

	for _, name := range slices.Compact(names) {
		if err := copyRegularFile(filepath.Join(root, name), filepath.Join(destination, name)); err != nil {
			return err
		}
	}

	databaseName := service.DatabaseName(datastore, serviceName)
	if err := os.WriteFile(filepath.Join(destination, filepath.Base(files.DatabaseName)), []byte(databaseName), service.PrivateFileMode); err != nil {
		return fmt.Errorf("unable to record the database name of %s: %w", serviceName, err)
	}

	for _, folder := range serviceBackupSeedFolders {
		if err := copyTree(filepath.Join(root, folder), filepath.Join(destination, folder)); err != nil {
			return err
		}
	}

	return nil
}

// copyRegularFile copies one file with its mode, and skips a source that is
// missing or is not a regular file, a symlink included
func copyRegularFile(source string, destination string) error {
	stat, err := os.Lstat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("unable to read %s: %w", source, err)
	}
	if !stat.Mode().IsRegular() {
		return nil
	}

	contents, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("unable to read %s: %w", source, err)
	}

	if err := os.WriteFile(destination, contents, stat.Mode().Perm()); err != nil {
		return fmt.Errorf("unable to write %s: %w", destination, err)
	}

	return nil
}

// copyTree copies every regular file under a directory, keeping its layout,
// and skips a source that is missing. Symlinks are skipped rather than
// followed, so a copy never reaches outside the directory it was given.
func copyTree(source string, destination string) error {
	if _, err := os.Lstat(source); os.IsNotExist(err) {
		return nil
	}

	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("unable to read %s: %w", path, err)
		}

		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)

		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}

		return copyRegularFile(path, target)
	})
}

// serviceRunning reports whether a service is running, which its datastore
// has to be for its data to be exported
func serviceRunning(ctx context.Context, datastore *service.Datastore, serviceName string) bool {
	status := service.Status(ctx, service.StatusInput{
		Datastore:   datastore,
		ServiceName: serviceName,
	})
	return strings.ToLower(status) == "running"
}

// exportServiceDumps writes a dump of the service's own database, and one of
// each database made in it, under the scope's dumps directory
func exportServiceDumps(ctx context.Context, input BackupServiceInput, record ServiceBackup) error {
	dumps := filepath.Join(input.dataDir(), serviceBackupDumps)
	if err := os.MkdirAll(filepath.Join(dumps, serviceBackupDatabaseDumps), 0700); err != nil {
		return fmt.Errorf("unable to create %s: %w", dumps, err)
	}

	if err := exportServiceDump(ctx, input, "", filepath.Join(dumps, serviceBackupOwnDump)); err != nil {
		return err
	}

	for _, database := range record.Databases {
		if err := exportServiceDump(ctx, input, database, filepath.Join(dumps, serviceBackupDatabaseDumps, database)); err != nil {
			return err
		}
	}

	return nil
}

// exportServiceDump writes the dump of one database, the service's own when it
// is empty, to a file
func exportServiceDump(ctx context.Context, input BackupServiceInput, database string, filename string) error {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, service.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("unable to create %s: %w", filename, err)
	}

	if err := input.Datastore.ExportService(ctx, service.ExportServiceInput{
		Database:    database,
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		Writer:      file,
	}); err != nil {
		file.Close() //nolint:errcheck
		return fmt.Errorf("unable to export %s: %w", dumpName(input.ServiceName, database), err)
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("unable to write %s: %w", filename, err)
	}

	return nil
}

// dumpName is how a dump of one database is named in a message
func dumpName(serviceName string, database string) string {
	if database == "" {
		return serviceName
	}

	return fmt.Sprintf("database %s of %s", database, serviceName)
}

// writeServiceBackupManifest writes the ServiceBackup record, readable only by
// its owner since it holds the passwords of the users made in the service
func writeServiceBackupManifest(filename string, record ServiceBackup) error {
	contents, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("unable to encode the backup of %s: %w", record.Name, err)
	}

	if err := os.WriteFile(filename, append(contents, '\n'), service.PrivateFileMode); err != nil {
		return fmt.Errorf("unable to write %s: %w", filename, err)
	}

	return nil
}

// readServiceBackupManifest reads the ServiceBackup record an export wrote
func readServiceBackupManifest(filename string) (ServiceBackup, error) {
	contents, err := os.ReadFile(filename)
	if err != nil {
		return ServiceBackup{}, fmt.Errorf("unable to read %s: %w", filename, err)
	}

	record := ServiceBackup{}
	if err := json.Unmarshal(contents, &record); err != nil {
		return ServiceBackup{}, fmt.Errorf("unable to read %s: %w", filename, err)
	}

	return record, nil
}

// recipePlay is one play of a docket-style recipe
type recipePlay struct {
	Name  string       `yaml:"name"`
	Tasks []recipeTask `yaml:"tasks"`
}

// recipeTask is one task of a recipePlay, which sets exactly one of its fields
type recipeTask struct {
	ServiceCreate *recipeService `yaml:"dokku_service_create,omitempty"`
	ServiceLink   *recipeLink    `yaml:"dokku_service_link,omitempty"`
}

// recipeService names a service in a recipe task
type recipeService struct {
	Type    string `yaml:"type"`
	Service string `yaml:"service"`
}

// recipeLink names a service and the app it is linked to in a recipe task
type recipeLink struct {
	Type    string `yaml:"type"`
	Service string `yaml:"service"`
	App     string `yaml:"app"`
}

// ServiceRecipe renders the docket-style recipe slice of a service: a play that
// creates it, and links it to each app it is linked to
func ServiceRecipe(serviceType string, serviceName string, linkedApps []string) ([]byte, error) {
	tasks := []recipeTask{{ServiceCreate: &recipeService{Type: serviceType, Service: serviceName}}}
	for _, appName := range linkedApps {
		tasks = append(tasks, recipeTask{ServiceLink: &recipeLink{Type: serviceType, Service: serviceName, App: appName}})
	}

	plays := []recipePlay{{
		Name:  fmt.Sprintf("%s service %s", serviceType, serviceName),
		Tasks: tasks,
	}}

	buffer := bytes.Buffer{}
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(plays); err != nil {
		return nil, fmt.Errorf("unable to render the recipe of %s: %w", serviceName, err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("unable to render the recipe of %s: %w", serviceName, err)
	}

	return buffer.Bytes(), nil
}

// writeServiceRecipe writes the recipe slice of a service as <type>.yml in the
// scope's config directory
func writeServiceRecipe(directory string, record ServiceBackup, linkedApps []string) error {
	contents, err := ServiceRecipe(record.Type, record.Name, linkedApps)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("unable to create %s: %w", directory, err)
	}

	filename := filepath.Join(directory, record.Type+".yml")
	if err := os.WriteFile(filename, contents, 0644); err != nil {
		return fmt.Errorf("unable to write %s: %w", filename, err)
	}

	return nil
}

// ImportServiceBackup makes a service again from what ExportServiceBackup wrote
// into its scope directory, and does nothing for a service of another
// datastore. A service that already exists is refused rather than replaced,
// unless the input is forced: it is then destroyed the way destroy does it,
// without the check for linked apps and without unlinking them, so that the
// apps keep the config they were given and the restored links file and
// secrets make it point at the restored service again.
//
// The service is created through CreateService on the definition, image,
// version, backend and settings it was backed up with, seeded with its
// secrets, links file, database name and config files so that its linked apps
// keep the urls they were given, and started. Its properties are then written
// back as they were, its users are made with their passwords, its own dump is
// loaded, and each database made in it is made and loaded in turn, the way
// clone carries a service's data across. A service that was exposed is exposed
// on the same ports again, the running containers of each linked app are
// attached to the networks of their links again, and dokku is told to write
// its crontab when the service has a scheduled backup; any of these failing is
// warned about, since the service and its data are back by then.
func ImportServiceBackup(ctx context.Context, input BackupServiceInput) error {
	if !input.handles() {
		return nil
	}

	if err := input.validate(); err != nil {
		return err
	}

	record, err := readServiceBackupManifest(filepath.Join(input.dataDir(), ServiceBackupManifest))
	if err != nil {
		return err
	}

	if record.Name != input.ServiceName || record.Type != input.ServiceType {
		return fmt.Errorf("the backup in %s is of %s service %s, not %s service %s", input.ScopeDir, record.Type, record.Name, input.ServiceType, input.ServiceName)
	}

	datastore, err := restoreDatastore(input.Datastore, record)
	if err != nil {
		return err
	}
	input.Datastore = datastore

	if service.Exists(ctx, datastore, input.ServiceName) {
		if err := replaceExistingService(ctx, input); err != nil {
			return err
		}
	}

	input.Logger.Header2(fmt.Sprintf("Restoring %s service %s", input.ServiceType, input.ServiceName)) //nolint:errcheck

	settings := record.Settings
	if err := CreateService(ctx, CreateServiceInput{
		Backend:            backend.Select(backend.SelectInput{Recorded: record.Backend}),
		ConfigOptions:      settings.ConfigOptions,
		CustomEnv:          settings.CustomEnv,
		Datastore:          datastore,
		Definition:         datastore.DefinitionName(),
		Image:              record.Image,
		ImageVersion:       record.ImageVersion,
		InitialNetwork:     settings.InitialNetwork,
		Labels:             settings.Labels,
		LogDriver:          settings.LogDriver,
		LogOptions:         settings.LogOptions,
		Memory:             settings.Memory,
		Mounts:             settings.Mounts,
		PostCreateNetworks: settings.PostCreateNetworks,
		PostStartNetworks:  settings.PostStartNetworks,
		RestartPolicy:      settings.RestartPolicy,
		SeedRoot:           filepath.Join(input.dataDir(), serviceBackupSeedRoot),
		ServiceName:        input.ServiceName,
		ServiceMemory:      &settings.ServiceMemory,
		ShmSize:            settings.ShmSize,
		VolumeTargets:      settings.VolumeTargets,
		WaitTimeout:        settings.WaitTimeout,
		Logger:             input.Logger,
	}); err != nil {
		return err
	}

	if err := restoreProperties(datastore, input.ServiceName, record.Properties); err != nil {
		return err
	}

	if err := restoreServiceData(ctx, input, record); err != nil {
		return err
	}

	restoreExposure(ctx, input, record)
	restoreLinkNetworks(ctx, input)
	restoreCrontab(ctx, input)

	input.Logger.Header2("Done") //nolint:errcheck
	return nil
}

// replaceExistingService destroys a service a forced restore makes again, on
// the definition the service itself runs, and refuses one that is not forced
func replaceExistingService(ctx context.Context, input BackupServiceInput) error {
	if !input.Force {
		return fmt.Errorf("unable to restore %s service %s, which already exists; destroy it first or import the backup with --force to replace it", input.ServiceType, input.ServiceName)
	}

	existing, unresolved := input.Datastore.ForService(input.ServiceName)
	if unresolved != nil {
		input.Logger.Warn(WarnInput{Warning: unresolved.Error()})
	}

	input.Logger.Info(fmt.Sprintf("Destroying %s service %s to restore it from the backup", input.ServiceType, input.ServiceName))
	if err := destroyExistingService(ctx, DestroyServiceInput{
		Datastore:   existing,
		ServiceName: input.ServiceName,
	}); err != nil {
		return fmt.Errorf("unable to replace %s service %s: %w", input.ServiceType, input.ServiceName, err)
	}

	return nil
}

// restoreLinkNetworks attaches the running containers of every linked app that
// exists to the network of its link again. A forced restore destroyed the
// networks along with the old service, and an app that is not redeployed would
// otherwise not reach the restored one until its containers were made again.
// A link still on the container link an earlier version gave the app has no
// network, and a failure is warned about rather than undoing the restore.
func restoreLinkNetworks(ctx context.Context, input BackupServiceInput) {
	for _, appName := range service.LiveLinkedApps(ctx, linkedAppsInput(input.Datastore, input.ServiceName)) {
		if service.HasLegacyLink(input.Datastore, input.ServiceName, appName) {
			continue
		}

		if err := reattachLinkedApp(ctx, input, appName); err != nil {
			input.Logger.Warn(WarnInput{
				Warning: fmt.Sprintf("unable to attach app %s to %s again: %s", appName, input.ServiceName, err),
			})
		}
	}
}

// reattachLinkedApp makes the network of an app's link to a restored service
// and attaches each of the app's running containers to it
func reattachLinkedApp(ctx context.Context, input BackupServiceInput, appName string) error {
	if err := ensureLinkNetwork(ctx, input.Datastore, input.ServiceName, appName); err != nil {
		return err
	}

	containers, err := service.AppContainerIDs(ctx, appName)
	if err != nil {
		return err
	}

	for _, containerID := range containers {
		if err := connectAppContainer(ctx, input.Logger, input.Datastore, input.ServiceName, appName, containerID); err != nil {
			return err
		}
	}

	return nil
}

// restoreDatastore is the datastore a backup is restored onto: the definition
// it was backed up from, or the one its image resolves to for a backup that
// names none
func restoreDatastore(datastore *service.Datastore, record ServiceBackup) (*service.Datastore, error) {
	if record.Definition == "" {
		return datastore.ForImage(record.Image, record.ImageVersion), nil
	}

	return datastore.WithDefinitionNamed(record.Definition)
}

// restoreProperties writes every property a service was backed up with back
// as it was, over whatever creating the service wrote
func restoreProperties(datastore *service.Datastore, serviceName string, properties map[string]string) error {
	commandPrefix := datastore.Properties().CommandPrefix
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, name := range names {
		if err := common.PropertyWrite(commandPrefix, serviceName, name, properties[name]); err != nil {
			return fmt.Errorf("failed to restore the %s property: %w", name, err)
		}
	}

	return nil
}

// restoreServiceData makes the users and databases a service was backed up
// with and loads its dumps: the users first, so that each database is made
// with every user given access to it, the way clone makes them
func restoreServiceData(ctx context.Context, input BackupServiceInput, record ServiceBackup) error {
	users := make([]string, 0, len(record.Users))
	for user := range record.Users {
		users = append(users, user)
	}
	slices.Sort(users)

	for _, user := range users {
		input.Logger.Info(fmt.Sprintf("Creating user %s in %s", user, input.ServiceName))
		if err := input.Datastore.CreateUser(ctx, input.ServiceName, user, record.Users[user]); err != nil {
			return fmt.Errorf("unable to restore user %s: %w", user, err)
		}
	}

	imports := record.Dumped && input.Datastore.Definition.Implements("import")
	if record.Dumped && !imports {
		input.Logger.Warn(WarnInput{
			Warning: fmt.Sprintf("the %s datastore cannot import data, so %s is restored without it", input.Datastore.Definition.Dokku.Plugin, input.ServiceName),
		})
	}

	dumps := filepath.Join(input.dataDir(), serviceBackupDumps)
	if imports {
		if err := importServiceDump(ctx, input, "", filepath.Join(dumps, serviceBackupOwnDump)); err != nil {
			return err
		}
	}

	if !input.Datastore.Definition.Dokku.Databases.Makes() {
		return nil
	}

	for _, database := range record.Databases {
		input.Logger.Info(fmt.Sprintf("Creating database %s in %s", database, input.ServiceName))
		if err := input.Datastore.CreateDatabase(ctx, input.ServiceName, database); err != nil {
			return fmt.Errorf("unable to restore database %s: %w", database, err)
		}

		if imports {
			if err := importServiceDump(ctx, input, database, filepath.Join(dumps, serviceBackupDatabaseDumps, database)); err != nil {
				return err
			}
		}
	}

	return nil
}

// importServiceDump loads the dump of one database, the service's own when it
// is empty, from a file
func importServiceDump(ctx context.Context, input BackupServiceInput, database string, filename string) error {
	input.Logger.Info(fmt.Sprintf("Importing data into %s", dumpName(input.ServiceName, database)))

	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("unable to read %s: %w", filename, err)
	}
	defer file.Close() //nolint:errcheck

	if err := input.Datastore.ImportService(ctx, service.ImportServiceInput{
		Database:    database,
		Datastore:   input.Datastore,
		Reader:      file,
		ServiceName: input.ServiceName,
	}); err != nil {
		return fmt.Errorf("unable to import into %s: %w", dumpName(input.ServiceName, database), err)
	}

	return nil
}

// restoreExposure exposes a restored service on the ports it was exposed on,
// and warns when it cannot be, since a port taken on this host leaves the
// service as restored as it can be
func restoreExposure(ctx context.Context, input BackupServiceInput, record ServiceBackup) {
	if len(record.Ports) == 0 {
		return
	}

	if err := ExposeService(ctx, ExposeServiceInput{
		Datastore:   input.Datastore,
		Force:       true,
		Logger:      input.Logger,
		Ports:       record.Ports,
		ServiceName: input.ServiceName,
	}); err != nil {
		input.Logger.Warn(WarnInput{
			Warning: fmt.Sprintf("unable to expose %s on %s again: %s", input.ServiceName, strings.Join(record.Ports, " "), err),
		})
	}
}

// restoreCrontab has dokku write its crontab again when a restored service has
// a scheduled backup, which is only read from there
func restoreCrontab(ctx context.Context, input BackupServiceInput) {
	schedules, err := ReadBackupSchedules(input.Datastore, input.ServiceName)
	if err != nil {
		input.Logger.Warn(WarnInput{Warning: err.Error()})
		return
	}

	if len(schedules) == 0 {
		return
	}

	if err := RegenerateCrontab(ctx); err != nil {
		input.Logger.Warn(WarnInput{Warning: err.Error()})
	}
}

// CopySeedRoot copies every regular file under a seed directory into a service
// root, keeping its layout and mode, owned by the dokku user. A folder is made
// with the mode the service folders are, and symlinks are skipped rather than
// followed.
func CopySeedRoot(source string, serviceRoot string) error {
	username, groupName := hostenv.SystemUser(), hostenv.SystemGroup()

	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("unable to read %s: %w", path, err)
		}

		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(serviceRoot, relative)

		if entry.IsDir() {
			return CreateServiceFolders([]string{target}, username, groupName)
		}

		if !entry.Type().IsRegular() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("unable to read %s: %w", path, err)
		}

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("unable to read %s: %w", path, err)
		}
		defer file.Close() //nolint:errcheck

		contents, err := io.ReadAll(file)
		if err != nil {
			return fmt.Errorf("unable to read %s: %w", path, err)
		}

		if err := common.WriteBytesToFile(common.WriteBytesToFileInput{
			Bytes:     contents,
			Filename:  target,
			GroupName: groupName,
			Mode:      info.Mode().Perm(),
			Username:  username,
		}); err != nil {
			return fmt.Errorf("unable to write %s: %w", target, err)
		}

		return nil
	})
}
