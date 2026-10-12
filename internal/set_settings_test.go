package internal

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// readSettingFile returns what a setting was written into, failing the test
// when the file cannot be read
func readSettingFile(t *testing.T, filename string) string {
	t.Helper()

	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("failed to read %s: %s", filename, err)
	}

	return string(contents)
}

// Each property names the info key that reads it back, and a credential is
// only ever read back as a fingerprint.
func TestSettablePropertiesAreReportedSafely(t *testing.T) {
	names := InfoKeyNames()
	for _, property := range SettableProperties {
		reportedBy := property.ReportedBy()
		if !slices.Contains(names, reportedBy) {
			t.Errorf("the %s property is reported by %s, which info does not answer", property.Name, reportedBy)
		}

		if property.Sensitive && !strings.HasSuffix(reportedBy, "-fingerprint") {
			t.Errorf("the %s property is a credential, so it must be reported as a fingerprint rather than by %s", property.Name, reportedBy)
		}

		if property.Description == "" {
			t.Errorf("the %s property has no description for the readme to list", property.Name)
		}
	}
}

// The settings another command owns are refused by name, so that nobody is left
// to work out from the list of valid keys where they went.
func TestSetPropertyRefusesASettingOwnedElsewhere(t *testing.T) {
	datastore := service.Datastores["redis"]
	tests := map[string]string{
		"image":           "upgrade",
		"image-version":   "upgrade",
		"definition":      "upgrade",
		"password":        "kept by the datastore itself",
		"root-password":   "kept by the datastore itself",
		"mounts":          "mount and unmount",
		"backup-schedule": "backup-schedule and backup-unschedule",
	}

	for key, expected := range tests {
		t.Run(key, func(t *testing.T) {
			err := SetProperty(datastore, "lollipop", key, "value")
			if err == nil {
				t.Fatalf("expected %s to be refused, got no error", key)
			}

			if !strings.Contains(err.Error(), key+" cannot be changed with set") || !strings.Contains(err.Error(), expected) {
				t.Errorf("expected the refusal to name %q, got %q", expected, err)
			}
		})
	}
}

// The container settings are written where create writes them, with the same
// modes, and a cleared one is what create writes when it is given none.
func TestSetPropertyWritesTheContainerSettings(t *testing.T) {
	redis := service.Datastores["redis"]
	withInfoService(t, redis, "lollipop")
	files := service.Files(redis, "lollipop")

	tests := []struct {
		key      string
		value    string
		filename string
		written  string
		cleared  string
		mode     os.FileMode
	}{
		{key: service.ConfigOptionsProperty, value: "--maxmemory-policy allkeys-lru", filename: files.ConfigOptions, written: "--maxmemory-policy allkeys-lru", cleared: "", mode: service.PrivateFileMode},
		{key: service.CustomEnvProperty, value: "TZ=UTC;LANG=C.UTF-8", filename: files.Env, written: "TZ=UTC\nLANG=C.UTF-8", cleared: "", mode: service.PrivateFileMode},
		{key: service.MemoryProperty, value: "512", filename: files.Memory, written: "512", cleared: "0", mode: 0644},
		{key: service.ShmSizeProperty, value: "256m", filename: files.ShmSize, written: "256m", cleared: "", mode: 0644},
	}

	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			if err := SetProperty(redis, "lollipop", test.key, test.value); err != nil {
				t.Fatalf("expected %s to be set, got %q", test.key, err)
			}

			if actual := readSettingFile(t, test.filename); actual != test.written {
				t.Errorf("expected %q to be written, got %q", test.written, actual)
			}

			if mode := fileMode(t, test.filename); mode != test.mode {
				t.Errorf("expected %s to be %o, got %o", test.filename, test.mode, mode)
			}

			if err := SetProperty(redis, "lollipop", test.key, ""); err != nil {
				t.Fatalf("expected %s to be cleared, got %q", test.key, err)
			}

			if actual := readSettingFile(t, test.filename); actual != test.cleared {
				t.Errorf("expected %q once cleared, got %q", test.cleared, actual)
			}
		})
	}

	// read back by info the way create's flags take it
	if err := SetProperty(redis, "lollipop", service.CustomEnvProperty, "TZ=UTC;LANG=C.UTF-8"); err != nil {
		t.Fatalf("expected the environment to be set, got %q", err)
	}
	info := Info(t.Context(), InfoInput{Datastore: redis, ServiceName: "lollipop"})
	if actual := info[service.CustomEnvProperty]; actual != "TZ=UTC;LANG=C.UTF-8" {
		t.Errorf("expected info to report the environment as it was set, got %q", actual)
	}
}

