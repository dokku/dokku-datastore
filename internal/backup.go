package internal

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

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

// storageClassEnv is what the backup image reads to decide the s3 storage class
// it uploads with. The property that sets it is
// service.BackupStorageClassProperty, and like the keyserver it is passed only
// when a service has one, so that the bucket's default applies otherwise.
const storageClassEnv = "S3_STORAGE_CLASS"

// backupTimestampEnv is what the backup image reads to decide whether the
// object key ends in a timestamp. The property that sets it is
// service.BackupTimestampProperty, and it is passed only to turn the timestamp
// off, so the image keeps its own default otherwise.
const backupTimestampEnv = "BACKUP_TIMESTAMP"

// backupSourceEnv tells the backup image where to read the backup from, and
// backupSourceStdin has it read a tar stream on stdin
const (
	backupSourceEnv   = "BACKUP_SOURCE"
	backupSourceStdin = "stdin"
)

// expectedSizeEnv is how large the backup image expects the upload to be,
// which it sizes the parts of a multipart upload by
const expectedSizeEnv = "S3_EXPECTED_SIZE"

// backupUploadSize overestimates the upload of an export of the given size.
// The archive adds a header for the directory and the export, pads the export
// to a whole block and ends with two empty blocks; gzip and gpg then add a
// little more on data that does not compress, which the margin covers the same
// way the backup image covers it when it estimates a mounted directory.
func backupUploadSize(exportSize int64) int64 {
	const block = 512
	archiveSize := 2*block + (exportSize+block-1)/block*block + 2*block
	return archiveSize + archiveSize/10 + 1048576
}

// the entries of the archive a backup ships, laid out as the image laid them
// out when it archived a directory mounted at /backup, so that a backup made
// either way is restored the same way
const (
	backupArchiveDir    = "backup/"
	backupArchiveExport = "backup/export"
)

// backupExport is the export a backup is made from. It holds every database
// rather than the one named for the service, wherever the datastore can export
// them: an app free to make databases of its own on the server would otherwise
// be backed up without them, with nothing to say so.
func backupExport(s *service.Datastore, serviceName string, w io.Writer) service.ExportServiceInput {
	return service.ExportServiceInput{
		Datastore:    s,
		ServiceName:  serviceName,
		Writer:       w,
		AllDatabases: s.ExportsAllDatabases(),
	}
}

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

