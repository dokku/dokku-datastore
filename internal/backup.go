package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/dokku/dokku-datastore/internal/cron"
	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// backup credential and encryption file names, kept as the bash datastore
// plugins wrote them so an existing service keeps working
const (
	accessKeyIDFile      = "AWS_ACCESS_KEY_ID"
	secretAccessKeyFile  = "AWS_SECRET_ACCESS_KEY"
	defaultRegionFile    = "AWS_DEFAULT_REGION"
	signatureVersionFile = "AWS_SIGNATURE_VERSION"
	endpointURLFile      = "ENDPOINT_URL"
	encryptionKeyFile    = "ENCRYPTION_KEY"
	publicKeyIDFile      = "ENCRYPT_WITH_PUBLIC_KEY_ID"
)

// keyserverEnv is what the backup image reads to decide where to fetch a public
// key from, and KeyserverProperty is the service property that sets it. It is a
// property rather than a file beside the other settings because it is set the
// way every other property is, through the set command.
//
// The image defaults to keyserver.ubuntu.com when it is not told otherwise, so
// it is passed only when a service has one.
const (
	keyserverEnv = "KEYSERVER"

	// KeyserverProperty is named in SettableProperties, which is what makes it
	// settable and what generates the list of valid keys
	KeyserverProperty = "backup-keyserver"
)

// writeBackupFile writes one of the backup settings files for a service
func writeBackupFile(folder string, name string, contents string) error {
	if err := os.MkdirAll(folder, ServiceFolderMode); err != nil {
		return fmt.Errorf("unable to create %s: %w", folder, err)
	}

	if err := common.SetPermissions(common.SetPermissionInput{
		Filename:  folder,
		GroupName: hostenv.SystemGroup(),
		Mode:      ServiceFolderMode,
		Username:  hostenv.SystemUser(),
	}); err != nil {
		return fmt.Errorf("unable to set permissions on %s: %w", folder, err)
	}

	filename := filepath.Join(folder, name)
	if err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   contents,
		Filename:  filename,
		GroupName: hostenv.SystemGroup(),
		Mode:      0640,
		Username:  hostenv.SystemUser(),
	}); err != nil {
		return fmt.Errorf("unable to write %s: %w", filename, err)
	}

	return nil
}

// BackupAuthInput is the input for the BackupAuth function
type BackupAuthInput struct {
	AccessKeyID      string
	Datastore        *service.Datastore
	DefaultRegion    string
	EndpointURL      string
	SecretAccessKey  string
	ServiceName      string
	SignatureVersion string
}

// BackupAuth stores the credentials backups are shipped with
func BackupAuth(ctx context.Context, input BackupAuthInput) error {
	folder := service.Folders(input.Datastore, input.ServiceName).Backup

	entries := map[string]string{
		accessKeyIDFile:     input.AccessKeyID,
		secretAccessKeyFile: input.SecretAccessKey,
	}
	for name, value := range map[string]string{
		defaultRegionFile:    input.DefaultRegion,
		signatureVersionFile: input.SignatureVersion,
		endpointURLFile:      input.EndpointURL,
	} {
		if value != "" {
			entries[name] = value
		}
	}

	for name, value := range entries {
		if err := writeBackupFile(folder, name, value); err != nil {
			return err
		}
	}

	return nil
}

// BackupDeauth removes the stored backup credentials for a service
func BackupDeauth(ctx context.Context, s *service.Datastore, serviceName string) error {
	folder := service.Folders(s, serviceName).Backup
	if err := os.RemoveAll(folder); err != nil {
		return fmt.Errorf("unable to remove %s: %w", folder, err)
	}

	return nil
}

// SetBackupEncryption stores a passphrase future backups are encrypted with
func SetBackupEncryption(ctx context.Context, s *service.Datastore, serviceName string, passphrase string) error {
	return writeBackupFile(service.Folders(s, serviceName).BackupEncryption, encryptionKeyFile, passphrase)
}

// SetBackupPublicKeyEncryption stores the gpg public key future backups are encrypted with
func SetBackupPublicKeyEncryption(ctx context.Context, s *service.Datastore, serviceName string, publicKeyID string) error {
	return writeBackupFile(service.Folders(s, serviceName).BackupEncryption, publicKeyIDFile, publicKeyID)
}

