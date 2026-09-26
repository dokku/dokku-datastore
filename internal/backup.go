package internal

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
	cronparser "github.com/robfig/cron/v3"
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
// key from. The property that sets it is service.KeyserverProperty, which is
// named in SettableProperties - that is what makes it settable and what
// generates the list of valid keys.
//
// The image defaults to keyserver.ubuntu.com when it is not told otherwise, so
// it is passed only when a service has one.
const keyserverEnv = "KEYSERVER"

// backupSourceEnv tells the backup image where to read the backup from, and
// backupSourceStdin has it read a tar stream on stdin
const (
	backupSourceEnv   = "BACKUP_SOURCE"
	backupSourceStdin = "stdin"
)

// the entries of the archive a backup ships, laid out as the image laid them
// out when it archived a directory mounted at /backup, so that a backup made
// either way is restored the same way
const (
	backupArchiveDir    = "backup/"
	backupArchiveExport = "backup/export"
)

// backupArchive writes the tar stream the backup image reads from stdin: the
// backup directory and the export inside it
func backupArchive(w io.Writer, exportFile string) error {
	handle, err := os.Open(exportFile)
	if err != nil {
		return fmt.Errorf("unable to open %s: %w", exportFile, err)
	}
	defer handle.Close()

	stat, err := handle.Stat()
	if err != nil {
		return fmt.Errorf("unable to read %s: %w", exportFile, err)
	}

	archive := tar.NewWriter(w)
	if err := archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir,
		Name:     backupArchiveDir,
		Mode:     0700,
		ModTime:  stat.ModTime(),
	}); err != nil {
		return fmt.Errorf("unable to archive the backup: %w", err)
	}

	if err := archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     backupArchiveExport,
		Mode:     0600,
		Size:     stat.Size(),
		ModTime:  stat.ModTime(),
	}); err != nil {
		return fmt.Errorf("unable to archive the backup: %w", err)
	}

	if _, err := io.Copy(archive, handle); err != nil {
		return fmt.Errorf("unable to archive the backup: %w", err)
	}

	if err := archive.Close(); err != nil {
		return fmt.Errorf("unable to archive the backup: %w", err)
	}

	return nil
}

// BackupFolderMode is the mode of the folders holding a service's backup
// credentials and encryption settings. Nothing but the dokku user and group
// reads them, so nobody else is let in, not even to list what is there.
const BackupFolderMode = 0750