// BackupAuth stores the credentials backups are shipped with. The stored
// settings are replaced as a whole, so an optional setting that is not passed
// is removed rather than left over from an earlier call.
func BackupAuth(ctx context.Context, input BackupAuthInput) error {
	// checked before anything is written, so a refused call leaves the stored
	// settings as they were
	if err := ValidateEndpointURL(input.EndpointURL); err != nil {
		return err
	}

	folder := service.Folders(input.Datastore, input.ServiceName).Backup

	// the pair is written first, which also creates the folder
	if err := writeBackupFile(folder, accessKeyIDFile, input.AccessKeyID); err != nil {
		return err
	}
	if err := writeBackupFile(folder, secretAccessKeyFile, input.SecretAccessKey); err != nil {
		return err
	}

	optional := []struct {
		name  string
		value string
	}{
		{defaultRegionFile, input.DefaultRegion},
		{signatureVersionFile, input.SignatureVersion},
		{endpointURLFile, input.EndpointURL},
	}

	for _, entry := range optional {
		if entry.value != "" {
			if err := writeBackupFile(folder, entry.name, entry.value); err != nil {
				return err
			}
			continue
		}

		if err := removeBackupFile(folder, entry.name); err != nil {
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

// removeBackupFile deletes one of the backup settings files for a service. A
// file that is already gone is left that way, so that clearing a setting with
// the set command can be repeated the way clearing a property can.
func removeBackupFile(folder string, name string) error {
	filename := filepath.Join(folder, name)
	if err := os.Remove(filename); err != nil && !errors.Is(err, fs.ErrNotExist) {
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

	// BackupSchedulesProperty is the file every schedule besides an
	// un-overridden default is recorded in, together with the settings each
	// one sets for itself. It is a JSON array, and it is absent when the only
	// schedule is the default one with nothing of its own.
	BackupSchedulesProperty = "backup-schedules"

	// BackupScheduleIDDefault is the schedule backup-schedule records when it
	// is given no --id. It is the schedule the three properties above name,
	// and --id default names that same schedule rather than a second one.
	BackupScheduleIDDefault = "default"
)

// backupScheduleProperties are every property a scheduled backup writes
var backupScheduleProperties = []string{BackupScheduleProperty, BackupBucketProperty, BackupUseIAMProperty}

// CronEntryFormatJSON is the entry format dokku passes to the cron-entries
// trigger when it reads each entry as a json object on its own line. Only those
// entries can carry a MAILTO, and dokku versions that do not pass it refuse
// them, so they are printed only when it is passed.
const CronEntryFormatJSON = "json"

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

// validateBucketForCrontab reports whether a bucket can be written into the
// command a scheduled backup runs. It is looser than ValidateBucketName, so a
// schedule recorded before the s3 naming rules were checked keeps its crontab
// entry, and the backup it runs reports why the bucket is refused.
func validateBucketForCrontab(bucketName string) error {
	if bucketName == "" {
		return errors.New("Please specify an aws bucket for the backup") //nolint:staticcheck // matches the bash datastore plugins
	}

	if !bucketNamePattern.MatchString(bucketName) {
		return fmt.Errorf("invalid bucket name %q: only letters, numbers, dots, dashes, underscores and slashes are allowed", bucketName)
	}

	return nil
}

// s3BucketPattern is what an s3 general purpose bucket may be made of: lowercase
// letters, numbers, dots and dashes, starting and ending with a letter or number
var s3BucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`)

// s3BucketIPPattern matches a bucket formatted as an ip address, which s3 refuses
var s3BucketIPPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$`)

// s3ReservedBucketPrefixes and s3ReservedBucketSuffixes are what s3 keeps for
// itself at either end of a general purpose bucket name
var (
	s3ReservedBucketPrefixes = []string{"xn--", "sthree-", "amzn-s3-demo-"}
	s3ReservedBucketSuffixes = []string{"-s3alias", "--ol-s3", ".mrap", "--x-s3", "--table-s3"}
)

// ValidateBucketName reports whether a bucket given to a backup follows the s3
// general purpose bucket naming rules. The bucket may end in a path the backup
// is uploaded under, which is checked the way an object name is.
func ValidateBucketName(bucketName string) error {
	if bucketName == "" {
		return errors.New("Please specify an aws bucket for the backup") //nolint:staticcheck // matches the bash datastore plugins
	}

	// the aws cli adds the s3:// scheme itself, so a bucket named with one is
	// uploaded to s3://s3://
	if strings.Contains(bucketName, "://") {
		return fmt.Errorf("invalid bucket name %q: name the bucket without a scheme such as s3://, the endpoint of an s3 compatible service is set with backup-auth", bucketName)
	}

	bucket, path, hasPath := strings.Cut(bucketName, "/")
	if len(bucket) < 3 || len(bucket) > 63 {
		return fmt.Errorf("invalid bucket name %q: must be between 3 and 63 characters", bucket)
	}

	if !s3BucketPattern.MatchString(bucket) {
		return fmt.Errorf("invalid bucket name %q: only lowercase letters, numbers, dots and dashes are allowed, and it must start and end with a letter or number", bucket)
	}

	if strings.Contains(bucket, "..") {
		return fmt.Errorf("invalid bucket name %q: must not contain two adjacent dots", bucket)
	}

	if s3BucketIPPattern.MatchString(bucket) {
		return fmt.Errorf("invalid bucket name %q: must not be formatted as an ip address", bucket)
	}

	for _, prefix := range s3ReservedBucketPrefixes {
		if strings.HasPrefix(bucket, prefix) {
			return fmt.Errorf("invalid bucket name %q: must not start with the reserved prefix %s", bucket, prefix)
		}
	}

	for _, suffix := range s3ReservedBucketSuffixes {
		if strings.HasSuffix(bucket, suffix) {
			return fmt.Errorf("invalid bucket name %q: must not end with the reserved suffix %s", bucket, suffix)
		}
	}

	if !hasPath {
		return nil
	}

	if !bucketNamePattern.MatchString(path) {
		return fmt.Errorf("invalid bucket path %q: only letters, numbers, dots, dashes, underscores and slashes are allowed", path)
	}

	// a trailing or doubled slash would leave an empty segment in the key
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("invalid bucket path %q: must not end with a slash, or hold an empty, . or .. segment", path)
		}
	}

	return nil
}

// ValidateEndpointURL reports whether an endpoint backups can be shipped to is
// an http or https url. An empty value is valid and means aws s3.
func ValidateEndpointURL(value string) error {
	if value == "" {
		return nil
	}

	parsed, err := url.Parse(value)
	if err != nil || strings.ContainsAny(value, " \t\r\n") || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("invalid endpoint url %q: must be an http or https url such as https://nyc3.digitaloceanspaces.com", value)
	}

	return nil
}

// backupScheduleIDPattern is what a schedule id may be made of, the same
// characters a service name may be made of. The id is written into the command
// a scheduled backup runs, so anything a shell would read specially is refused.
var backupScheduleIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// BackupSchedule is one scheduled backup a service is recorded with.
//
// The cron expression, the bucket and whether it uses an instance role are the
// schedule itself. Every other field is a setting the schedule set for itself:
// a field left empty inherits the service's value, and the resolved value is
// filled in when a backup runs or the crontab is written. It is never written
// back, or a service value would become a setting of the schedule.
type BackupSchedule struct {
	// ID is the name the schedule is addressed by. The schedule made without
	// --id has BackupScheduleIDDefault.
	ID string `json:"id"`

	// Schedule is the cron schedule the backup runs on
	Schedule string `json:"schedule"`

	// BucketName is the bucket the dump is shipped to
	BucketName string `json:"bucket-name"`

	// UseIAM reports whether the backup runs against an instance role rather
	// than against stored credentials. It is written even when false, so a
	// schedule that does not use an instance role is distinct from one whose
	// file was cut short.
	UseIAM bool `json:"use-iam"`

	// StorageClass is the storage class this schedule uploads with, empty when
	// it inherits the service's backup-storage-class.
	StorageClass string `json:"storage-class,omitempty"`

	// ObjectName is the key this schedule uploads under, empty when it inherits
	// the service's backup-object-name.
	ObjectName string `json:"object-name,omitempty"`

	// Timestamp is "true" or "false" when this schedule decides for itself
	// whether the key ends in the time the backup started, and empty when it
	// inherits the service's backup-timestamp.
	Timestamp string `json:"timestamp,omitempty"`

	// Mailto is who cron mails the output of this schedule to, empty when it
	// inherits the service's backup-mailto. The resolved recipient is what the
	// crontab is written with, and it is not stored back onto this field.
	Mailto string `json:"mailto,omitempty"`

	// Keyserver is where this schedule fetches a public key from, empty when
	// it inherits the service's backup-keyserver.
	Keyserver string `json:"keyserver,omitempty"`

	// ExportArgs are the extra arguments this schedule's export is run with,
	// empty when it inherits the service's export-args.
	ExportArgs string `json:"export-args,omitempty"`

	// EncryptionPassphrase is the passphrase this schedule encrypts with. Set
	// together with PublicKeyID it replaces the service's encryption, and the
	// public key still takes precedence. Empty alongside PublicKeyID means the
	// schedule inherits the service's passphrase and public key.
	EncryptionPassphrase string `json:"encryption-passphrase,omitempty"`

	// PublicKeyID is the gpg public key this schedule encrypts with.
	PublicKeyID string `json:"public-key-id,omitempty"`
}

