package internal

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

// withScheduleService points the package at a temporary root and creates a
// service in it, with properties read and written through the environment
func withScheduleService(t *testing.T, serviceNames ...string) *service.Datastore {
	t.Helper()

	withDataRoot(t)
	t.Setenv("DOKKU_LIB_ROOT", service.DokkuLibRoot)
	// no plugins to trigger, so regenerating the crontab does nothing
	t.Setenv("PLUGIN_PATH", "")

	datastore := service.Datastores["redis"]
	for _, serviceName := range serviceNames {
		if err := os.MkdirAll(service.Folders(datastore, serviceName).Root, 0755); err != nil {
			t.Fatalf("failed to create the service root: %s", err)
		}
	}

	return datastore
}

// The schedule is written into the dokku crontab, which is refused as a whole
// when a line in it is invalid, so anything cron cannot run is turned away
// before it is recorded. "daily" is what issue 6 was scheduled with: cron
// skipped it without a word, and the service was never backed up.
func TestValidateBackupSchedule(t *testing.T) {
	tests := []struct {
		schedule string
		valid    bool
	}{
		{schedule: "0 3 * * *", valid: true},
		{schedule: "*/15 * * * *", valid: true},
		{schedule: "0 3 * * 1-5", valid: true},
		{schedule: "@daily", valid: true},
		{schedule: "@hourly", valid: true},
		{schedule: "@weekly", valid: true},
		{schedule: "", valid: false},
		{schedule: "daily", valid: false},
		{schedule: "@every 1h", valid: false},
		{schedule: "0 3 * *", valid: false},
		{schedule: "0 3 * * * *", valid: false},
		{schedule: "61 3 * * *", valid: false},
		{schedule: "0 3 * * *; rm -rf /", valid: false},
		{schedule: "0 3 * * *\n0 4 * * *", valid: false},
	}

	for _, test := range tests {
		t.Run(test.schedule, func(t *testing.T) {
			err := ValidateBackupSchedule(test.schedule)
			if test.valid && err != nil {
				t.Errorf("expected %q to be valid, got %s", test.schedule, err)
			}
			if !test.valid && err == nil {
				t.Errorf("expected %q to be refused", test.schedule)
			}
		})
	}
}

// The bucket is written into a shell command and a semicolon separated line, so
// anything either reads specially is refused
func TestValidateBucketName(t *testing.T) {
	tests := []struct {
		bucketName string
		valid      bool
	}{
		{bucketName: "my-bucket", valid: true},
		{bucketName: "my.bucket_2", valid: true},
		{bucketName: "my-bucket/with/a/prefix", valid: true},
		{bucketName: "", valid: false},
		{bucketName: "my bucket", valid: false},
		{bucketName: "my-bucket;true", valid: false},
		{bucketName: "my-bucket$(true)", valid: false},
		{bucketName: "my-bucket`true`", valid: false},
		{bucketName: "my-bucket&", valid: false},
	}

	for _, test := range tests {
		t.Run(test.bucketName, func(t *testing.T) {
			err := ValidateBucketName(test.bucketName)
			if test.valid && err != nil {
				t.Errorf("expected %q to be valid, got %s", test.bucketName, err)
			}
			if !test.valid && err == nil {
				t.Errorf("expected %q to be refused", test.bucketName)
			}
		})
	}
}

func TestCronEntry(t *testing.T) {
	tests := []struct {
		name     string
		schedule BackupSchedule
		expected string
	}{
		{
			name:     "a plain schedule",
			schedule: BackupSchedule{Schedule: "0 3 * * *", BucketName: "my-bucket"},
			expected: "0 3 * * *;dokku redis:backup lollipop my-bucket;/var/log/dokku/redis.log",
		},
		{
			name:     "using an iam profile",
			schedule: BackupSchedule{Schedule: "@daily", BucketName: "my-bucket", UseIAM: true},
			expected: "@daily;dokku redis:backup lollipop my-bucket --use-iam;/var/log/dokku/redis.log",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := CronEntry("redis", "lollipop", test.schedule)
			if actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}

			// dokku refuses a line that is not two or three fields
			if fields := strings.Split(actual, ";"); len(fields) != 3 {
				t.Errorf("expected three fields, got %d in %q", len(fields), actual)
			}
		})
	}
}

