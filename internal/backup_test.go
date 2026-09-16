package internal

import (
	"strings"
	"testing"
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

	joined := strings.Join(BackupArgs(withKeyserver), " ")
	if !strings.Contains(joined, "-e KEYSERVER=http://10.0.0.2:11371") {
		t.Errorf("expected the keyserver to be passed, got %s", joined)
	}

	if joined := strings.Join(BackupArgs(base), " "); strings.Contains(joined, "KEYSERVER") {
		t.Errorf("expected no keyserver when none is set, got %s", joined)
	}
}

// The settings are read from files named after the variables they become, and
// the image is always last because everything after it would be its command.
func TestBackupArgsPassesTheSettingsItIsGiven(t *testing.T) {
	args := BackupArgs(BackupArgsInput{
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
		"-e AWS_ACCESS_KEY_ID=key",
		"-e AWS_SECRET_ACCESS_KEY=secret",
		"-e BUCKET_NAME=bucket",
		"-e BACKUP_NAME=redis-lollipop",
		"-v /tmp/dump:/backup",
		"-e ENCRYPT_WITH_PUBLIC_KEY_ID=DEADBEEF",
		"-e ENDPOINT_URL=http://10.0.0.3:9000",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("expected %s, got %s", expected, joined)
		}
	}

	if args[len(args)-1] != "dokku/s3backup:0.18.0" {
		t.Errorf("expected the image last, got %s", args[len(args)-1])
	}
}

// Without credentials the backup runs against an instance role, and passing an
// empty pair would look like credentials that are wrong rather than absent.
func TestBackupArgsOmitsCredentialsForAnInstanceRole(t *testing.T) {
	args := BackupArgs(BackupArgsInput{
		BucketName: "bucket",
		BackupName: "redis-lollipop",
		BackupDir:  "/tmp/dump",
		Image:      "dokku/s3backup:0.18.0",
	})

	if joined := strings.Join(args, " "); strings.Contains(joined, "AWS_ACCESS_KEY_ID") || strings.Contains(joined, "AWS_SECRET_ACCESS_KEY") {
		t.Errorf("expected no credentials, got %s", joined)
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

	first := strings.Join(BackupArgs(input), " ")
	for range 20 {
		if again := strings.Join(BackupArgs(input), " "); again != first {
			t.Fatalf("expected the same command every time:\n%s\n%s", first, again)
		}
	}
}