// Each backup setting is written into the file its backup command writes, as
// privately, and reads back the way the backup command's own would.
func TestSetPropertyWritesTheBackupSettings(t *testing.T) {
	redis := service.Datastores["redis"]
	withInfoService(t, redis, "lollipop")
	folders := service.Folders(redis, "lollipop")

	tests := []struct {
		key      string
		value    string
		filename string
	}{
		{key: backupAccessKeyIDProperty, value: "AKIAEXAMPLE", filename: filepath.Join(folders.Backup, accessKeyIDFile)},
		{key: backupSecretAccessKeyProperty, value: "wJalrXUtnFEMI", filename: filepath.Join(folders.Backup, secretAccessKeyFile)},
		{key: "backup-default-region", value: "us-east-1", filename: filepath.Join(folders.Backup, defaultRegionFile)},
		{key: "backup-signature-version", value: "s3v4", filename: filepath.Join(folders.Backup, signatureVersionFile)},
		{key: "backup-endpoint-url", value: "https://nyc3.digitaloceanspaces.com", filename: filepath.Join(folders.Backup, endpointURLFile)},
		{key: backupEncryptionPassphraseProperty, value: "hunter2", filename: filepath.Join(folders.BackupEncryption, encryptionKeyFile)},
		{key: "backup-public-key-id", value: "0xDEADBEEF", filename: filepath.Join(folders.BackupEncryption, publicKeyIDFile)},
	}

	for _, test := range tests {
		if err := SetProperty(redis, "lollipop", test.key, test.value); err != nil {
			t.Fatalf("expected %s to be set, got %q", test.key, err)
		}

		if actual := readSettingFile(t, test.filename); actual != test.value {
			t.Errorf("expected %s to hold %q, got %q", test.key, test.value, actual)
		}

		if mode := fileMode(t, test.filename); mode != service.PrivateFileMode {
			t.Errorf("expected %s to be %o, got %o", test.filename, service.PrivateFileMode, mode)
		}
	}

	for _, folder := range []string{folders.Backup, folders.BackupEncryption} {
		if mode := fileMode(t, folder); mode != BackupFolderMode {
			t.Errorf("expected %s to be %o, got %o", folder, BackupFolderMode, mode)
		}
	}

	// the pair set one at a time is the pair backup-auth would have stored
	info := Info(t.Context(), InfoInput{Datastore: redis, ServiceName: "lollipop"})
	if info["backup-authenticated"] != "true" {
		t.Errorf("expected the service to be authenticated, got %q", info["backup-authenticated"])
	}
	for key, expected := range map[string]string{
		"backup-access-key-id-fingerprint":     backupFingerprint("AKIAEXAMPLE"),
		"backup-secret-access-key-fingerprint": backupFingerprint("wJalrXUtnFEMI"),
		"backup-auth-fingerprint":              backupFingerprint("AKIAEXAMPLE", "wJalrXUtnFEMI"),
	} {
		if info[key] != expected {
			t.Errorf("expected %s to be %q, got %q", key, expected, info[key])
		}
	}
	if info["backup-encrypted"] != "true" {
		t.Errorf("expected the backups to be encrypted, got %q", info["backup-encrypted"])
	}

	// clearing removes the file, and clearing it again is not an error
	for _, test := range tests {
		for range 2 {
			if err := SetProperty(redis, "lollipop", test.key, ""); err != nil {
				t.Fatalf("expected %s to be cleared, got %q", test.key, err)
			}
		}

		if common.FileExists(test.filename) {
			t.Errorf("expected %s to be removed", test.filename)
		}
	}
}

// A credential that does not fit its file is refused without being repeated,
// since the error is printed wherever the command's output goes.
func TestSetPropertyNeverRepeatsACredential(t *testing.T) {
	redis := service.Datastores["redis"]
	withInfoService(t, redis, "lollipop")

	for _, property := range SettableProperties {
		if !property.Sensitive {
			continue
		}

		err := SetProperty(redis, "lollipop", property.Name, "hunter2\nhunter3")
		if err == nil {
			t.Fatalf("expected a %s over two lines to be refused", property.Name)
		}

		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("expected the refusal of %s to leave the value out, got %q", property.Name, err)
		}
	}
}