// What dokku writes into its crontab for a task handed to it with a log file
func TestCrontabLine(t *testing.T) {
	tests := []struct {
		name     string
		schedule BackupSchedule
		expected string
	}{
		{
			name:     "a plain schedule",
			schedule: BackupSchedule{Schedule: "0 3 * * *", BucketName: "my-bucket"},
			expected: "0 3 * * * dokku redis:backup lollipop my-bucket &>> /var/log/dokku/redis.log",
		},
		{
			name:     "using an iam profile",
			schedule: BackupSchedule{Schedule: "@daily", BucketName: "my-bucket", UseIAM: true},
			expected: "@daily dokku redis:backup lollipop my-bucket --use-iam &>> /var/log/dokku/redis.log",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := CrontabLine("redis", "lollipop", test.schedule); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// The properties are the only record of what a service was scheduled with, so
// what is scheduled has to be what is read back, and unscheduling has to leave
// nothing behind
func TestScheduleBackupRoundTrip(t *testing.T) {
	datastore := withScheduleService(t, "lollipop")

	if _, ok := ReadBackupSchedule(datastore, "lollipop"); ok {
		t.Fatal("expected a new service to have no scheduled backup")
	}

	if _, err := BackupScheduleCat(datastore, "lollipop"); err == nil {
		t.Error("expected cat to fail with no scheduled backup")
	}

	if err := ScheduleBackup(t.Context(), ScheduleBackupInput{
		BucketName:  "my-bucket",
		Datastore:   datastore,
		Schedule:    "0 3 * * *",
		ServiceName: "lollipop",
		UseIAM:      true,
	}); err != nil {
		t.Fatalf("failed to schedule the backup: %s", err)
	}

	schedule, ok := ReadBackupSchedule(datastore, "lollipop")
	if !ok {
		t.Fatal("expected the backup to be scheduled")
	}
	expected := BackupSchedule{Schedule: "0 3 * * *", BucketName: "my-bucket", UseIAM: true}
	if schedule != expected {
		t.Errorf("expected %+v, got %+v", expected, schedule)
	}

	contents, err := BackupScheduleCat(datastore, "lollipop")
	if err != nil {
		t.Fatalf("failed to cat the schedule: %s", err)
	}
	if contents != "0 3 * * * dokku redis:backup lollipop my-bucket --use-iam &>> /var/log/dokku/redis.log\n" {
		t.Errorf("unexpected cat output %q", contents)
	}

	// scheduling again without --use-iam drops it rather than keeping the old
	// value
	if err := ScheduleBackup(t.Context(), ScheduleBackupInput{
		BucketName:  "my-bucket",
		Datastore:   datastore,
		Schedule:    "@daily",
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("failed to schedule the backup again: %s", err)
	}
	if schedule, _ := ReadBackupSchedule(datastore, "lollipop"); schedule.UseIAM || schedule.Schedule != "@daily" {
		t.Errorf("expected the new schedule without iam, got %+v", schedule)
	}

	if err := UnscheduleBackup(t.Context(), UnscheduleBackupInput{Datastore: datastore, ServiceName: "lollipop"}); err != nil {
		t.Fatalf("failed to unschedule the backup: %s", err)
	}

	if _, ok := ReadBackupSchedule(datastore, "lollipop"); ok {
		t.Error("expected the backup to be unscheduled")
	}

	// and again, which has nothing to do
	if err := UnscheduleBackup(t.Context(), UnscheduleBackupInput{Datastore: datastore, ServiceName: "lollipop"}); err != nil {
		t.Errorf("expected unscheduling twice to succeed, got %s", err)
	}
}

// A schedule cron cannot run is refused before anything is recorded, so a
// service already scheduled keeps the schedule it had
func TestScheduleBackupRefusesAnInvalidSchedule(t *testing.T) {
	datastore := withScheduleService(t, "lollipop")

	if err := ScheduleBackup(t.Context(), ScheduleBackupInput{
		BucketName:  "my-bucket",
		Datastore:   datastore,
		Schedule:    "0 3 * * *",
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("failed to schedule the backup: %s", err)
	}

	for _, input := range []ScheduleBackupInput{
		{BucketName: "my-bucket", Schedule: "daily"},
		{BucketName: "my bucket", Schedule: "@daily"},
	} {
		input.Datastore = datastore
		input.ServiceName = "lollipop"
		if err := ScheduleBackup(t.Context(), input); err == nil {
			t.Errorf("expected %+v to be refused", input)
		}
	}

	if schedule, _ := ReadBackupSchedule(datastore, "lollipop"); schedule.Schedule != "0 3 * * *" || schedule.BucketName != "my-bucket" {
		t.Errorf("expected the earlier schedule to be kept, got %+v", schedule)
	}
}

// Earlier versions wrote the schedule to a cron file, which is read once to
// migrate it. Every shape they wrote has to be read back.
func TestParseCronEntryReadsLegacyCronFiles(t *testing.T) {
	tests := []struct {
		name     string
		entry    string
		expected BackupSchedule
	}{
		{
			name:     "a plain schedule",
			entry:    "0 3 * * * dokku /usr/bin/dokku redis:backup lollipop my-bucket",
			expected: BackupSchedule{Schedule: "0 3 * * *", BucketName: "my-bucket"},
		},
		{
			name:     "a one field schedule",
			entry:    "@daily dokku /usr/bin/dokku redis:backup lollipop my-bucket",
			expected: BackupSchedule{Schedule: "@daily", BucketName: "my-bucket"},
		},
		{
			name:     "using an iam profile",
			entry:    "0 3 * * * dokku /usr/bin/dokku redis:backup lollipop my-bucket --use-iam",
			expected: BackupSchedule{Schedule: "0 3 * * *", BucketName: "my-bucket", UseIAM: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schedule, ok := ParseCronEntry("redis", test.entry)
			if !ok {
				t.Fatalf("expected %q to parse", test.entry)
			}

			if schedule != test.expected {
				t.Errorf("expected %+v, got %+v", test.expected, schedule)
			}
		})
	}
}

// Anything else in the cron directory is not a schedule this wrote, and reading
// one as if it were would report a service as scheduled when it is not.
func TestParseCronEntryRejectsWhatItDidNotWrite(t *testing.T) {
	tests := []struct {
		name  string
		entry string
	}{
		{name: "nothing at all", entry: ""},
		{name: "another plugin's backup", entry: "0 3 * * * dokku /usr/bin/dokku postgres:backup lollipop my-bucket"},
		{name: "another command", entry: "0 3 * * * dokku /usr/bin/dokku redis:export lollipop"},
		{name: "no bucket", entry: "0 3 * * * dokku /usr/bin/dokku redis:backup lollipop"},
		{name: "no schedule", entry: "redis:backup lollipop my-bucket"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, ok := ParseCronEntry("redis", test.entry); ok {
				t.Errorf("expected %q not to parse", test.entry)
			}
		})
	}
}

// The keyserver reaches the backup image as an environment variable, and only
// when a service has one: the image has a default of its own, and passing an
// empty value would override it with nothing.
func TestBackupArgsCarriesTheKeyserverOnlyWhenSet(t *testing.T) {
	base := BackupArgsInput{
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		BucketName:      "bucket",
		BackupName:      "redis-lollipop",
		Image:           "dokku/s3backup:0.19.1",
	}

	withKeyserver := base
	withKeyserver.Keyserver = "http://10.0.0.2:11371"

	args, env := BackupArgs(withKeyserver)
	if joined := strings.Join(args, " "); !strings.Contains(joined, "-e KEYSERVER") {
		t.Errorf("expected the keyserver to be passed, got %s", joined)
	}
	if env["KEYSERVER"] != "http://10.0.0.2:11371" {
		t.Errorf("expected the keyserver in the environment, got %q", env["KEYSERVER"])
	}

	args, env = BackupArgs(base)
	if joined := strings.Join(args, " "); strings.Contains(joined, "KEYSERVER") {
		t.Errorf("expected no keyserver when none is set, got %s", joined)
	}
	if _, ok := env["KEYSERVER"]; ok {
		t.Errorf("expected no keyserver in the environment when none is set")
	}
}

// The settings are read from files named after the variables they become, and
// the image is always last because everything after it would be its command.
func TestBackupArgsPassesTheSettingsItIsGiven(t *testing.T) {
	args, env := BackupArgs(BackupArgsInput{
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		BucketName:      "bucket",
		BackupName:      "redis-lollipop",
		Image:           "dokku/s3backup:0.19.1",
		Settings: map[string]string{
			"ENCRYPT_WITH_PUBLIC_KEY_ID": "DEADBEEF",
			"ENDPOINT_URL":               "http://10.0.0.3:9000",
		},
	})

	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"-e AWS_ACCESS_KEY_ID",
		"-e AWS_SECRET_ACCESS_KEY",
		"-e BUCKET_NAME",
		"-e BACKUP_NAME",
		"-e BACKUP_SOURCE",
		"-e ENCRYPT_WITH_PUBLIC_KEY_ID",
		"-e ENDPOINT_URL",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("expected %s, got %s", expected, joined)
		}
	}

	for name, expected := range map[string]string{
		"AWS_ACCESS_KEY_ID":          "key",
		"AWS_SECRET_ACCESS_KEY":      "secret",
		"BUCKET_NAME":                "bucket",
		"BACKUP_NAME":                "redis-lollipop",
		"ENCRYPT_WITH_PUBLIC_KEY_ID": "DEADBEEF",
		"ENDPOINT_URL":               "http://10.0.0.3:9000",
	} {
		if env[name] != expected {
			t.Errorf("expected %s=%s in the environment, got %q", name, expected, env[name])
		}
	}

	if args[len(args)-1] != "dokku/s3backup:0.19.1" {
		t.Errorf("expected the image last, got %s", args[len(args)-1])
	}
}

// Without credentials the backup runs against an instance role, and passing an
// empty pair would look like credentials that are wrong rather than absent.
func TestBackupArgsOmitsCredentialsForAnInstanceRole(t *testing.T) {
	args, env := BackupArgs(BackupArgsInput{
		BucketName: "bucket",
		BackupName: "redis-lollipop",
		Image:      "dokku/s3backup:0.19.1",
	})

	if joined := strings.Join(args, " "); strings.Contains(joined, "AWS_ACCESS_KEY_ID") || strings.Contains(joined, "AWS_SECRET_ACCESS_KEY") {
		t.Errorf("expected no credentials, got %s", joined)
	}

	for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		if _, ok := env[name]; ok {
			t.Errorf("expected no %s in the environment", name)
		}
	}
}

// The argv of a process is readable by every user on the host, so a value put
// there is handed to all of them. Only the names go in it, and docker reads the
// values from its own environment, which only its owner can read.
func TestBackupArgsKeepsValuesOutOfTheArgv(t *testing.T) {
	args, env := BackupArgs(BackupArgsInput{
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI",
		BucketName:      "bucket",
		BackupName:      "redis-lollipop",
		Image:           "dokku/s3backup:0.19.1",
		Keyserver:       "http://10.0.0.2:11371",
		Settings: map[string]string{
			"ENCRYPTION_KEY": "hunter2",
		},
	})

	for _, arg := range args {
		for name, value := range env {
			if strings.Contains(arg, value) {
				t.Errorf("expected the value of %s to stay out of the argv, found it in %q", name, arg)
			}
		}
	}

	if len(env) != 7 {
		t.Errorf("expected every variable to be in the environment, got %v", env)
	}
}

// The settings come out of a Go map, which iterates in a different order every
// run, so the command a service is backed up with would otherwise differ
// between two runs that did the same thing.
func TestBackupArgsIsStable(t *testing.T) {
	input := BackupArgsInput{
		BucketName: "bucket",
		BackupName: "redis-lollipop",
		Image:      "dokku/s3backup:0.19.1",
		Settings: map[string]string{
			"AWS_DEFAULT_REGION":         "us-east-1",
			"AWS_SIGNATURE_VERSION":      "s3v4",
			"ENDPOINT_URL":               "http://10.0.0.3:9000",
			"ENCRYPTION_KEY":             "hunter2",
			"ENCRYPT_WITH_PUBLIC_KEY_ID": "DEADBEEF",
		},
	}

	args, _ := BackupArgs(input)
	first := strings.Join(args, " ")
	for range 20 {
		args, _ := BackupArgs(input)
		if again := strings.Join(args, " "); again != first {
			t.Fatalf("expected the same command every time:\n%s\n%s", first, again)
		}
	}
}

// The dump used to be mounted into the backup container from a temporary
// directory. On a dokku installed in docker, dockerd could not see that
// directory and mounted an empty one in its place, which was shipped as an
// empty backup that reported success. Issue 18 is that backup.
func TestBackupArgsMountsNothing(t *testing.T) {
	args, env := BackupArgs(BackupArgsInput{
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		BucketName:      "bucket",
		BackupName:      "redis-lollipop",
		Image:           "dokku/s3backup:0.19.1",
	})

	for _, arg := range args {
		if arg == "-v" || arg == "--volume" || arg == "--mount" || strings.HasPrefix(arg, "--volume=") || strings.HasPrefix(arg, "--mount=") {
			t.Errorf("expected nothing to be mounted, got %s", strings.Join(args, " "))
		}
	}

	if !slices.Contains(args, "-i") {
		t.Errorf("expected stdin to be kept open for the dump, got %s", strings.Join(args, " "))
	}

	if env["BACKUP_SOURCE"] != "stdin" {
		t.Errorf("expected the image to read the dump from stdin, got %q", env["BACKUP_SOURCE"])
	}
}

// The archive is laid out as the image laid out a mounted /backup, so that a
// backup made before the dump was streamed is restored the same way as one
// made after.
func TestBackupArchiveLayout(t *testing.T) {
	exportFile := filepath.Join(t.TempDir(), "export")
	contents := []byte("a dump, which may be binary\x00\xff")
	if err := os.WriteFile(exportFile, contents, 0600); err != nil {
		t.Fatalf("failed to write the export: %s", err)
	}

	var buffer bytes.Buffer
	if err := backupArchive(&buffer, exportFile); err != nil {
		t.Fatalf("failed to archive the export: %s", err)
	}

	reader := tar.NewReader(&buffer)
	directory, err := reader.Next()
	if err != nil {
		t.Fatalf("failed to read the first entry: %s", err)
	}
	if directory.Name != "backup/" || directory.Typeflag != tar.TypeDir {
		t.Errorf("expected the backup directory first, got %q (%c)", directory.Name, directory.Typeflag)
	}

	export, err := reader.Next()
	if err != nil {
		t.Fatalf("failed to read the second entry: %s", err)
	}
	if export.Name != "backup/export" || export.Typeflag != tar.TypeReg {
		t.Errorf("expected the export second, got %q (%c)", export.Name, export.Typeflag)
	}
	if export.Mode != 0600 {
		t.Errorf("expected the export to be private, got %o", export.Mode)
	}

	actual, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read the export back: %s", err)
	}
	if !bytes.Equal(actual, contents) {
		t.Errorf("expected the export to be archived as it is, got %q", actual)
	}

	if _, err := reader.Next(); err != io.EOF {
		t.Errorf("expected nothing after the export, got %v", err)
	}
}