// UnsetBackupEncryption removes the stored backup passphrase
func UnsetBackupEncryption(ctx context.Context, s *service.Datastore, serviceName string) error {
	return removeBackupSetting(s, serviceName, encryptionKeyFile)
}

// UnsetBackupPublicKeyEncryption removes the stored backup public key
func UnsetBackupPublicKeyEncryption(ctx context.Context, s *service.Datastore, serviceName string) error {
	return removeBackupSetting(s, serviceName, publicKeyIDFile)
}

// removeBackupSetting deletes one of the backup encryption files
func removeBackupSetting(s *service.Datastore, serviceName string, name string) error {
	filename := filepath.Join(service.Folders(s, serviceName).BackupEncryption, name)
	if err := os.Remove(filename); err != nil {
		return fmt.Errorf("unable to remove %s: %w", filename, err)
	}

	return nil
}

// BackupScheduleCat returns the contents of the backup cron file for a service
func BackupScheduleCat(s *service.Datastore, serviceName string) (string, error) {
	cronFile := service.Files(s, serviceName).CronFile
	if !common.FileExists(cronFile) {
		return "", fmt.Errorf("There is no scheduled backup for %s.", serviceName) //nolint:staticcheck // matches the bash datastore plugins
	}

	contents, err := os.ReadFile(cronFile)
	if err != nil {
		return "", fmt.Errorf("unable to read %s: %w", cronFile, err)
	}

	return string(contents), nil
}

// CronEntry builds the crontab line that runs a scheduled backup
func CronEntry(dokkuBin string, commandPrefix string, input ScheduleBackupInput) string {
	entry := fmt.Sprintf("%s dokku %s %s:backup %s %s", input.Schedule, dokkuBin, commandPrefix, input.ServiceName, input.BucketName)
	if input.UseIAM {
		entry = fmt.Sprintf("%s --use-iam", entry)
	}

	return entry
}

// ScheduleBackupInput is the input for the ScheduleBackup function
type ScheduleBackupInput struct {
	BucketName  string
	Datastore   *service.Datastore
	Schedule    string
	ServiceName string
	UseIAM      bool
}

// ScheduleBackup writes the cron entry that backs a service up on a schedule
func ScheduleBackup(ctx context.Context, input ScheduleBackupInput) error {
	commandPrefix := input.Datastore.Properties().CommandPrefix
	// staged inside the service, so that an interrupted schedule cannot leave a
	// file the service listing mistakes for a service
	tmpCronFile := StagedCronFile(input.Datastore, input.ServiceName)

	dokkuBin, err := exec.LookPath("dokku")
	if err != nil {
		return fmt.Errorf("unable to find the dokku binary: %w", err)
	}

	entry := CronEntry(dokkuBin, commandPrefix, input)

	// cron ignores a final line that is not newline terminated
	if err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   entry + "\n",
		Filename:  tmpCronFile,
		GroupName: hostenv.SystemGroup(),
		Mode:      0644,
		Username:  hostenv.SystemUser(),
	}); err != nil {
		return fmt.Errorf("unable to write %s: %w", tmpCronFile, err)
	}

	// the cron directory belongs to root, so the staged file is moved into place
	// by the helper the plugin installs and the dokku group is granted
	return cron.Install(ctx, commandPrefix, input.ServiceName)
}

// BackupArgsInput is the input for BackupArgs. Every value is already resolved,
// so that building the command needs neither a filesystem nor a daemon.
type BackupArgsInput struct {
	// AccessKeyID and SecretAccessKey are the credentials. They are empty when
	// the backup runs against an instance role instead.
	AccessKeyID     string
	SecretAccessKey string

	// BucketName is the bucket the dump is shipped to
	BucketName string

	// BackupName is what the object is named after, ahead of the timestamp the
	// image appends
	BackupName string

	// BackupDir is the host directory holding the dump, mounted at /backup
	BackupDir string

	// Settings are the values read from the backup settings files, keyed by the
	// environment variable each file is named after
	Settings map[string]string

	// Keyserver is where the image fetches a public key from, passed only when
	// a service sets one so that the image otherwise keeps its own default
	Keyserver string

	// Image is the image the backup runs in
	Image string
}