// A backup setting means nothing for a datastore that cannot be backed up, so it
// is refused the way the backup command it stands in for is. Clearing one is
// always allowed.
func TestSetPropertyRefusesABackupSettingWithoutBackups(t *testing.T) {
	memcached := service.Datastores["memcached"]
	withInfoService(t, memcached, "lollipop")

	err := SetProperty(memcached, "lollipop", backupSecretAccessKeyProperty, "wJalrXUtnFEMI")
	if err == nil {
		t.Fatal("expected memcached to refuse a backup credential")
	}

	if !strings.Contains(err.Error(), "the memcached datastore does not support backups") {
		t.Errorf("expected the refusal to say why, got %q", err)
	}

	if err := SetProperty(memcached, "lollipop", backupSecretAccessKeyProperty, ""); err != nil {
		t.Errorf("expected clearing it to be allowed, got %q", err)
	}
}

// The bucket and the instance role belong to a scheduled backup, so they are
// refused for a service without one, and the bucket one has cannot be taken
// away from under its crontab entry.
func TestSetPropertyScheduleSettings(t *testing.T) {
	redis := service.Datastores["redis"]
	withInfoService(t, redis, "lollipop")

	for _, key := range []string{BackupBucketProperty, BackupUseIAMProperty} {
		value := "other-bucket"
		if key == BackupUseIAMProperty {
			value = "true"
		}

		err := SetProperty(redis, "lollipop", key, value)
		if err == nil || !strings.Contains(err.Error(), "There is no scheduled backup for lollipop") {
			t.Errorf("expected %s to be refused without a schedule, got %v", key, err)
		}

		if err := SetProperty(redis, "lollipop", key, ""); err != nil {
			t.Errorf("expected clearing %s without a schedule to be allowed, got %q", key, err)
		}
	}

	if err := writeBackupSchedule(redis, "lollipop", BackupSchedule{Schedule: "@daily", BucketName: "my-bucket"}); err != nil {
		t.Fatalf("failed to schedule the backup: %s", err)
	}

	if err := SetProperty(redis, "lollipop", BackupBucketProperty, "other-bucket"); err != nil {
		t.Fatalf("expected the bucket to be changed, got %q", err)
	}
	if err := SetProperty(redis, "lollipop", BackupUseIAMProperty, "true"); err != nil {
		t.Fatalf("expected the instance role to be set, got %q", err)
	}

	schedule, _ := ReadBackupSchedule(redis, "lollipop")
	if schedule.BucketName != "other-bucket" || !schedule.UseIAM {
		t.Errorf("expected the schedule to ship to other-bucket with an instance role, got %+v", schedule)
	}

	err := SetProperty(redis, "lollipop", BackupBucketProperty, "")
	if err == nil || !strings.Contains(err.Error(), "backup-unschedule") {
		t.Errorf("expected clearing the bucket of a scheduled backup to be refused, got %v", err)
	}

	// false is how backup-schedule records no instance role: not at all
	if err := SetProperty(redis, "lollipop", BackupUseIAMProperty, "false"); err != nil {
		t.Fatalf("expected the instance role to be unset, got %q", err)
	}
	if actual := common.PropertyGet(redis.Properties().CommandPrefix, "lollipop", BackupUseIAMProperty); actual != "" {
		t.Errorf("expected the property to be removed, got %q", actual)
	}
}

// The warning names the commands that make the change take, and there is none
// for a property read each time it is used.
func TestSettablePropertyWarning(t *testing.T) {
	tests := map[string]string{
		service.MemoryProperty:          "memory takes effect the next time the container is built: run `dokku redis:stop lollipop` then `dokku redis:start lollipop`",
		service.PortBindAddressProperty: "port-bind-address takes effect on an exposed service once it is reexposed: run `dokku redis:reexpose lollipop`",
		service.WaitTimeoutProperty:     "",
		backupSecretAccessKeyProperty:   "",
	}

	for key, expected := range tests {
		property, ok := LookupSettableProperty(key)
		if !ok {
			t.Fatalf("expected %s to be settable", key)
		}

		if actual := property.Warning("redis", "lollipop"); actual != expected {
			t.Errorf("expected the %s warning to be %q, got %q", key, expected, actual)
		}
	}
}

// A shared memory size docker would refuse is refused by create as well, so set
// accepts no less than create does.
func TestCreateRefusesAnUnusableShmSize(t *testing.T) {
	withDataRoot(t)

	err := CreateService(t.Context(), CreateServiceInput{
		Datastore:   service.Datastores["redis"],
		ServiceName: "lollipop",
		ShmSize:     "lots",
	})
	if err == nil || !strings.Contains(err.Error(), `invalid shm-size value "lots"`) {
		t.Errorf("expected the shared memory size to be refused, got %v", err)
	}
}