// The image sizes the parts of its upload by the size it is told to expect,
// and a stream on stdin has no size it can find out for itself. Without one, a
// dump larger than about 78 GiB runs out of parts and fails to upload.
func TestBackupArgsCarriesTheExpectedSizeOnlyWhenKnown(t *testing.T) {
	base := BackupArgsInput{
		BucketName: "bucket",
		BackupName: "redis-lollipop",
		Image:      "dokku/s3backup:0.19.1",
	}

	withSize := base
	withSize.ExpectedSize = 107374182400

	args, env := BackupArgs(withSize)
	if joined := strings.Join(args, " "); !strings.Contains(joined, "-e S3_EXPECTED_SIZE") {
		t.Errorf("expected the size to be passed, got %s", joined)
	}
	if env["S3_EXPECTED_SIZE"] != "107374182400" {
		t.Errorf("expected the size in the environment, got %q", env["S3_EXPECTED_SIZE"])
	}

	args, env = BackupArgs(base)
	if joined := strings.Join(args, " "); strings.Contains(joined, "S3_EXPECTED_SIZE") {
		t.Errorf("expected no size when none is known, got %s", joined)
	}
	if _, ok := env["S3_EXPECTED_SIZE"]; ok {
		t.Errorf("expected no size in the environment when none is known")
	}
}

