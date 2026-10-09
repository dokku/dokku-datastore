package internal

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	// the log file is named after the logs directory of the host the tests
	// run on, so the default is what they expect
	t.Setenv("DOKKU_LOGS_DIR", "")

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

// A bucket given to a backup follows the s3 general purpose bucket naming rules,
// and anything a shell or the crontab reads specially is refused
func TestValidateBucketName(t *testing.T) {
	tests := []struct {
		bucketName string
		valid      bool
	}{
		{bucketName: "my-bucket", valid: true},
		{bucketName: "my.bucket", valid: true},
		{bucketName: "abc", valid: true},
		{bucketName: strings.Repeat("a", 63), valid: true},
		{bucketName: "my-bucket/with/a/prefix", valid: true},
		{bucketName: "my-bucket/With_A.prefix", valid: true},
		{bucketName: "", valid: false},
		{bucketName: "ab", valid: false},
		{bucketName: strings.Repeat("a", 64), valid: false},
		{bucketName: "My-Bucket", valid: false},
		{bucketName: "my_bucket", valid: false},
		{bucketName: "-my-bucket", valid: false},
		{bucketName: "my-bucket-", valid: false},
		{bucketName: ".my-bucket", valid: false},
		{bucketName: "my-bucket.", valid: false},
		{bucketName: "my..bucket", valid: false},
		{bucketName: "192.168.5.4", valid: false},
		{bucketName: "xn--my-bucket", valid: false},
		{bucketName: "sthree-my-bucket", valid: false},
		{bucketName: "amzn-s3-demo-my-bucket", valid: false},
		{bucketName: "my-bucket-s3alias", valid: false},
		{bucketName: "my-bucket--ol-s3", valid: false},
		{bucketName: "my-bucket.mrap", valid: false},
		{bucketName: "my-bucket--x-s3", valid: false},
		{bucketName: "my-bucket--table-s3", valid: false},
		{bucketName: "s3://my-bucket", valid: false},
		{bucketName: "my-bucket/", valid: false},
		{bucketName: "my-bucket//backups", valid: false},
		{bucketName: "my-bucket/./backups", valid: false},
		{bucketName: "my-bucket/../backups", valid: false},
		{bucketName: "my-bucket/a b", valid: false},
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

// A bucket named with a scheme is told apart from any other invalid name, since
// it is what someone pointing a backup at an s3 compatible service tries first
func TestValidateBucketNameExplainsTheScheme(t *testing.T) {
	err := ValidateBucketName("s3://my-space")
	if err == nil || !strings.Contains(err.Error(), "s3://") || !strings.Contains(err.Error(), "backup-auth") {
		t.Errorf("expected the error to explain the scheme, got %v", err)
	}
}

// A schedule recorded before the s3 naming rules were checked keeps its crontab
// entry, so the backup it runs reports why the bucket is refused rather than
// the schedule disappearing
func TestBackupScheduleValidateKeepsLegacyBucketNames(t *testing.T) {
	schedule := BackupSchedule{Schedule: "@daily", BucketName: "My_Bucket"}
	if err := schedule.Validate(); err != nil {
		t.Errorf("expected %+v to be written into the crontab, got %s", schedule, err)
	}
}

// A schedule with a MAILTO that cannot be written is left out of the crontab
func TestBackupScheduleValidateChecksTheMailto(t *testing.T) {
	schedule := BackupSchedule{Schedule: "@daily", BucketName: "my-bucket", Mailto: "ops@example.com;true"}
	if err := schedule.Validate(); err == nil {
		t.Errorf("expected %+v to be refused", schedule)
	}

	schedule.Mailto = "ops@example.com"
	if err := schedule.Validate(); err != nil {
		t.Errorf("expected %+v to be valid, got %s", schedule, err)
	}
}

func TestCronEntry(t *testing.T) {
	t.Setenv("DOKKU_LOGS_DIR", "")

	tests := []struct {
		name     string
		schedule BackupSchedule
		expected CronTask
		text     string
		json     string
	}{
		{
			name:     "a plain schedule",
			schedule: BackupSchedule{Schedule: "0 3 * * *", BucketName: "my-bucket"},
			expected: CronTask{Schedule: "0 3 * * *", Command: "dokku redis:backup lollipop my-bucket", LogFile: "/var/log/dokku/redis.lollipop.backup.log"},
			text:     "0 3 * * *;dokku redis:backup lollipop my-bucket;/var/log/dokku/redis.lollipop.backup.log",
			json:     `{"schedule":"0 3 * * *","command":"dokku redis:backup lollipop my-bucket","log-file":"/var/log/dokku/redis.lollipop.backup.log"}`,
		},
		{
			name:     "using an iam profile",
			schedule: BackupSchedule{Schedule: "@daily", BucketName: "my-bucket", UseIAM: true},
			expected: CronTask{Schedule: "@daily", Command: "dokku redis:backup lollipop my-bucket --use-iam", LogFile: "/var/log/dokku/redis.lollipop.backup.log"},
			text:     "@daily;dokku redis:backup lollipop my-bucket --use-iam;/var/log/dokku/redis.lollipop.backup.log",
			json:     `{"schedule":"@daily","command":"dokku redis:backup lollipop my-bucket --use-iam","log-file":"/var/log/dokku/redis.lollipop.backup.log"}`,
		},
		{
			name:     "with a mailto",
			schedule: BackupSchedule{Schedule: "@daily", BucketName: "my-bucket", Mailto: "ops@example.com"},
			expected: CronTask{Schedule: "@daily", Command: "dokku redis:backup lollipop my-bucket", LogFile: "/var/log/dokku/redis.lollipop.backup.log", Mailto: "ops@example.com"},
			text:     "@daily;dokku redis:backup lollipop my-bucket;/var/log/dokku/redis.lollipop.backup.log",
			json:     `{"schedule":"@daily","command":"dokku redis:backup lollipop my-bucket","log-file":"/var/log/dokku/redis.lollipop.backup.log","mailto":"ops@example.com"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := CronEntry("redis", "lollipop", test.schedule)
			if task != test.expected {
				t.Errorf("expected %+v, got %+v", test.expected, task)
			}

			// dokku versions that do not read json refuse a line that is not
			// two or three fields, so the mailto is never in the text form
			text := task.Text()
			if text != test.text {
				t.Errorf("expected %q, got %q", test.text, text)
			}
			if fields := strings.Split(text, ";"); len(fields) != 3 {
				t.Errorf("expected three fields, got %d in %q", len(fields), text)
			}

			line, err := task.JSONLine()
			if err != nil {
				t.Fatalf("failed to encode the task: %s", err)
			}
			if line != test.json {
				t.Errorf("expected %q, got %q", test.json, line)
			}
			if strings.Contains(line, "\n") {
				t.Errorf("expected a single line, got %q", line)
			}

			decoded := CronTask{}
			if err := json.Unmarshal([]byte(line), &decoded); err != nil {
				t.Fatalf("failed to decode %q: %s", line, err)
			}
			if decoded != task {
				t.Errorf("expected %q to decode to %+v, got %+v", line, task, decoded)
			}
		})
	}
}

// Each service's backups are logged to a file of their own, so that one
// service's output can be read without every other service's, and the file
// follows dokku's logs directory
func TestBackupLogFile(t *testing.T) {
	t.Setenv("DOKKU_LOGS_DIR", "")
	if actual := BackupLogFile("redis", "lollipop"); actual != "/var/log/dokku/redis.lollipop.backup.log" {
		t.Errorf("expected the default logs directory, got %q", actual)
	}
	if BackupLogFile("redis", "lollipop") == BackupLogFile("redis", "grape") {
		t.Error("expected two services not to share a log file")
	}
	if BackupLogFile("redis", "lollipop") == BackupLogFile("postgres", "lollipop") {
		t.Error("expected two datastores not to share a log file")
	}

	t.Setenv("DOKKU_LOGS_DIR", "/srv/dokku/logs")
	if actual := BackupLogFile("redis", "lollipop"); actual != "/srv/dokku/logs/redis.lollipop.backup.log" {
		t.Errorf("expected the logs directory dokku was given, got %q", actual)
	}
}

// The time a backup starts and ends is printed in utc, so that runs logged by
// hosts in different zones read the same
func TestBackupTimestamp(t *testing.T) {
	zone := time.FixedZone("UTC-5", -5*60*60)
	if actual := BackupTimestamp(time.Date(2026, 9, 29, 22, 0, 0, 0, zone)); actual != "2026-09-30T03:00:00Z" {
		t.Errorf("expected the time in utc, got %q", actual)
	}
}

// What dokku writes into its crontab for a task handed to it with a log file
func TestCrontabLine(t *testing.T) {
	t.Setenv("DOKKU_LOGS_DIR", "")

	tests := []struct {
		name     string
		schedule BackupSchedule
		expected string
	}{
		{
			name:     "a plain schedule",
			schedule: BackupSchedule{Schedule: "0 3 * * *", BucketName: "my-bucket"},
			expected: "0 3 * * * dokku redis:backup lollipop my-bucket &>> /var/log/dokku/redis.lollipop.backup.log",
		},
		{
			name:     "using an iam profile",
			schedule: BackupSchedule{Schedule: "@daily", BucketName: "my-bucket", UseIAM: true},
			expected: "@daily dokku redis:backup lollipop my-bucket --use-iam &>> /var/log/dokku/redis.lollipop.backup.log",
		},
		{
			name:     "with a mailto",
			schedule: BackupSchedule{Schedule: "@daily", BucketName: "my-bucket", Mailto: "ops@example.com"},
			expected: "@daily dokku redis:backup lollipop my-bucket 2>&1 | tee -a /var/log/dokku/redis.lollipop.backup.log",
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
// nothing of the schedule behind
func TestScheduleBackupRoundTrip(t *testing.T) {
	datastore := withScheduleService(t, "lollipop")

	if _, ok := ReadBackupSchedule(datastore, "lollipop"); ok {
		t.Fatal("expected a new service to have no scheduled backup")
	}

	if _, err := BackupScheduleCat(datastore, "lollipop"); err == nil {
		t.Error("expected cat to fail with no scheduled backup")
	}

	// the mailto is a property of the service rather than of the schedule, and
	// is read along with it
	if err := SetProperty(datastore, "lollipop", service.BackupMailtoProperty, "ops@example.com"); err != nil {
		t.Fatalf("failed to set the mailto: %s", err)
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
	expected := BackupSchedule{Schedule: "0 3 * * *", BucketName: "my-bucket", UseIAM: true, Mailto: "ops@example.com"}
	if schedule != expected {
		t.Errorf("expected %+v, got %+v", expected, schedule)
	}

	contents, err := BackupScheduleCat(datastore, "lollipop")
	if err != nil {
		t.Fatalf("failed to cat the schedule: %s", err)
	}
	if contents != "0 3 * * * dokku redis:backup lollipop my-bucket --use-iam 2>&1 | tee -a /var/log/dokku/redis.lollipop.backup.log\n" {
		t.Errorf("unexpected cat output %q", contents)
	}

	report, err := BackupScheduleCatReport(datastore, "lollipop")
	if err != nil {
		t.Fatalf("failed to report the schedule: %s", err)
	}
	expectedReport := BackupScheduleReport{
		Schedule:    "0 3 * * *",
		BucketName:  "my-bucket",
		UseIAM:      true,
		Mailto:      "ops@example.com",
		CrontabLine: strings.TrimSuffix(contents, "\n"),
	}
	if report != expectedReport {
		t.Errorf("expected %+v, got %+v", expectedReport, report)
	}

	// scheduling again without --use-iam drops it rather than keeping the old
	// value, while the mailto the service was set with stays
	if err := ScheduleBackup(t.Context(), ScheduleBackupInput{
		BucketName:  "my-bucket",
		Datastore:   datastore,
		Schedule:    "@daily",
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("failed to schedule the backup again: %s", err)
	}
	if schedule, _ := ReadBackupSchedule(datastore, "lollipop"); schedule.UseIAM || schedule.Mailto != "ops@example.com" || schedule.Schedule != "@daily" {
		t.Errorf("expected the new schedule without iam and with the mailto, got %+v", schedule)
	}

	if err := UnscheduleBackup(t.Context(), UnscheduleBackupInput{Datastore: datastore, ServiceName: "lollipop"}); err != nil {
		t.Fatalf("failed to unschedule the backup: %s", err)
	}

	if _, ok := ReadBackupSchedule(datastore, "lollipop"); ok {
		t.Error("expected the backup to be unscheduled")
	}
	if mailto := service.BackupMailto(datastore, "lollipop"); mailto != "ops@example.com" {
		t.Errorf("expected unscheduling to keep the mailto, got %q", mailto)
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
		{BucketName: "My_Bucket", Schedule: "@daily"},
		{BucketName: "s3://my-bucket", Schedule: "@daily"},
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

// A legacy cron file could only be given a MAILTO by adding a line to it by
// hand, which is kept when the file is migrated
func TestParseLegacyCronFile(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		expected BackupSchedule
	}{
		{
			name:     "a single line",
			contents: "0 3 * * * dokku /usr/bin/dokku redis:backup lollipop my-bucket\n",
			expected: BackupSchedule{Schedule: "0 3 * * *", BucketName: "my-bucket"},
		},
		{
			name:     "a mailto before the entry",
			contents: "MAILTO=ops@example.com\n0 3 * * * dokku /usr/bin/dokku redis:backup lollipop my-bucket --use-iam\n",
			expected: BackupSchedule{Schedule: "0 3 * * *", BucketName: "my-bucket", UseIAM: true, Mailto: "ops@example.com"},
		},
		{
			name:     "a quoted mailto",
			contents: "MAILTO=\"ops@example.com\"\n@daily dokku /usr/bin/dokku redis:backup lollipop my-bucket\n",
			expected: BackupSchedule{Schedule: "@daily", BucketName: "my-bucket", Mailto: "ops@example.com"},
		},
		{
			name:     "comments and other environment lines",
			contents: "# backups\nSHELL=/bin/bash\n\nMAILTO = ops@example.com\nPATH=/usr/bin:/bin\n@daily dokku /usr/bin/dokku redis:backup lollipop my-bucket\n",
			expected: BackupSchedule{Schedule: "@daily", BucketName: "my-bucket", Mailto: "ops@example.com"},
		},
		{
			name:     "a mailto that cannot be written, left to the caller",
			contents: "MAILTO=ops@example.com; true\n@daily dokku /usr/bin/dokku redis:backup lollipop my-bucket\n",
			expected: BackupSchedule{Schedule: "@daily", BucketName: "my-bucket", Mailto: "ops@example.com; true"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schedule, ok := ParseLegacyCronFile("redis", test.contents)
			if !ok {
				t.Fatalf("expected %q to parse", test.contents)
			}

			if schedule != test.expected {
				t.Errorf("expected %+v, got %+v", test.expected, schedule)
			}
		})
	}
}

// A file whose first entry is not one this wrote is not read, whatever follows
func TestParseLegacyCronFileRejectsWhatItDidNotWrite(t *testing.T) {
	for _, contents := range []string{
		"",
		"MAILTO=ops@example.com\n",
		"MAILTO=ops@example.com\n0 3 * * * dokku /usr/bin/dokku postgres:backup lollipop my-bucket\n",
		"0 3 * * * /usr/bin/something-else\n0 3 * * * dokku /usr/bin/dokku redis:backup lollipop my-bucket\n",
	} {
		if _, ok := ParseLegacyCronFile("redis", contents); ok {
			t.Errorf("expected %q not to parse", contents)
		}
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
		Image:           "dokku/s3backup:0.20.0",
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

// The storage class reaches the backup image the same way, and only when a
// service has one: the upload otherwise lands on the bucket's default, and an
// empty value would be handed to the aws cli as a storage class of its own.
func TestBackupArgsCarriesTheStorageClassOnlyWhenSet(t *testing.T) {
	base := BackupArgsInput{
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		BucketName:      "bucket",
		BackupName:      "redis-lollipop",
		Image:           "dokku/s3backup:0.20.0",
	}

	withStorageClass := base
	withStorageClass.StorageClass = "STANDARD_IA"

	args, env := BackupArgs(withStorageClass)
	if joined := strings.Join(args, " "); !strings.Contains(joined, "-e S3_STORAGE_CLASS") {
		t.Errorf("expected the storage class to be passed, got %s", joined)
	}
	if env["S3_STORAGE_CLASS"] != "STANDARD_IA" {
		t.Errorf("expected the storage class in the environment, got %q", env["S3_STORAGE_CLASS"])
	}

	args, env = BackupArgs(base)
	if joined := strings.Join(args, " "); strings.Contains(joined, "S3_STORAGE_CLASS") {
		t.Errorf("expected no storage class when none is set, got %s", joined)
	}
	if _, ok := env["S3_STORAGE_CLASS"]; ok {
		t.Errorf("expected no storage class in the environment when none is set")
	}
}

// issue 17: the timestamp is turned off only when a service asks for it, since
// the image keeps it by default and every backup before this had one
func TestBackupArgsOmitsTheTimestampOnlyWhenAsked(t *testing.T) {
	base := BackupArgsInput{
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		BucketName:      "bucket",
		BackupName:      "db/latest",
		Image:           "dokku/s3backup:0.20.0",
	}

	withoutTimestamp := base
	withoutTimestamp.OmitTimestamp = true

	args, env := BackupArgs(withoutTimestamp)
	if joined := strings.Join(args, " "); !strings.Contains(joined, "-e BACKUP_TIMESTAMP") {
		t.Errorf("expected the timestamp setting to be passed, got %s", joined)
	}
	if env["BACKUP_TIMESTAMP"] != "false" {
		t.Errorf("expected the timestamp to be turned off, got %q", env["BACKUP_TIMESTAMP"])
	}
	if env["BACKUP_NAME"] != "db/latest" {
		t.Errorf("expected the backup to be named db/latest, got %q", env["BACKUP_NAME"])
	}

	args, env = BackupArgs(base)
	if joined := strings.Join(args, " "); strings.Contains(joined, "BACKUP_TIMESTAMP") {
		t.Errorf("expected no timestamp setting by default, got %s", joined)
	}
	if _, ok := env["BACKUP_TIMESTAMP"]; ok {
		t.Errorf("expected no timestamp setting in the environment by default")
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
		Image:           "dokku/s3backup:0.20.0",
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

	if args[len(args)-1] != "dokku/s3backup:0.20.0" {
		t.Errorf("expected the image last, got %s", args[len(args)-1])
	}
}

// Without credentials the backup runs against an instance role, and passing an
// empty pair would look like credentials that are wrong rather than absent.
func TestBackupArgsOmitsCredentialsForAnInstanceRole(t *testing.T) {
	args, env := BackupArgs(BackupArgsInput{
		BucketName: "bucket",
		BackupName: "redis-lollipop",
		Image:      "dokku/s3backup:0.20.0",
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
		Image:           "dokku/s3backup:0.20.0",
		Keyserver:       "http://10.0.0.2:11371",
		StorageClass:    "GLACIER_IR",
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

	if len(env) != 8 {
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
		Image:      "dokku/s3backup:0.20.0",
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
		Image:           "dokku/s3backup:0.20.0",
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
		Image:      "dokku/s3backup:0.20.0",
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

// A setting left out of a later call used to stay in place, so backups kept
// using it and info kept reporting it (#255).
func TestBackupAuthReplacesEarlierSettings(t *testing.T) {
	tests := []struct {
		name     string
		input    BackupAuthInput
		expected map[string]string
	}{
		{
			name:  "no optional settings",
			input: BackupAuthInput{},
			expected: map[string]string{
				defaultRegionFile:    "",
				signatureVersionFile: "",
				endpointURLFile:      "",
			},
		},
		{
			name:  "only a region",
			input: BackupAuthInput{DefaultRegion: "eu-west-1"},
			expected: map[string]string{
				defaultRegionFile:    "eu-west-1",
				signatureVersionFile: "",
				endpointURLFile:      "",
			},
		},
		{
			name: "every optional setting",
			input: BackupAuthInput{
				DefaultRegion:    "eu-west-1",
				EndpointURL:      "http://10.0.0.4:9000",
				SignatureVersion: "s3v2",
			},
			expected: map[string]string{
				defaultRegionFile:    "eu-west-1",
				signatureVersionFile: "s3v2",
				endpointURLFile:      "http://10.0.0.4:9000",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			input := tt.input
			input.AccessKeyID = "AKIANEW"
			input.Datastore = datastore
			input.SecretAccessKey = "newsecret"
			input.ServiceName = "lollipop"
			if err := BackupAuth(t.Context(), input); err != nil {
				t.Fatalf("failed to replace the credentials: %s", err)
			}

			expected := map[string]string{
				accessKeyIDFile:     "AKIANEW",
				secretAccessKeyFile: "newsecret",
			}
			for name, value := range tt.expected {
				expected[name] = value
			}

			folder := service.Folders(datastore, "lollipop").Backup
			for name, value := range expected {
				filename := filepath.Join(folder, name)
				contents, err := os.ReadFile(filename)
				if value == "" {
					if !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("expected %s to be removed, got %q (%v)", name, contents, err)
					}
					continue
				}

				if err != nil {
					t.Fatalf("failed to read %s: %s", filename, err)
				}
				if string(contents) != value {
					t.Errorf("expected %s to be %q, got %q", name, value, contents)
				}
			}
		})
	}
}

// The endpoint of an s3 compatible service is handed to the aws cli, which only
// takes an http or https url
func TestValidateEndpointURL(t *testing.T) {
	tests := []struct {
		endpointURL string
		valid       bool
	}{
		{endpointURL: "", valid: true},
		{endpointURL: "https://nyc3.digitaloceanspaces.com", valid: true},
		{endpointURL: "http://127.0.0.1:9000", valid: true},
		{endpointURL: "https://gw.example.com/s3", valid: true},
		{endpointURL: "nyc3.digitaloceanspaces.com", valid: false},
		{endpointURL: "s3://my-bucket", valid: false},
		{endpointURL: "https://", valid: false},
		{endpointURL: "ftp://example.com", valid: false},
		{endpointURL: "https://a b", valid: false},
	}

	for _, test := range tests {
		t.Run(test.endpointURL, func(t *testing.T) {
			err := ValidateEndpointURL(test.endpointURL)
			if test.valid && err != nil {
				t.Errorf("expected %q to be valid, got %s", test.endpointURL, err)
			}
			if !test.valid && err == nil {
				t.Errorf("expected %q to be refused", test.endpointURL)
			}
		})
	}
}

// A refused endpoint leaves the settings stored earlier as they were
func TestBackupAuthRefusesAnInvalidEndpointURL(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	if err := BackupAuth(t.Context(), BackupAuthInput{
		AccessKeyID:     "AKIAEXAMPLE",
		Datastore:       datastore,
		EndpointURL:     "http://10.0.0.3:9000",
		SecretAccessKey: "wJalrXUtnFEMI",
		ServiceName:     "lollipop",
	}); err != nil {
		t.Fatalf("failed to store the credentials: %s", err)
	}

	if err := BackupAuth(t.Context(), BackupAuthInput{
		AccessKeyID:     "AKIANEW",
		Datastore:       datastore,
		EndpointURL:     "nyc3.digitaloceanspaces.com",
		SecretAccessKey: "newsecret",
		ServiceName:     "lollipop",
	}); err == nil {
		t.Fatal("expected an endpoint url without a scheme to be refused")
	}

	folder := service.Folders(datastore, "lollipop").Backup
	for name, value := range map[string]string{
		accessKeyIDFile:     "AKIAEXAMPLE",
		secretAccessKeyFile: "wJalrXUtnFEMI",
		endpointURLFile:     "http://10.0.0.3:9000",
	} {
		contents, err := os.ReadFile(filepath.Join(folder, name))
		if err != nil {
			t.Fatalf("failed to read %s: %s", name, err)
		}
		if string(contents) != value {
			t.Errorf("expected %s to be kept as %q, got %q", name, value, contents)
		}
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

// A backup holds every database wherever the datastore can export them, and the
// single dump it always made wherever it cannot.
func TestBackupExportCoversEveryDatabase(t *testing.T) {
	tests := map[string]bool{
		"clickhouse": true,
		"couchdb":    true,
		"mariadb":    true,
		"mongo":      true,
		"mysql":      true,
		"postgres":   true,
		"redis":      false,
	}

	for name, expected := range tests {
		t.Run(name, func(t *testing.T) {
			datastore, ok := service.Datastores[name]
			if !ok {
				t.Fatalf("expected %s to be registered", name)
			}

			var dump bytes.Buffer
			input := backupExport(datastore, "lollipop", &dump)
			if input.AllDatabases != expected {
				t.Errorf("expected a backup of every database=%t, got %t", expected, input.AllDatabases)
			}

			if input.ServiceName != "lollipop" || input.Writer != &dump || input.Datastore != datastore {
				t.Errorf("expected the export of lollipop into the backup's file, got %+v", input)
			}

			if len(input.ExtraArgs) != 0 {
				t.Errorf("expected the backup to leave the arguments to the export-args property, got %q", input.ExtraArgs)
			}
		})
	}
}

// withServiceActionPlugin points the plugin path at a temporary directory
// holding a service-action trigger that records its arguments and exits with
// the given code, and puts a plugn on PATH that runs it the way plugn does. It
// returns the file the arguments are recorded in.
func withServiceActionPlugin(t *testing.T, exitCode int) string {
	t.Helper()

	root := t.TempDir()
	recorded := filepath.Join(root, "arguments")
	directory := filepath.Join(root, "enabled", "notify")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatalf("unable to create %s: %s", directory, err)
	}

	script := fmt.Sprintf("#!/usr/bin/env bash\necho \"$*\" >>%q\nexit %d\n", recorded, exitCode)
	if err := os.WriteFile(filepath.Join(directory, "service-action"), []byte(script), 0755); err != nil {
		t.Fatalf("unable to write the service-action trigger: %s", err)
	}

	bin := t.TempDir()
	plugn := "#!/usr/bin/env bash\nshift\ntrigger=\"$1\"\nshift\nfor script in \"$PLUGIN_PATH\"/enabled/*/\"$trigger\"; do\n  \"$script\" \"$@\" || exit $?\ndone\n"
	if err := os.WriteFile(filepath.Join(bin, "plugn"), []byte(plugn), 0755); err != nil {
		t.Fatalf("unable to write plugn: %s", err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PLUGIN_PATH", root)

	return recorded
}

// A plugin is told how each backup ended, so that it can tell something outside
// dokku, whether the backup was scheduled or run by hand
func TestCallPostBackup(t *testing.T) {
	tests := []struct {
		name     string
		bucket   string
		status   string
		expected string
	}{
		{
			name:     "a backup that finished",
			bucket:   "my-bucket",
			status:   BackupStatusSuccess,
			expected: "post-backup redis lollipop my-bucket success\n",
		},
		{
			name:     "a backup that failed",
			bucket:   "my-bucket/redis-backups",
			status:   BackupStatusFailure,
			expected: "post-backup redis lollipop my-bucket/redis-backups failure\n",
		},
		{
			name:     "a backup refused before a bucket was named",
			bucket:   "",
			status:   BackupStatusFailure,
			expected: "post-backup redis lollipop  failure\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorded := withServiceActionPlugin(t, 0)

			err := CallPostBackup(t.Context(), PostBackupInput{
				BucketName:  test.bucket,
				Datastore:   service.Datastores["redis"],
				ServiceName: "lollipop",
				Status:      test.status,
			})
			if err != nil {
				t.Fatalf("CallPostBackup() returned an error: %s", err)
			}

			contents, err := os.ReadFile(recorded)
			if err != nil {
				t.Fatalf("the trigger was not called: %s", err)
			}
			if string(contents) != test.expected {
				t.Errorf("the trigger was handed %q, expected %q", contents, test.expected)
			}
		})
	}
}

// A plugin that fails is reported, so the caller can warn about it
func TestCallPostBackupReportsAFailingTrigger(t *testing.T) {
	withServiceActionPlugin(t, 3)

	err := CallPostBackup(t.Context(), PostBackupInput{
		BucketName:  "my-bucket",
		Datastore:   service.Datastores["redis"],
		ServiceName: "lollipop",
		Status:      BackupStatusSuccess,
	})
	if err == nil {
		t.Fatal("CallPostBackup() returned no error for a failing trigger")
	}
	if !strings.Contains(err.Error(), "service-action post-backup") {
		t.Errorf("the error does not name the trigger: %s", err)
	}
}

// Outside a dokku install there is no plugin to tell
func TestCallPostBackupWithoutAPluginPath(t *testing.T) {
	recorded := withServiceActionPlugin(t, 0)
	t.Setenv("PLUGIN_PATH", "")

	err := CallPostBackup(t.Context(), PostBackupInput{
		BucketName:  "my-bucket",
		Datastore:   service.Datastores["redis"],
		ServiceName: "lollipop",
		Status:      BackupStatusSuccess,
	})
	if err != nil {
		t.Fatalf("CallPostBackup() returned an error: %s", err)
	}
	if _, err := os.Stat(recorded); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the trigger was called without a plugin path")
	}
}