// BackupArgs builds the argv for the container that ships a dump to s3.
func BackupArgs(input BackupArgsInput) []string {
	args := []string{"container", "run", "--rm"}

	if input.AccessKeyID != "" {
		args = append(args, "-e", fmt.Sprintf("%s=%s", accessKeyIDFile, input.AccessKeyID))
	}

	if input.SecretAccessKey != "" {
		args = append(args, "-e", fmt.Sprintf("%s=%s", secretAccessKeyFile, input.SecretAccessKey))
	}

	args = append(args,
		"-e", fmt.Sprintf("BUCKET_NAME=%s", input.BucketName),
		"-e", fmt.Sprintf("BACKUP_NAME=%s", input.BackupName),
		"-v", fmt.Sprintf("%s:/backup", input.BackupDir),
	)

	// sorted, because a map would otherwise emit a different command each run
	names := make([]string, 0, len(input.Settings))
	for name := range input.Settings {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		args = append(args, "-e", fmt.Sprintf("%s=%s", name, input.Settings[name]))
	}

	if input.Keyserver != "" {
		args = append(args, "-e", fmt.Sprintf("%s=%s", keyserverEnv, input.Keyserver))
	}

	return append(args, input.Image)
}

// BackupInput is the input for the Backup function
type BackupInput struct {
	BucketName  string
	Datastore   *service.Datastore
	ServiceName string
	UseIAM      bool
}

// Backup exports a service and ships the result to an s3 bucket
func Backup(ctx context.Context, input BackupInput) error {
	serviceFolders := service.Folders(input.Datastore, input.ServiceName)
	commandPrefix := input.Datastore.Properties().CommandPrefix

	arguments := BackupArgsInput{
		BucketName: input.BucketName,
		BackupName: fmt.Sprintf("%s-%s", commandPrefix, input.ServiceName),
		Keyserver:  common.PropertyGet(commandPrefix, input.ServiceName, KeyserverProperty),
		Image:      hostenv.S3BackupImage,
		Settings:   map[string]string{},
	}

	if !input.UseIAM {
		accessKeyID := filepath.Join(serviceFolders.Backup, accessKeyIDFile)
		if !common.FileExists(accessKeyID) {
			return errors.New("Missing AWS_ACCESS_KEY_ID file") //nolint:staticcheck // matches the bash datastore plugins
		}

		secretAccessKey := filepath.Join(serviceFolders.Backup, secretAccessKeyFile)
		if !common.FileExists(secretAccessKey) {
			return errors.New("Missing AWS_SECRET_ACCESS_KEY file") //nolint:staticcheck // matches the bash datastore plugins
		}

		arguments.AccessKeyID = common.ReadFirstLine(accessKeyID)
		arguments.SecretAccessKey = common.ReadFirstLine(secretAccessKey)
	}

	containerID := service.ContainerID(input.Datastore, input.ServiceName)
	if !service.ContainerExists(ctx, containerID) {
		return errors.New("Service container does not exist") //nolint:staticcheck // matches the bash datastore plugins
	}

	if !common.ContainerIsRunning(containerID) {
		return errors.New("Service container is not running") //nolint:staticcheck // matches the bash datastore plugins
	}

	backupDir, err := os.MkdirTemp("", "dokku-datastore-backup")
	if err != nil {
		return fmt.Errorf("unable to create a temporary directory: %w", err)
	}
	defer os.RemoveAll(backupDir)

	exportFile := filepath.Join(backupDir, "export")
	handle, err := os.Create(exportFile)
	if err != nil {
		return fmt.Errorf("unable to create %s: %w", exportFile, err)
	}

	if err := input.Datastore.ExportService(ctx, service.ExportServiceInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		Writer:      handle,
	}); err != nil {
		handle.Close()
		return err
	}

	if err := handle.Close(); err != nil {
		return fmt.Errorf("unable to close %s: %w", exportFile, err)
	}

	arguments.BackupDir = backupDir

	for folder, names := range map[string][]string{
		serviceFolders.Backup:           {defaultRegionFile, signatureVersionFile, endpointURLFile},
		serviceFolders.BackupEncryption: {encryptionKeyFile, publicKeyIDFile},
	} {
		for _, name := range names {
			filename := filepath.Join(folder, name)
			if common.FileExists(filename) {
				arguments.Settings[name] = common.ReadFirstLine(filename)
			}
		}
	}

	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command:      common.DockerBin(),
		Args:         BackupArgs(arguments),
		StreamStderr: true,
		StreamStdout: true,
	}); err != nil {
		return fmt.Errorf("unable to run the backup: %w", err)
	}

	return nil
}