// An underestimate is what makes a large upload fail, so the estimate has to
// cover the archive the export is actually shipped as, whatever its size
func TestBackupUploadSizeCoversTheArchive(t *testing.T) {
	for _, size := range []int{0, 1, 511, 512, 513, 1048576, 1048577} {
		exportFile := filepath.Join(t.TempDir(), "export")
		if err := os.WriteFile(exportFile, bytes.Repeat([]byte{0xff}, size), 0600); err != nil {
			t.Fatalf("failed to write the export: %s", err)
		}

		var buffer bytes.Buffer
		if err := backupArchive(&buffer, exportFile); err != nil {
			t.Fatalf("failed to archive the export: %s", err)
		}

		archiveSize := int64(buffer.Len())
		expected := backupUploadSize(int64(size))
		if expected < archiveSize+archiveSize/10 {
			t.Errorf("expected the estimate for %d bytes to cover the %d byte archive with room to spare, got %d", size, archiveSize, expected)
		}
	}
}

// A missing export is an error rather than an empty archive, which is what
// the image would otherwise be handed
func TestBackupArchiveRefusesAMissingExport(t *testing.T) {
	var buffer bytes.Buffer
	if err := backupArchive(&buffer, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected a missing export to be an error")
	}

	if buffer.Len() != 0 {
		t.Errorf("expected nothing to be written, got %d bytes", buffer.Len())
	}
}

