package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

func TestCronEntry(t *testing.T) {
	tests := []struct {
		name     string
		input    ScheduleBackupInput
		expected string
	}{
		{
			name:     "a plain schedule",
			input:    ScheduleBackupInput{Schedule: "0 3 * * *", ServiceName: "lollipop", BucketName: "my-bucket"},
			expected: "0 3 * * * dokku /usr/bin/dokku redis:backup lollipop my-bucket",
		},
		{
			name:     "using an iam profile",
			input:    ScheduleBackupInput{Schedule: "@daily", ServiceName: "lollipop", BucketName: "my-bucket", UseIAM: true},
			expected: "@daily dokku /usr/bin/dokku redis:backup lollipop my-bucket --use-iam",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := CronEntry("/usr/bin/dokku", "redis", test.input); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// The schedule has to survive the round trip through the cron file, since that
// file is the only record of what a service was scheduled with.
func TestParseCronEntryReadsBackWhatCronEntryWrote(t *testing.T) {
	tests := []struct {
		name  string
		input ScheduleBackupInput
	}{
		{
			name:  "a plain schedule",
			input: ScheduleBackupInput{Schedule: "0 3 * * *", ServiceName: "lollipop", BucketName: "my-bucket"},
		},
		{
			name:  "a one field schedule",
			input: ScheduleBackupInput{Schedule: "@daily", ServiceName: "lollipop", BucketName: "my-bucket"},
		},
		{
			name:  "using an iam profile",
			input: ScheduleBackupInput{Schedule: "0 3 * * *", ServiceName: "lollipop", BucketName: "my-bucket", UseIAM: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := CronEntry("/usr/bin/dokku", "redis", test.input)

			schedule, ok := ParseCronEntry("redis", entry)
			if !ok {
				t.Fatalf("expected %q to parse", entry)
			}

			if schedule.Schedule != test.input.Schedule {
				t.Errorf("expected the schedule %q, got %q", test.input.Schedule, schedule.Schedule)
			}

			if schedule.BucketName != test.input.BucketName {
				t.Errorf("expected the bucket %q, got %q", test.input.BucketName, schedule.BucketName)
			}

			if schedule.UseIAM != test.input.UseIAM {
				t.Errorf("expected use iam to be %v, got %v", test.input.UseIAM, schedule.UseIAM)
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
		BackupDir:       "/tmp/dump",
		Image:           "dokku/s3backup:0.18.0",
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
		BackupDir:       "/tmp/dump",
		Image:           "dokku/s3backup:0.18.0",
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
		"-v /tmp/dump:/backup",
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

	if args[len(args)-1] != "dokku/s3backup:0.18.0" {
		t.Errorf("expected the image last, got %s", args[len(args)-1])
	}
}

// Without credentials the backup runs against an instance role, and passing an
// empty pair would look like credentials that are wrong rather than absent.
func TestBackupArgsOmitsCredentialsForAnInstanceRole(t *testing.T) {
	args, env := BackupArgs(BackupArgsInput{
		BucketName: "bucket",
		BackupName: "redis-lollipop",
		BackupDir:  "/tmp/dump",
		Image:      "dokku/s3backup:0.18.0",
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
		BackupDir:       "/tmp/dump",
		Image:           "dokku/s3backup:0.18.0",
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

	if len(env) != 6 {
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
		BackupDir:  "/tmp/dump",
		Image:      "dokku/s3backup:0.18.0",
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