// ValidateBackupScheduleID reports whether an id can name a schedule.
func ValidateBackupScheduleID(id string) error {
	if !backupScheduleIDPattern.MatchString(id) {
		return fmt.Errorf("invalid backup schedule id %q: valid characters are [A-Za-z0-9_-]+", id)
	}

	return nil
}

// hasOverrides reports whether the schedule set any setting for itself.
func (b BackupSchedule) hasOverrides() bool {
	return b.StorageClass != "" || b.ObjectName != "" || b.Timestamp != "" || b.Mailto != "" || b.Keyserver != "" || b.ExportArgs != "" || b.hasOwnEncryption()
}

// hasOwnEncryption reports whether the schedule replaces the service's
// passphrase and public key with its own pair. Either field being set does,
// and a public key still takes precedence over a passphrase inside that pair.
func (b BackupSchedule) hasOwnEncryption() bool {
	return b.EncryptionPassphrase != "" || b.PublicKeyID != ""
}

// overrideNames are the properties the schedule set for itself, in the order
// the set command documents them.
func (b BackupSchedule) overrideNames() []string {
	names := []string{}
	if b.StorageClass != "" {
		names = append(names, service.BackupStorageClassProperty)
	}
	if b.ObjectName != "" {
		names = append(names, service.BackupObjectNameProperty)
	}
	if b.Timestamp != "" {
		names = append(names, service.BackupTimestampProperty)
	}
	if b.Mailto != "" {
		names = append(names, service.BackupMailtoProperty)
	}
	if b.Keyserver != "" {
		names = append(names, service.KeyserverProperty)
	}
	if b.ExportArgs != "" {
		names = append(names, service.ExportArgsProperty)
	}
	if b.EncryptionPassphrase != "" {
		names = append(names, backupEncryptionPassphraseProperty)
	}
	if b.PublicKeyID != "" {
		names = append(names, "backup-public-key-id")
	}

	return names
}

// withInheritedMailto returns the schedule with the recipient cron mails filled
// in from the service when the schedule did not set its own. The result is what
// the crontab is built from, and it is not what gets written back.
func (b BackupSchedule) withInheritedMailto(s *service.Datastore, serviceName string) BackupSchedule {
	if b.Mailto == "" {
		b.Mailto = service.BackupMailto(s, serviceName)
	}
	if b.ID == "" {
		b.ID = BackupScheduleIDDefault
	}

	return b
}

// Validate reports whether a schedule can be written into the dokku crontab
func (b BackupSchedule) Validate() error {
	if err := ValidateBackupSchedule(b.Schedule); err != nil {
		return err
	}

	if err := validateBucketForCrontab(b.BucketName); err != nil {
		return err
	}

	return service.ValidateBackupMailto(b.Mailto)
}

// readDefaultBackupSchedule reads the schedule made without --id from the three
// properties it is recorded in, and reports whether there is one. Its own
// settings, when it has any, are on the default object in the schedules file.
func readDefaultBackupSchedule(s *service.Datastore, serviceName string) (BackupSchedule, bool) {
	commandPrefix := s.Properties().CommandPrefix
	schedule := BackupSchedule{
		ID:         BackupScheduleIDDefault,
		Schedule:   common.PropertyGet(commandPrefix, serviceName, BackupScheduleProperty),
		BucketName: common.PropertyGet(commandPrefix, serviceName, BackupBucketProperty),
		UseIAM:     common.PropertyGet(commandPrefix, serviceName, BackupUseIAMProperty) == "true",
	}

	if schedule.Schedule == "" {
		return BackupSchedule{}, false
	}

	return schedule, true
}

// ReadBackupSchedule reads the schedule made without --id, including any
// settings it set for itself, and reports whether the service has one.
func ReadBackupSchedule(s *service.Datastore, serviceName string) (BackupSchedule, bool) {
	schedule, ok := readDefaultBackupSchedule(s, serviceName)
	if !ok {
		return BackupSchedule{}, false
	}

	stored, err := readStoredBackupSchedules(s, serviceName)
	if err != nil {
		return schedule, true
	}

	for _, item := range stored {
		if item.ID != BackupScheduleIDDefault {
			continue
		}

		// the three properties win when the file disagrees with them, so a
		// schedule changed with set is what a backup runs
		schedule.StorageClass = item.StorageClass
		schedule.ObjectName = item.ObjectName
		schedule.Timestamp = item.Timestamp
		schedule.Mailto = item.Mailto
		schedule.Keyserver = item.Keyserver
		schedule.ExportArgs = item.ExportArgs
		schedule.EncryptionPassphrase = item.EncryptionPassphrase
		schedule.PublicKeyID = item.PublicKeyID
		break
	}

	return schedule, true
}

// ReadBackupSchedules reads every schedule a service has. The default schedule
// is first, when it has one, and the others follow by id. A schedules file
// that does not decode is reported, and a default schedule recorded in the
// three properties is still returned.
func ReadBackupSchedules(s *service.Datastore, serviceName string) ([]BackupSchedule, error) {
	stored, err := readStoredBackupSchedules(s, serviceName)

	var schedules []BackupSchedule
	if schedule, ok := ReadBackupSchedule(s, serviceName); ok {
		schedules = append(schedules, schedule)
	}

	if err != nil {
		return schedules, err
	}

	named := make([]BackupSchedule, 0, len(stored))
	for _, schedule := range stored {
		if schedule.ID == "" || schedule.ID == BackupScheduleIDDefault {
			continue
		}

		named = append(named, schedule)
	}
	sort.Slice(named, func(i, j int) bool { return named[i].ID < named[j].ID })

	return append(schedules, named...), nil
}