// writeBackupFile writes one of the backup settings files for a service
func writeBackupFile(folder string, name string, contents string) error {
	if err := os.MkdirAll(folder, BackupFolderMode); err != nil {
		return fmt.Errorf("unable to create %s: %w", folder, err)
	}

	if err := common.SetPermissions(common.SetPermissionInput{
		Filename:  folder,
		GroupName: hostenv.SystemGroup(),
		Mode:      BackupFolderMode,
		Username:  hostenv.SystemUser(),
	}); err != nil {
		return fmt.Errorf("unable to set permissions on %s: %w", folder, err)
	}

	// replaced rather than rewritten, because a file left by an older plugin may
	// still be readable by everyone and would hold the new secret until chmodded
	filename := filepath.Join(folder, name)
	if err := service.ReplaceFileAtomically(filename, contents, service.PrivateFileMode); err != nil {
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

// the properties a scheduled backup is recorded in, named after the info keys
// that report them
const (
	// BackupScheduleProperty is the cron schedule the backup runs on
	BackupScheduleProperty = "backup-schedule"

	// BackupBucketProperty is the bucket a scheduled backup is shipped to
	BackupBucketProperty = "backup-bucket"

	// BackupUseIAMProperty is set when a scheduled backup runs against an
	// instance role rather than against stored credentials
	BackupUseIAMProperty = "backup-use-iam"
)

// backupScheduleProperties are every property a scheduled backup writes
var backupScheduleProperties = []string{BackupScheduleProperty, BackupBucketProperty, BackupUseIAMProperty}

// cronScheduleParser reads a schedule the way the dokku cron plugin does, so a
// schedule accepted here is one dokku would accept for an app's own task
var cronScheduleParser = cronparser.NewParser(cronparser.Minute | cronparser.Hour | cronparser.Dom | cronparser.Month | cronparser.Dow | cronparser.Descriptor)

// bucketNamePattern is what a bucket may be made of. The bucket is written into
// a shell command in the dokku crontab, and into a line the cron-entries trigger
// separates with semicolons, so anything a shell or that line reads specially
// is refused rather than escaped.
var bucketNamePattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// ValidateBackupSchedule reports whether a schedule is one cron can run.
//
// A schedule cron cannot read used to be written anyway, and cron skipped it
// without a word: a service scheduled with "daily" rather than "@daily" was
// reported as scheduled and never backed up. It matters more now that the
// schedule is part of the dokku crontab, which is refused as a whole when any
// line in it is invalid.
func ValidateBackupSchedule(schedule string) error {
	if strings.TrimSpace(schedule) == "" {
		return errors.New("Please specify a schedule for the backup") //nolint:staticcheck // matches the bash datastore plugins
	}

	if strings.ContainsAny(schedule, ";\n\r") {
		return fmt.Errorf("invalid backup schedule %q: a schedule cannot contain a semicolon or a newline", schedule)
	}

	// understood by the parser, but not by the cron that runs the crontab
	if strings.HasPrefix(schedule, "@every") {
		return fmt.Errorf("invalid backup schedule %q: @every is not supported by cron", schedule)
	}

	if _, err := cronScheduleParser.Parse(schedule); err != nil {
		return fmt.Errorf("invalid backup schedule %q: %w", schedule, err)
	}

	return nil
}

// ValidateBucketName reports whether a bucket can be written into the command
// a scheduled backup runs
func ValidateBucketName(bucketName string) error {
	if bucketName == "" {
		return errors.New("Please specify an aws bucket for the backup") //nolint:staticcheck // matches the bash datastore plugins
	}

	if !bucketNamePattern.MatchString(bucketName) {
		return fmt.Errorf("invalid bucket name %q: only letters, numbers, dots, dashes, underscores and slashes are allowed", bucketName)
	}

	return nil
}

// BackupSchedule is the scheduled backup a service is recorded with
type BackupSchedule struct {
	// Schedule is the cron schedule the backup runs on
	Schedule string

	// BucketName is the bucket the dump is shipped to
	BucketName string

	// UseIAM reports whether the backup runs against an instance role rather
	// than against stored credentials
	UseIAM bool
}

// Validate reports whether a schedule can be written into the dokku crontab
func (b BackupSchedule) Validate() error {
	if err := ValidateBackupSchedule(b.Schedule); err != nil {
		return err
	}

	return ValidateBucketName(b.BucketName)
}

// ReadBackupSchedule reads the scheduled backup a service is recorded with, and
// reports whether it has one
func ReadBackupSchedule(s *service.Datastore, serviceName string) (BackupSchedule, bool) {
	commandPrefix := s.Properties().CommandPrefix
	schedule := BackupSchedule{
		Schedule:   common.PropertyGet(commandPrefix, serviceName, BackupScheduleProperty),
		BucketName: common.PropertyGet(commandPrefix, serviceName, BackupBucketProperty),
		UseIAM:     common.PropertyGet(commandPrefix, serviceName, BackupUseIAMProperty) == "true",
	}

	if schedule.Schedule == "" {
		return BackupSchedule{}, false
	}

	return schedule, true
}

// BackupLogFile is where the output of a datastore's scheduled backups goes
func BackupLogFile(commandPrefix string) string {
	return fmt.Sprintf("/var/log/dokku/%s.log", commandPrefix)
}

// backupCommand is the command a scheduled backup runs. It is run from the dokku
// crontab, whose PATH includes wherever dokku is installed.
func backupCommand(commandPrefix string, serviceName string, schedule BackupSchedule) string {
	command := fmt.Sprintf("dokku %s:backup %s %s", commandPrefix, serviceName, schedule.BucketName)
	if schedule.UseIAM {
		command = fmt.Sprintf("%s --use-iam", command)
	}

	return command
}

// CronEntry builds the line the cron-entries trigger prints for a scheduled
// backup: the schedule, the command and the log file, separated by semicolons
func CronEntry(commandPrefix string, serviceName string, schedule BackupSchedule) string {
	return strings.Join([]string{
		schedule.Schedule,
		backupCommand(commandPrefix, serviceName, schedule),
		BackupLogFile(commandPrefix),
	}, ";")
}

// CrontabLine builds the line dokku writes into its crontab for a scheduled
// backup, the same way it writes any task handed to it by cron-entries
func CrontabLine(commandPrefix string, serviceName string, schedule BackupSchedule) string {
	return fmt.Sprintf("%s %s &>> %s", schedule.Schedule, backupCommand(commandPrefix, serviceName, schedule), BackupLogFile(commandPrefix))
}

// BackupScheduleCat returns the crontab line a service's scheduled backup runs
// from
func BackupScheduleCat(s *service.Datastore, serviceName string) (string, error) {
	schedule, ok := ReadBackupSchedule(s, serviceName)
	if !ok {
		return "", fmt.Errorf("There is no scheduled backup for %s.", serviceName) //nolint:staticcheck // matches the bash datastore plugins
	}

	return CrontabLine(s.Properties().CommandPrefix, serviceName, schedule) + "\n", nil
}

// ParseCronEntry reads back the schedule a legacy cron file was written with,
// and reports whether the line was one an earlier version of the plugin wrote.
// Those files are only read to migrate them onto the cron-entries trigger.
//
// The fields are found by locating the backup command rather than by counting
// from the start of the line, because the schedule is not a fixed width: a
// service scheduled with @daily has one field where a service scheduled with
// five stars has five.
func ParseCronEntry(commandPrefix string, entry string) (BackupSchedule, bool) {
	fields := strings.Fields(entry)
	command := fmt.Sprintf("%s:backup", commandPrefix)

	index := slices.Index(fields, command)
	// the command is preceded by the schedule, the user cron runs it as, and
	// the dokku binary, and followed by the service and the bucket
	if index < 3 || index+2 >= len(fields) {
		return BackupSchedule{}, false
	}

	schedule := BackupSchedule{
		Schedule:   strings.Join(fields[:index-2], " "),
		BucketName: fields[index+2],
		UseIAM:     slices.Contains(fields[index+3:], "--use-iam"),
	}

	return schedule, true
}

// writeBackupSchedule records a scheduled backup for a service
func writeBackupSchedule(s *service.Datastore, serviceName string, schedule BackupSchedule) error {
	commandPrefix := s.Properties().CommandPrefix
	if err := common.PropertyWrite(commandPrefix, serviceName, BackupScheduleProperty, schedule.Schedule); err != nil {
		return fmt.Errorf("unable to record the backup schedule: %w", err)
	}

	if err := common.PropertyWrite(commandPrefix, serviceName, BackupBucketProperty, schedule.BucketName); err != nil {
		return fmt.Errorf("unable to record the backup bucket: %w", err)
	}

	if !schedule.UseIAM {
		if err := common.PropertyDelete(commandPrefix, serviceName, BackupUseIAMProperty); err != nil {
			return fmt.Errorf("unable to record the backup credentials: %w", err)
		}

		return nil
	}

	if err := common.PropertyWrite(commandPrefix, serviceName, BackupUseIAMProperty, "true"); err != nil {
		return fmt.Errorf("unable to record the backup credentials: %w", err)
	}

	return nil
}

// regenerateCrontab has dokku write its crontab again, which is where the
// cron-entries trigger is read. Dokku 0.36.0 and later do that on
// scheduler-cron-write, and earlier versions on cron-write. Each is a trigger
// nothing acts on in the versions that use the other, so both are fired.
func regenerateCrontab(ctx context.Context) error {
	for _, trigger := range []string{"scheduler-cron-write", "cron-write"} {
		if _, err := execx.PlugnTrigger(ctx, common.PlugnTriggerInput{
			Trigger:      trigger,
			StreamStderr: true,
		}); err != nil {
			return fmt.Errorf("failed to call the %s trigger: %w", trigger, err)
		}
	}

	return nil
}

// ScheduleBackupInput is the input for the ScheduleBackup function
type ScheduleBackupInput struct {
	BucketName  string
	Datastore   *service.Datastore
	Schedule    string
	ServiceName string
	UseIAM      bool
}

// ScheduleBackup records a scheduled backup for a service and has dokku write it
// into its crontab
func ScheduleBackup(ctx context.Context, input ScheduleBackupInput) error {
	schedule := BackupSchedule{
		Schedule:   input.Schedule,
		BucketName: input.BucketName,
		UseIAM:     input.UseIAM,
	}
	if err := schedule.Validate(); err != nil {
		return err
	}

	if err := writeBackupSchedule(input.Datastore, input.ServiceName, schedule); err != nil {
		return err
	}

	return regenerateCrontab(ctx)
}

// UnscheduleBackupInput is the input for the UnscheduleBackup function
type UnscheduleBackupInput struct {
	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

	// ServiceName is the service to stop backing up
	ServiceName string
}

// UnscheduleBackup removes a service's scheduled backup and has dokku write its
// crontab without it. A service with no scheduled backup is left alone, so that
// destroying one does not rewrite the crontab for nothing.
func UnscheduleBackup(ctx context.Context, input UnscheduleBackupInput) error {
	if _, ok := ReadBackupSchedule(input.Datastore, input.ServiceName); !ok {
		return nil
	}

	commandPrefix := input.Datastore.Properties().CommandPrefix
	for _, property := range backupScheduleProperties {
		if err := common.PropertyDelete(commandPrefix, input.ServiceName, property); err != nil {
			return fmt.Errorf("unable to remove the %s property: %w", property, err)
		}
	}

	return regenerateCrontab(ctx)
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

	// Settings are the values read from the backup settings files, keyed by the
	// environment variable each file is named after
	Settings map[string]string

	// Keyserver is where the image fetches a public key from, passed only when
	// a service sets one so that the image otherwise keeps its own default
	Keyserver string

	// Image is the image the backup runs in
	Image string
}

// BackupArgs builds the argv for the container that ships a dump to s3, and the
// environment docker has to be run with for it.
//
// The dump is handed over on stdin rather than mounted. A mounted directory is
// resolved by dockerd, which on a dokku installed in docker is a different
// filesystem than the one this process wrote the dump to: docker mounted an
// empty directory in its place, and an empty archive was shipped as a success.
//
// The argv names each variable without a value, which docker fills in from its
// own environment. The values include the credentials and the encryption
// passphrase, and an argv is readable by every user on the host, where the
// environment of a process is readable only by its owner.
func BackupArgs(input BackupArgsInput) ([]string, map[string]string) {
	args := []string{"container", "run", "--rm", "-i"}
	env := map[string]string{}

	setenv := func(name string, value string) {
		args = append(args, "-e", name)
		env[name] = value
	}

	if input.AccessKeyID != "" {
		setenv(accessKeyIDFile, input.AccessKeyID)
	}

	if input.SecretAccessKey != "" {
		setenv(secretAccessKeyFile, input.SecretAccessKey)
	}

	setenv("BUCKET_NAME", input.BucketName)
	setenv("BACKUP_NAME", input.BackupName)
	setenv(backupSourceEnv, backupSourceStdin)

	// sorted, because a map would otherwise emit a different command each run
	names := make([]string, 0, len(input.Settings))
	for name := range input.Settings {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		setenv(name, input.Settings[name])
	}

	if input.Keyserver != "" {
		setenv(keyserverEnv, input.Keyserver)
	}

	return append(args, input.Image), env
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
		Keyserver:  service.Keyserver(input.Datastore, input.ServiceName),
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

	// before the dump rather than after it: the tool that ships the dump is
	// pinned and fetched at install, which is no help on a host that has been
	// pruned since, and exporting a database only to find there is nothing to
	// ship it with is a wasted read of the whole service
	if err := service.EnsureTaggedImage(ctx, service.EnsureTaggedImageInput{
		Action:      "backup",
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		TaggedImage: hostenv.S3BackupImage,
	}); err != nil {
		return err
	}

	// a file rather than a pipe into the backup container, because the archive
	// names the size of the export ahead of it. It is only ever read by this
	// process, so it can live wherever this process keeps temporary files.
	handle, err := os.CreateTemp("", "dokku-datastore-backup-")
	if err != nil {
		return fmt.Errorf("unable to create a temporary file: %w", err)
	}
	exportFile := handle.Name()
	defer os.Remove(exportFile)

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

	reader, writer := io.Pipe()
	archived := make(chan error, 1)
	go func() {
		err := backupArchive(writer, exportFile)
		writer.CloseWithError(err)
		archived <- err
	}()

	args, env := BackupArgs(arguments)
	_, runErr := execx.Run(ctx, common.ExecCommandInput{
		Command:      common.DockerBin(),
		Args:         args,
		Env:          env,
		Stdin:        reader,
		StreamStderr: true,
		StreamStdout: true,
	})

	// closed so that an archive the container stopped reading early does not
	// hold the writer open forever
	reader.Close()

	// the archive failing is why the container failed, when it did, since the
	// image refuses a stream that ends early. A pipe closed under the archive
	// is the other way around: the container stopped reading first.
	archiveErr := <-archived
	if runErr != nil {
		if archiveErr != nil && !errors.Is(archiveErr, io.ErrClosedPipe) {
			return archiveErr
		}

		return fmt.Errorf("unable to run the backup: %w", runErr)
	}

	return archiveErr
}
