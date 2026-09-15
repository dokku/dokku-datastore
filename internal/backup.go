package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dokku/dokku-datastore/internal/cron"
	"github.com/dokku/dokku-datastore/internal/datastores"
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

// writeBackupFile writes one of the backup settings files for a service
func writeBackupFile(folder string, name string, contents string) error {
	if err := os.MkdirAll(folder, ServiceFolderMode); err != nil {
		return fmt.Errorf("unable to create %s: %w", folder, err)
	}

	if err := common.SetPermissions(common.SetPermissionInput{
		Filename:  folder,
		GroupName: datastores.SystemGroup(),
		Mode:      ServiceFolderMode,
		Username:  datastores.SystemUser(),
	}); err != nil {
		return fmt.Errorf("unable to set permissions on %s: %w", folder, err)
	}

	filename := filepath.Join(folder, name)
	if err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   contents,
		Filename:  filename,
		GroupName: datastores.SystemGroup(),
		Mode:      0640,
		Username:  datastores.SystemUser(),
	}); err != nil {
		return fmt.Errorf("unable to write %s: %w", filename, err)
	}

	return nil
}

// BackupAuthInput is the input for the BackupAuth function
type BackupAuthInput struct {
	AccessKeyID      string
	Datastore        datastores.Datastore
	DefaultRegion    string
	EndpointURL      string
	SecretAccessKey  string
	ServiceName      string
	SignatureVersion string
}

// BackupAuth stores the credentials backups are shipped with
func BackupAuth(ctx context.Context, input BackupAuthInput) error {
	folder := datastores.Folders(input.Datastore, input.ServiceName).Backup

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
func BackupDeauth(ctx context.Context, s datastores.Datastore, serviceName string) error {
	folder := datastores.Folders(s, serviceName).Backup
	if err := os.RemoveAll(folder); err != nil {
		return fmt.Errorf("unable to remove %s: %w", folder, err)
	}

	return nil
}

// SetBackupEncryption stores a passphrase future backups are encrypted with
func SetBackupEncryption(ctx context.Context, s datastores.Datastore, serviceName string, passphrase string) error {
	return writeBackupFile(datastores.Folders(s, serviceName).BackupEncryption, encryptionKeyFile, passphrase)
}

// SetBackupPublicKeyEncryption stores the gpg public key future backups are encrypted with
func SetBackupPublicKeyEncryption(ctx context.Context, s datastores.Datastore, serviceName string, publicKeyID string) error {
	return writeBackupFile(datastores.Folders(s, serviceName).BackupEncryption, publicKeyIDFile, publicKeyID)
}

// UnsetBackupEncryption removes the stored backup passphrase
func UnsetBackupEncryption(ctx context.Context, s datastores.Datastore, serviceName string) error {
	return removeBackupSetting(s, serviceName, encryptionKeyFile)
}

// UnsetBackupPublicKeyEncryption removes the stored backup public key
func UnsetBackupPublicKeyEncryption(ctx context.Context, s datastores.Datastore, serviceName string) error {
	return removeBackupSetting(s, serviceName, publicKeyIDFile)
}

// removeBackupSetting deletes one of the backup encryption files
func removeBackupSetting(s datastores.Datastore, serviceName string, name string) error {
	filename := filepath.Join(datastores.Folders(s, serviceName).BackupEncryption, name)
	if err := os.Remove(filename); err != nil {
		return fmt.Errorf("unable to remove %s: %w", filename, err)
	}

	return nil
}

// BackupScheduleCat returns the contents of the backup cron file for a service
func BackupScheduleCat(s datastores.Datastore, serviceName string) (string, error) {
	cronFile := datastores.Files(s, serviceName).CronFile
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
	Datastore   datastores.Datastore
	Schedule    string
	ServiceName string
	UseIAM      bool
}

// ScheduleBackup writes the cron entry that backs a service up on a schedule
func ScheduleBackup(ctx context.Context, input ScheduleBackupInput) error {
	commandPrefix := input.Datastore.Properties().CommandPrefix
	// the helper only moves this exact path, which is rooted at the plugin's own
	// data directory rather than the shared one
	tmpCronFile := StagedCronFile(input.Datastore)

	dokkuBin, err := exec.LookPath("dokku")
	if err != nil {
		return fmt.Errorf("unable to find the dokku binary: %w", err)
	}

	entry := CronEntry(dokkuBin, commandPrefix, input)

	// cron ignores a final line that is not newline terminated
	if err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   entry + "\n",
		Filename:  tmpCronFile,
		GroupName: datastores.SystemGroup(),
		Mode:      0644,
		Username:  datastores.SystemUser(),
	}); err != nil {
		return fmt.Errorf("unable to write %s: %w", tmpCronFile, err)
	}

	// the cron directory belongs to root, so the staged file is moved into place
	// by the helper the plugin installs and the dokku group is granted
	return cron.Install(ctx, commandPrefix, input.ServiceName)
}

// BackupInput is the input for the Backup function
type BackupInput struct {
	BucketName  string
	Datastore   datastores.Datastore
	ServiceName string
	UseIAM      bool
}

// Backup exports a service and ships the result to an s3 bucket
func Backup(ctx context.Context, input BackupInput) error {
	serviceFolders := datastores.Folders(input.Datastore, input.ServiceName)
	commandPrefix := input.Datastore.Properties().CommandPrefix

	dockerArgs := []string{"container", "run", "--rm"}

	if !input.UseIAM {
		accessKeyID := filepath.Join(serviceFolders.Backup, accessKeyIDFile)
		if !common.FileExists(accessKeyID) {
			return errors.New("Missing AWS_ACCESS_KEY_ID file") //nolint:staticcheck // matches the bash datastore plugins
		}

		secretAccessKey := filepath.Join(serviceFolders.Backup, secretAccessKeyFile)
		if !common.FileExists(secretAccessKey) {
			return errors.New("Missing AWS_SECRET_ACCESS_KEY file") //nolint:staticcheck // matches the bash datastore plugins
		}

		dockerArgs = append(dockerArgs,
			"-e", fmt.Sprintf("AWS_ACCESS_KEY_ID=%s", common.ReadFirstLine(accessKeyID)),
			"-e", fmt.Sprintf("AWS_SECRET_ACCESS_KEY=%s", common.ReadFirstLine(secretAccessKey)),
		)
	}

	containerID := datastores.ContainerID(input.Datastore, input.ServiceName)
	if !datastores.ContainerExists(ctx, containerID) {
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

	if err := input.Datastore.ExportService(ctx, datastores.ExportServiceInput{
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

	dockerArgs = append(dockerArgs,
		"-e", fmt.Sprintf("BUCKET_NAME=%s", input.BucketName),
		"-e", fmt.Sprintf("BACKUP_NAME=%s-%s", commandPrefix, input.ServiceName),
		"-v", fmt.Sprintf("%s:/backup", backupDir),
	)

	for folder, names := range map[string][]string{
		serviceFolders.Backup:           {defaultRegionFile, signatureVersionFile, endpointURLFile},
		serviceFolders.BackupEncryption: {encryptionKeyFile, publicKeyIDFile},
	} {
		for _, name := range names {
			filename := filepath.Join(folder, name)
			if common.FileExists(filename) {
				dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", name, common.ReadFirstLine(filename)))
			}
		}
	}

	dockerArgs = append(dockerArgs, datastores.PluginS3BackupImage)

	if _, err := datastores.CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command:      common.DockerBin(),
		Args:         dockerArgs,
		StreamStderr: true,
		StreamStdout: true,
	}); err != nil {
		return fmt.Errorf("unable to run the backup: %w", err)
	}

	return nil
}