// LookupBackupSchedule reads the schedule an id names. The default id reads
// the three properties, so it finds that schedule even when the schedules file
// has no object for it.
func LookupBackupSchedule(s *service.Datastore, serviceName string, id string) (BackupSchedule, error) {
	if id == "" {
		id = BackupScheduleIDDefault
	}
	if err := ValidateBackupScheduleID(id); err != nil {
		return BackupSchedule{}, err
	}

	schedules, err := ReadBackupSchedules(s, serviceName)
	for _, schedule := range schedules {
		if schedule.ID == id {
			return schedule, nil
		}
	}
	if err != nil {
		return BackupSchedule{}, err
	}

	return BackupSchedule{}, fmt.Errorf("There is no scheduled backup %s for %s.", id, serviceName) //nolint:staticcheck // matches the bash datastore plugins
}

// FormatBackupSchedules renders every schedule on one line, the way info
// reports them. Records are separated by semicolons and the id, cron
// expression, bucket and use-iam of one schedule by pipes. No schedule is an
// empty line.
func FormatBackupSchedules(schedules []BackupSchedule) string {
	if len(schedules) == 0 {
		return ""
	}

	parts := make([]string, len(schedules))
	for i, schedule := range schedules {
		parts[i] = strings.Join([]string{schedule.ID, schedule.Schedule, schedule.BucketName, strconv.FormatBool(schedule.UseIAM)}, "|")
	}

	return strings.Join(parts, ";")
}

// readStoredBackupSchedules reads the schedules file. A missing file is no
// extra schedule. A file that does not decode is an error, so a writer does
// not replace schedules it could not read.
func readStoredBackupSchedules(s *service.Datastore, serviceName string) ([]BackupSchedule, error) {
	raw := strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, BackupSchedulesProperty))
	if raw == "" {
		return nil, nil
	}

	var schedules []BackupSchedule
	if err := json.Unmarshal([]byte(raw), &schedules); err != nil {
		return nil, fmt.Errorf("unable to read the scheduled backups for %s: %w", serviceName, err)
	}

	return schedules, nil
}

// writeStoredBackupSchedules records the schedules file. The default schedule
// is kept only when it set something for itself, and it is written first. An
// empty list deletes the file rather than leaving an empty array.
func writeStoredBackupSchedules(s *service.Datastore, serviceName string, schedules []BackupSchedule) error {
	commandPrefix := s.Properties().CommandPrefix
	kept := make([]BackupSchedule, 0, len(schedules))
	var defaultSchedule *BackupSchedule
	named := make([]BackupSchedule, 0, len(schedules))
	for _, schedule := range schedules {
		switch schedule.ID {
		case BackupScheduleIDDefault:
			if schedule.hasOverrides() {
				copy := schedule
				defaultSchedule = &copy
			}
		case "":
		default:
			named = append(named, schedule)
		}
	}
	sort.Slice(named, func(i, j int) bool { return named[i].ID < named[j].ID })
	if defaultSchedule != nil {
		kept = append(kept, *defaultSchedule)
	}
	kept = append(kept, named...)

	if len(kept) == 0 {
		if err := common.PropertyDelete(commandPrefix, serviceName, BackupSchedulesProperty); err != nil {
			return fmt.Errorf("unable to record the scheduled backups: %w", err)
		}

		return nil
	}

	encoded, err := json.Marshal(kept)
	if err != nil {
		return fmt.Errorf("unable to record the scheduled backups: %w", err)
	}

	if err := common.PropertyWrite(commandPrefix, serviceName, BackupSchedulesProperty, string(encoded)); err != nil {
		return fmt.Errorf("unable to record the scheduled backups: %w", err)
	}

	return nil
}

// syncDefaultScheduleIdentity copies the default schedule's cron expression,
// bucket and instance role onto its object in the schedules file, when it has
// one, and leaves the settings that object set for itself in place. A default
// schedule with nothing of its own is not given an object.
func syncDefaultScheduleIdentity(s *service.Datastore, serviceName string, identity BackupSchedule) error {
	stored, err := readStoredBackupSchedules(s, serviceName)
	if err != nil {
		return err
	}

	for i, item := range stored {
		if item.ID != BackupScheduleIDDefault {
			continue
		}

		item.Schedule = identity.Schedule
		item.BucketName = identity.BucketName
		item.UseIAM = identity.UseIAM
		stored[i] = item
		return writeStoredBackupSchedules(s, serviceName, stored)
	}

	return nil
}

// upsertNamedBackupSchedule records a schedule other than the default one,
// replacing that id's cron expression, bucket and instance role and keeping
// the settings it already set for itself.
func upsertNamedBackupSchedule(s *service.Datastore, serviceName string, schedule BackupSchedule) error {
	stored, err := readStoredBackupSchedules(s, serviceName)
	if err != nil {
		return err
	}

	for i, item := range stored {
		if item.ID != schedule.ID {
			continue
		}

		item.Schedule = schedule.Schedule
		item.BucketName = schedule.BucketName
		item.UseIAM = schedule.UseIAM
		stored[i] = item
		return writeStoredBackupSchedules(s, serviceName, stored)
	}

	return writeStoredBackupSchedules(s, serviceName, append(stored, schedule))
}