// fileMode returns the permission bits of a file, failing the test when it
// cannot be read
func fileMode(t *testing.T, filename string) os.FileMode {
	t.Helper()

	info, err := os.Stat(filename)
	if err != nil {
		t.Fatalf("failed to stat %s: %s", filename, err)
	}

	return info.Mode().Perm()
}

// The credentials are what #26 was about: every user on the host could read
// the secret access key the backups are shipped with.
func TestBackupAuthKeepsTheCredentialsPrivate(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	if err := BackupAuth(t.Context(), BackupAuthInput{
		AccessKeyID:      "AKIAEXAMPLE",
		Datastore:        datastore,
		DefaultRegion:    "us-east-1",
		EndpointURL:      "http://10.0.0.3:9000",
		SecretAccessKey:  "wJalrXUtnFEMI",
		ServiceName:      "lollipop",
		SignatureVersion: "s3v4",
	}); err != nil {
		t.Fatalf("failed to store the credentials: %s", err)
	}

	folder := service.Folders(datastore, "lollipop").Backup
	if mode := fileMode(t, folder); mode != BackupFolderMode {
		t.Errorf("expected %s to be %o, got %o", folder, BackupFolderMode, mode)
	}

	for _, name := range []string{accessKeyIDFile, secretAccessKeyFile, defaultRegionFile, signatureVersionFile, endpointURLFile} {
		filename := filepath.Join(folder, name)
		if mode := fileMode(t, filename); mode != service.PrivateFileMode {
			t.Errorf("expected %s to be %o, got %o", name, service.PrivateFileMode, mode)
		}
	}
}