// saveBackupSchedule writes one schedule back, keeping every other schedule.
// The default schedule's object is dropped when it no longer sets anything for
// itself. Its cron expression, bucket and instance role stay in their properties.
func saveBackupSchedule(s *service.Datastore, serviceName string, schedule BackupSchedule) error {
	stored, err := readStoredBackupSchedules(s, serviceName)
	if err != nil {
		return err
	}

	replaced := false
	for i, item := range stored {
		if item.ID != schedule.ID {
			continue
		}

		stored[i] = schedule
		replaced = true
		break
	}
	if !replaced {
		stored = append(stored, schedule)
	}

	return writeStoredBackupSchedules(s, serviceName, stored)
}

// defaultLogsDir is where dokku keeps its logs when DOKKU_LOGS_DIR is not set,
// the same default the dokku script exports
const defaultLogsDir = "/var/log/dokku"

// logsDir is where dokku keeps its logs. Both the cron-entries trigger and
// backup-logs are run by dokku, which exports DOKKU_LOGS_DIR to each, so the two
// agree on it.
func logsDir() string {
	if dir := strings.TrimSpace(os.Getenv("DOKKU_LOGS_DIR")); dir != "" {
		return dir
	}

	return defaultLogsDir
}

// BackupLogFile is where the output of a service's scheduled backups goes. Each
// service has one of its own, since a file shared by a datastore's services has
// no way of telling whose backup wrote a line. Neither a command prefix nor a
// service name can hold a dot, so no two services share a file, and the name
// ends in .log so that dokku's logrotate configuration rotates it.
func BackupLogFile(commandPrefix string, serviceName string) string {
	return filepath.Join(logsDir(), fmt.Sprintf("%s.%s.backup.log", commandPrefix, serviceName))
}

// BackupTimestamp is how the time a backup starts and ends at is printed, so
// that each run can be told apart in the log it is appended to
func BackupTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// backupCommand is the command a scheduled backup runs. It is run from the dokku
// crontab, whose PATH includes wherever dokku is installed.
func backupCommand(commandPrefix string, serviceName string, schedule BackupSchedule) string {
	id := schedule.ID
	if id == "" {
		id = BackupScheduleIDDefault
	}

	command := fmt.Sprintf("dokku %s:backup %s %s --id %s", commandPrefix, serviceName, schedule.BucketName, id)
	if schedule.UseIAM {
		command = fmt.Sprintf("%s --use-iam", command)
	}

	return command
}

// CronTask is a scheduled backup as the cron-entries trigger hands it to dokku
type CronTask struct {
	// Schedule is the cron schedule the backup runs on
	Schedule string `json:"schedule"`

	// Command is the command the backup runs
	Command string `json:"command"`

	// LogFile is the file the output of the backup is appended to
	LogFile string `json:"log-file"`

	// Mailto is who cron mails the output of the backup to, instead of the
	// global MAILTO
	Mailto string `json:"mailto,omitempty"`
}

// Text builds the line the cron-entries trigger prints for dokku versions that
// do not read json entries: the schedule, the command and the log file,
// separated by semicolons. Those versions refuse a line with a fourth field, so
// the task's MAILTO is left out.
func (t CronTask) Text() string {
	return strings.Join([]string{t.Schedule, t.Command, t.LogFile}, ";")
}

// JSONLine builds the line the cron-entries trigger prints for dokku versions
// that read json entries: the task as a json object on a single line
func (t CronTask) JSONLine() (string, error) {
	line, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("unable to encode the cron entry: %w", err)
	}

	return string(line), nil
}

// CronEntry builds the task the cron-entries trigger prints for a scheduled
// backup
func CronEntry(commandPrefix string, serviceName string, schedule BackupSchedule) CronTask {
	return CronTask{
		Schedule: schedule.Schedule,
		Command:  backupCommand(commandPrefix, serviceName, schedule),
		LogFile:  BackupLogFile(commandPrefix, serviceName),
		Mailto:   schedule.Mailto,
	}
}

// CrontabLine builds the line dokku writes into its crontab for a scheduled
// backup, the same way it writes any task handed to it by cron-entries. A task
// with a MAILTO keeps its output on stdout, where cron mails it from, and dokku
// writes it under a MAILTO line of its own.
func CrontabLine(commandPrefix string, serviceName string, schedule BackupSchedule) string {
	task := CronEntry(commandPrefix, serviceName, schedule)
	if task.Mailto != "" {
		return fmt.Sprintf("%s %s 2>&1 | tee -a %s", task.Schedule, task.Command, task.LogFile)
	}

	return fmt.Sprintf("%s %s &>> %s", task.Schedule, task.Command, task.LogFile)
}

// BackupScheduleReport is one scheduled backup as backup-schedule-cat reports
// it in json. The settings are the ones a backup of that schedule runs with,
// after inheriting from the service, and overrides names the properties the
// schedule set itself. The passphrase is reported as a fingerprint.
type BackupScheduleReport struct {
	ID                              string   `json:"id"`
	Schedule                        string   `json:"schedule"`
	BucketName                      string   `json:"bucket-name"`
	UseIAM                          bool     `json:"use-iam"`
	StorageClass                    string   `json:"storage-class"`
	ObjectName                      string   `json:"object-name"`
	Timestamp                       string   `json:"timestamp"`
	Mailto                          string   `json:"mailto"`
	Keyserver                       string   `json:"keyserver"`
	PublicKeyID                     string   `json:"public-key-id"`
	EncryptionPassphraseFingerprint string   `json:"encryption-passphrase-fingerprint"`
	ExportArgs                      string   `json:"export-args"`
	Overrides                       []string `json:"overrides"`
	CrontabLine                     string   `json:"crontab-line"`
}

// BackupSchedulesReport is every scheduled backup, as backup-schedule-cat
// reports them when a service has more than one and no schedule was named.
type BackupSchedulesReport struct {
	Schedules []BackupScheduleReport `json:"schedules"`
}

// reportBackupSchedule builds the report of one schedule, with the settings it
// inherits from the service filled in.
func reportBackupSchedule(s *service.Datastore, serviceName string, schedule BackupSchedule) BackupScheduleReport {
	resolved := schedule.withInheritedMailto(s, serviceName)

	storageClass := schedule.StorageClass
	if storageClass == "" {
		storageClass = service.BackupStorageClass(s, serviceName)
	}
	objectName := schedule.ObjectName
	if objectName == "" {
		objectName = service.BackupObjectName(s, serviceName)
	}
	timestamp := schedule.Timestamp
	if timestamp == "" {
		timestamp = strconv.FormatBool(service.BackupTimestamp(s, serviceName))
	}
	keyserver := schedule.Keyserver
	if keyserver == "" {
		keyserver = service.Keyserver(s, serviceName)
	}
	exportArgs := schedule.ExportArgs
	if exportArgs == "" {
		exportArgs = service.ServiceExtraArgs(s, serviceName, service.ExportArgsProperty)
	}

	publicKeyID, passphraseFingerprint := resolvedScheduleEncryption(s, serviceName, schedule)

	return BackupScheduleReport{
		ID:                              schedule.ID,
		Schedule:                        schedule.Schedule,
		BucketName:                      schedule.BucketName,
		UseIAM:                          schedule.UseIAM,
		StorageClass:                    storageClass,
		ObjectName:                      objectName,
		Timestamp:                       timestamp,
		Mailto:                          resolved.Mailto,
		Keyserver:                       keyserver,
		PublicKeyID:                     publicKeyID,
		EncryptionPassphraseFingerprint: passphraseFingerprint,
		ExportArgs:                      exportArgs,
		Overrides:                       schedule.overrideNames(),
		CrontabLine:                     CrontabLine(s.Properties().CommandPrefix, serviceName, resolved),
	}
}

// resolvedScheduleEncryption is the public key and the passphrase fingerprint
// a schedule runs with. A schedule that set either replaces the service's
// pair, and otherwise the service's files are reported.
func resolvedScheduleEncryption(s *service.Datastore, serviceName string, schedule BackupSchedule) (string, string) {
	if schedule.hasOwnEncryption() {
		passphraseFingerprint := ""
		if schedule.EncryptionPassphrase != "" {
			passphraseFingerprint = fingerprint(schedule.EncryptionPassphrase)
		}

		return schedule.PublicKeyID, passphraseFingerprint
	}

	folder := service.Folders(s, serviceName).BackupEncryption
	return common.ReadFirstLine(filepath.Join(folder, publicKeyIDFile)), storedFingerprint(filepath.Join(folder, encryptionKeyFile))
}

// BackupScheduleCatOutput reports a service's scheduled backups. One schedule,
// or an id, is that schedule and the text is its crontab line. Several, with
// no id, are every schedule and the text is the id and the crontab line of
// each, separated by a tab.
func BackupScheduleCatOutput(s *service.Datastore, serviceName string, id string) (any, string, error) {
	if id != "" {
		schedule, err := LookupBackupSchedule(s, serviceName, id)
		if err != nil {
			return nil, "", err
		}

		report := reportBackupSchedule(s, serviceName, schedule)
		return report, report.CrontabLine + "\n", nil
	}

	schedules, err := ReadBackupSchedules(s, serviceName)
	if err != nil {
		return nil, "", err
	}
	if len(schedules) == 0 {
		return nil, "", fmt.Errorf("There is no scheduled backup for %s.", serviceName) //nolint:staticcheck // matches the bash datastore plugins
	}
	if len(schedules) == 1 {
		report := reportBackupSchedule(s, serviceName, schedules[0])
		return report, report.CrontabLine + "\n", nil
	}

	reports := make([]BackupScheduleReport, 0, len(schedules))
	lines := make([]string, 0, len(schedules))
	for _, schedule := range schedules {
		report := reportBackupSchedule(s, serviceName, schedule)
		reports = append(reports, report)
		lines = append(lines, schedule.ID+"\t"+report.CrontabLine)
	}

	return BackupSchedulesReport{Schedules: reports}, strings.Join(lines, "\n") + "\n", nil
}

// BackupScheduleCatReport reports a service's only scheduled backup.
func BackupScheduleCatReport(s *service.Datastore, serviceName string) (BackupScheduleReport, error) {
	value, _, err := BackupScheduleCatOutput(s, serviceName, "")
	if err != nil {
		return BackupScheduleReport{}, err
	}

	report, ok := value.(BackupScheduleReport)
	if !ok {
		return BackupScheduleReport{}, fmt.Errorf("%s has more than one scheduled backup, pass --id", serviceName)
	}

	return report, nil
}

// BackupScheduleCat returns the text backup-schedule-cat prints for a service.
func BackupScheduleCat(s *service.Datastore, serviceName string) (string, error) {
	_, text, err := BackupScheduleCatOutput(s, serviceName, "")
	return text, err
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

// ParseLegacyCronFile reads back the schedule a legacy cron file was written
// with, and reports whether the file held a line an earlier version of the plugin
// wrote. Environment lines are skipped, and a MAILTO among them is kept as the
// schedule's MAILTO, since the file was the only place one could be set. The
// MAILTO is returned as written, and is left for the caller to validate.
func ParseLegacyCronFile(commandPrefix string, contents string) (BackupSchedule, bool) {
	mailto := ""
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if name, value, ok := strings.Cut(line, "="); ok && legacyCronEnvNamePattern.MatchString(strings.TrimSpace(name)) {
			if strings.TrimSpace(name) == "MAILTO" {
				mailto = strings.Trim(strings.TrimSpace(value), `"'`)
			}
			continue
		}

		schedule, ok := ParseCronEntry(commandPrefix, line)
		if !ok {
			return BackupSchedule{}, false
		}

		schedule.Mailto = mailto
		return schedule, true
	}

	return BackupSchedule{}, false
}