// A service set up by the bash plugins already has a credential file anyone can
// read, and storing new credentials must not leave them in it.
func TestBackupAuthTightensAnExistingCredentialFile(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	folder := service.Folders(datastore, "lollipop").Backup
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatalf("failed to create %s: %s", folder, err)
	}

	filename := filepath.Join(folder, secretAccessKeyFile)
	if err := os.WriteFile(filename, []byte("old"), 0644); err != nil {
		t.Fatalf("failed to write %s: %s", filename, err)
	}
	if err := os.Chmod(filename, 0644); err != nil {
		t.Fatalf("failed to chmod %s: %s", filename, err)
	}

	if err := BackupAuth(t.Context(), BackupAuthInput{
		AccessKeyID:     "AKIAEXAMPLE",
		Datastore:       datastore,
		SecretAccessKey: "new",
		ServiceName:     "lollipop",
	}); err != nil {
		t.Fatalf("failed to store the credentials: %s", err)
	}

	if mode := fileMode(t, filename); mode != service.PrivateFileMode {
		t.Errorf("expected %s to be %o, got %o", filename, service.PrivateFileMode, mode)
	}

	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("failed to read %s: %s", filename, err)
	}
	if string(contents) != "new" {
		t.Errorf("expected the new secret, got %q", contents)
	}

	if mode := fileMode(t, folder); mode != BackupFolderMode {
		t.Errorf("expected %s to be %o, got %o", folder, BackupFolderMode, mode)
	}
}

func TestSetBackupEncryptionKeepsThePassphrasePrivate(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	if err := SetBackupEncryption(t.Context(), datastore, "lollipop", "hunter2"); err != nil {
		t.Fatalf("failed to store the passphrase: %s", err)
	}

	folder := service.Folders(datastore, "lollipop").BackupEncryption
	if mode := fileMode(t, filepath.Join(folder, encryptionKeyFile)); mode != service.PrivateFileMode {
		t.Errorf("expected the passphrase to be %o, got %o", service.PrivateFileMode, mode)
	}

	if mode := fileMode(t, folder); mode != BackupFolderMode {
		t.Errorf("expected %s to be %o, got %o", folder, BackupFolderMode, mode)
	}
}