// legacyCronEnvNamePattern is what the name of an environment line in a cron
// file is made of
var legacyCronEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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

	// ID is the schedule to record. Empty records the default schedule, which
	// replaces that schedule's cron expression, bucket and instance role and
	// leaves every other schedule, and that schedule's own settings, in place.
	ID string
}

// ScheduleBackup records a scheduled backup for a service and has dokku write it
// into its crontab. A schedule named with an id replaces only that schedule.
func ScheduleBackup(ctx context.Context, input ScheduleBackupInput) error {
	id := input.ID
	if id == "" {
		id = BackupScheduleIDDefault
	}
	if err := ValidateBackupScheduleID(id); err != nil {
		return err
	}

	schedule := BackupSchedule{
		ID:         id,
		Schedule:   input.Schedule,
		BucketName: input.BucketName,
		UseIAM:     input.UseIAM,
	}

	// the crontab takes looser bucket names than a backup does, so that a schedule
	// recorded before they were checked keeps its entry, but a new one must be
	// one the backup it runs would accept
	if err := ValidateBucketName(input.BucketName); err != nil {
		return err
	}

	if err := schedule.Validate(); err != nil {
		return err
	}

	if id == BackupScheduleIDDefault {
		if err := writeBackupSchedule(input.Datastore, input.ServiceName, schedule); err != nil {
			return err
		}

		// the three properties are what a backup reads. Keeping the file in
		// step with them fails the command when the file cannot be read, after
		// the properties and the crontab already name the new schedule.
		syncErr := syncDefaultScheduleIdentity(input.Datastore, input.ServiceName, schedule)
		if err := regenerateCrontab(ctx); err != nil {
			return err
		}

		return syncErr
	}

	if err := upsertNamedBackupSchedule(input.Datastore, input.ServiceName, schedule); err != nil {
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

	// ID is the one schedule to remove. Empty removes the only schedule, and
	// refuses when the service has more than one.
	ID string

	// All removes every schedule. Destroying a service sets it, so a service
	// with several schedules is removed whole.
	All bool
}

// UnscheduleBackup removes a scheduled backup and has dokku write its crontab
// without it. A service with no scheduled backup is left alone, so that
// destroying one does not rewrite the crontab for nothing.
func UnscheduleBackup(ctx context.Context, input UnscheduleBackupInput) error {
	if input.ID != "" && input.All {
		return errors.New("--id and --all cannot be combined")
	}

	if input.All {
		return removeAllBackupSchedules(ctx, input.Datastore, input.ServiceName)
	}

	id := input.ID
	if id == "" {
		schedules, err := ReadBackupSchedules(input.Datastore, input.ServiceName)
		if err != nil {
			return err
		}

		switch len(schedules) {
		case 0:
			return nil
		case 1:
			id = schedules[0].ID
		default:
			return fmt.Errorf("%s has more than one scheduled backup, pass --id or --all", input.ServiceName)
		}
	} else if err := ValidateBackupScheduleID(id); err != nil {
		return err
	}

	if _, err := LookupBackupSchedule(input.Datastore, input.ServiceName, id); err != nil {
		return err
	}

	if err := removeBackupSchedule(input.Datastore, input.ServiceName, id); err != nil {
		return err
	}

	return regenerateCrontab(ctx)
}

// removeBackupSchedule removes one schedule. The schedules file is rewritten
// before the default schedule's properties are deleted, so a file that cannot
// be read does not drop the default schedule and leave its settings behind.
func removeBackupSchedule(s *service.Datastore, serviceName string, id string) error {
	stored, err := readStoredBackupSchedules(s, serviceName)
	if err != nil {
		return err
	}

	kept := make([]BackupSchedule, 0, len(stored))
	for _, schedule := range stored {
		if schedule.ID == id {
			continue
		}

		kept = append(kept, schedule)
	}
	if err := writeStoredBackupSchedules(s, serviceName, kept); err != nil {
		return err
	}

	if id != BackupScheduleIDDefault {
		return nil
	}

	commandPrefix := s.Properties().CommandPrefix
	for _, property := range backupScheduleProperties {
		if err := common.PropertyDelete(commandPrefix, serviceName, property); err != nil {
			return fmt.Errorf("unable to remove the %s property: %w", property, err)
		}
	}

	return nil
}

// removeAllBackupSchedules removes every schedule, including a schedules file
// that does not decode. Nothing scheduled leaves the crontab as it is.
func removeAllBackupSchedules(ctx context.Context, s *service.Datastore, serviceName string) error {
	commandPrefix := s.Properties().CommandPrefix
	scheduled := common.PropertyExists(commandPrefix, serviceName, BackupScheduleProperty) || common.PropertyExists(commandPrefix, serviceName, BackupSchedulesProperty)

	properties := append([]string{}, backupScheduleProperties...)
	properties = append(properties, BackupSchedulesProperty)
	for _, property := range properties {
		if err := common.PropertyDelete(commandPrefix, serviceName, property); err != nil {
			return fmt.Errorf("unable to remove the %s property: %w", property, err)
		}
	}

	if !scheduled {
		return nil
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
	// image appends and the extension
	BackupName string

	// OmitTimestamp has the image upload to BackupName with no timestamp, so
	// every backup lands on the same key
	OmitTimestamp bool

	// ExpectedSize is an overestimate of the upload in bytes, passed only when
	// known. A stream on stdin is otherwise uploaded in parts sized for one no
	// larger than about 78 GiB, and a larger dump fails to upload.
	ExpectedSize int64

	// Settings are the values read from the backup settings files, keyed by the
	// environment variable each file is named after
	Settings map[string]string

	// Keyserver is where the image fetches a public key from, passed only when
	// a service sets one so that the image otherwise keeps its own default
	Keyserver string

	// StorageClass is the s3 storage class the object is uploaded with, passed
	// only when a service sets one so that the bucket's default otherwise applies
	StorageClass string

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
	if input.OmitTimestamp {
		setenv(backupTimestampEnv, "false")
	}
	setenv(backupSourceEnv, backupSourceStdin)
	if input.ExpectedSize > 0 {
		setenv(expectedSizeEnv, strconv.FormatInt(input.ExpectedSize, 10))
	}

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

	if input.StorageClass != "" {
		setenv(storageClassEnv, input.StorageClass)
	}

	return append(args, input.Image), env
}

// BackupInput is the input for the Backup function
type BackupInput struct {
	BucketName  string
	Datastore   *service.Datastore
	ServiceName string
	UseIAM      bool

	// ScheduleID is the schedule whose settings this backup runs with. Empty
	// uses the service's settings, and the bucket and instance role still come
	// from this input, which is what the crontab line passes.
	ScheduleID string
}

// the status a backup ended with, as the post-backup trigger is handed it
const (
	BackupStatusSuccess = "success"
	BackupStatusFailure = "failure"
)

// PostBackupInput is the input for the CallPostBackup function
type PostBackupInput struct {
	// BucketName is the bucket the backup was made to, empty when none was named
	BucketName string

	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

	// ServiceName is the service that was backed up
	ServiceName string

	// Status is whether the backup succeeded or failed
	Status string
}

// CallPostBackup fires the service-action post-backup trigger once a backup has
// ended, so that a plugin can tell something outside dokku whether it succeeded
func CallPostBackup(ctx context.Context, input PostBackupInput) error {
	_, err := execx.PlugnTrigger(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"post-backup", input.Datastore.ServiceType(), input.ServiceName, input.BucketName, input.Status},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action post-backup trigger: %w", err)
	}

	return nil
}

// backupScheduleForRun reads the schedule a backup was asked to run with.
// A backup that names no schedule uses the service's settings, which an empty
// schedule stands in for.
func backupScheduleForRun(input BackupInput) (BackupSchedule, error) {
	if input.ScheduleID == "" {
		return BackupSchedule{}, nil
	}

	return LookupBackupSchedule(input.Datastore, input.ServiceName, input.ScheduleID)
}

// Backup exports a service and ships the result to an s3 bucket
func Backup(ctx context.Context, input BackupInput) error {
	serviceFolders := service.Folders(input.Datastore, input.ServiceName)
	commandPrefix := input.Datastore.Properties().CommandPrefix

	schedule, err := backupScheduleForRun(input)
	if err != nil {
		return err
	}

	backupName := schedule.ObjectName
	if backupName == "" {
		backupName = service.BackupObjectName(input.Datastore, input.ServiceName)
	}
	if backupName == "" {
		backupName = fmt.Sprintf("%s-%s", commandPrefix, input.ServiceName)
	}

	omitTimestamp := !service.BackupTimestamp(input.Datastore, input.ServiceName)
	if schedule.Timestamp != "" {
		omitTimestamp = schedule.Timestamp == "false"
	}
	keyserver := schedule.Keyserver
	if keyserver == "" {
		keyserver = service.Keyserver(input.Datastore, input.ServiceName)
	}
	storageClass := schedule.StorageClass
	if storageClass == "" {
		storageClass = service.BackupStorageClass(input.Datastore, input.ServiceName)
	}

	arguments := BackupArgsInput{
		BucketName:    input.BucketName,
		BackupName:    backupName,
		OmitTimestamp: omitTimestamp,
		Keyserver:     keyserver,
		StorageClass:  storageClass,
		Image:         hostenv.S3BackupImage,
		Settings:      map[string]string{},
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

	export := backupExport(input.Datastore, input.ServiceName, handle)
	if schedule.ExportArgs != "" {
		extraArgs, err := service.SplitExtraArgs(schedule.ExportArgs)
		if err != nil {
			handle.Close()
			return fmt.Errorf("unable to read the export arguments of %s: %w", schedule.ID, err)
		}

		export.ExtraArgs = extraArgs
	}
	if err := input.Datastore.ExportService(ctx, export); err != nil {
		handle.Close()
		return err
	}

	if err := handle.Close(); err != nil {
		return fmt.Errorf("unable to close %s: %w", exportFile, err)
	}

	stat, err := os.Stat(exportFile)
	if err != nil {
		return fmt.Errorf("unable to read %s: %w", exportFile, err)
	}
	arguments.ExpectedSize = backupUploadSize(stat.Size())

	for _, name := range []string{defaultRegionFile, signatureVersionFile, endpointURLFile} {
		filename := filepath.Join(serviceFolders.Backup, name)
		if common.FileExists(filename) {
			arguments.Settings[name] = common.ReadFirstLine(filename)
		}
	}

	// a schedule that set a passphrase or a public key replaces the service's
	// pair for this run. The public key still takes precedence inside the pair.
	if schedule.hasOwnEncryption() {
		if schedule.EncryptionPassphrase != "" {
			arguments.Settings[encryptionKeyFile] = schedule.EncryptionPassphrase
		}
		if schedule.PublicKeyID != "" {
			arguments.Settings[publicKeyIDFile] = schedule.PublicKeyID
		}
	} else {
		for _, name := range []string{encryptionKeyFile, publicKeyIDFile} {
			filename := filepath.Join(serviceFolders.BackupEncryption, name)
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
