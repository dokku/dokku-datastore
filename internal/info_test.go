package internal

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// withInfoService points the package at a temporary root and creates a service
// in it, since info is only ever taken of a service that exists
func withInfoService(t *testing.T, datastore *service.Datastore, serviceName string) {
	t.Helper()

	withDataRoot(t)

	// the property reads info makes go through the environment rather than the
	// package variable withDataRoot swaps
	t.Setenv("DOKKU_LIB_ROOT", service.DokkuLibRoot)

	folders := service.Folders(datastore, serviceName)
	if err := os.MkdirAll(folders.Root, 0755); err != nil {
		t.Fatalf("failed to create the service root: %s", err)
	}
}

// writeInfoFile writes one of the files a service records its state in
func writeInfoFile(t *testing.T, filename string, contents string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatalf("failed to create %s: %s", filepath.Dir(filename), err)
	}

	if err := os.WriteFile(filename, []byte(contents), 0644); err != nil {
		t.Fatalf("failed to write %s: %s", filename, err)
	}
}

// The gap this closed: a property could be set and then never read back, so
// nothing could reconstruct what a service had been configured with.
func TestInfoCoversEverySettableProperty(t *testing.T) {
	names := InfoKeyNames()
	for _, property := range SettableProperties {
		if !slices.Contains(names, property) {
			t.Errorf("the %s property is settable but info does not report it", property)
		}
	}
}

// The other half of the same gap: info used to report config-options without
// accepting --config-options, so the value existed but could not be selected.
// The flags are generated from InfoKeys, so a key missing from it is a key with
// no flag.
func TestInfoKeysMatchWhatInfoAnswers(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	names := InfoKeyNames()

	for _, name := range names {
		if _, ok := info[name]; !ok {
			t.Errorf("the %s key is declared but never answered, so its flag reports nothing", name)
		}
	}

	for name := range info {
		if !slices.Contains(names, name) {
			t.Errorf("the %s key is answered but not declared, so no flag selects it", name)
		}
	}
}

// Info builds on service.Info, and this is what says so if it stops doing that.
func TestInfoCoversEveryServiceInfoKey(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	names := InfoKeyNames()
	for key := range service.Info(context.Background(), service.InfoInput{Datastore: datastore, ServiceName: "lollipop"}) {
		if !slices.Contains(names, key) {
			t.Errorf("service.Info answers %s, which info does not report", key)
		}
	}
}

// Every key becomes a flag, so a key without a description is an undocumented
// flag and a key declared twice is a flag declared twice, which panics.
func TestInfoKeysAreDocumented(t *testing.T) {
	seen := map[string]bool{}
	for _, key := range InfoKeys {
		if strings.TrimSpace(key.Description) == "" {
			t.Errorf("the %s key has no description", key.Name)
		}

		if seen[key.Name] {
			t.Errorf("the %s key is declared twice", key.Name)
		}
		seen[key.Name] = true
	}
}

func TestInfoReadsTheRecordedState(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	serviceFiles := service.Files(datastore, "lollipop")
	for filename, contents := range map[string]string{
		serviceFiles.Backend:       "compose",
		serviceFiles.DatabaseName:  "lollipop_db",
		serviceFiles.Definition:    "redis",
		serviceFiles.Env:           "ONE=1\nTWO=2",
		serviceFiles.Image:         "redis",
		serviceFiles.ImageVersion:  "8.4.2",
		serviceFiles.Memory:        "512",
		serviceFiles.ShmSize:       "128m",
		serviceFiles.ConfigOptions: "--appendonly yes",
	} {
		writeInfoFile(t, filename, contents)
	}

	for key, value := range map[string]string{
		"initial-network":               "my-network",
		service.LogDriverProperty:       "json-file",
		service.LogOptProperty:          "max-size=20m,max-file=3",
		service.RestartPolicyProperty:   "unless-stopped",
		service.WaitTimeoutProperty:     "120",
		service.PortBindAddressProperty: "10.0.0.5",
		service.PortSourceRangeProperty: "10.0.0.0/8",
		service.ExposeHostProperty:      "db.example.com",
		service.VolumeTargetsProperty:   "data=/redis-data",
	} {
		if err := SetProperty(datastore, "lollipop", key, value); err != nil {
			t.Fatalf("failed to set the %s property: %s", key, err)
		}
	}

	// written directly, since set refuses them for redis, whose export and
	// import ignore them: what is read back is what is under test
	for key, value := range map[string]string{
		service.ExportArgsProperty: "--hex-blob",
		service.ImportArgsProperty: "--force",
	} {
		if err := common.PropertyWrite(datastore.Properties().CommandPrefix, "lollipop", key, value); err != nil {
			t.Fatalf("failed to write the %s property: %s", key, err)
		}
	}

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})

	for key, expected := range map[string]string{
		"backend":           "compose",
		"config-options":    "--appendonly yes",
		"custom-env":        "ONE=1;TWO=2",
		"database-name":     "lollipop_db",
		"definition":        "redis",
		"export-args":       "--hex-blob",
		"port-bind-address": "10.0.0.5",
		"port-source-range": "10.0.0.0/8",
		"expose-host":       "db.example.com",
		"image":             "redis",
		"image-version":     "8.4.2",
		"import-args":       "--force",
		"initial-network":   "my-network",
		"log-driver":        "json-file",
		"log-opt":           "max-size=20m,max-file=3",
		"memory":            "512",
		"restart-policy":    "unless-stopped",
		"service":           "lollipop",
		"shm-size":          "128m",
		"wait-timeout":      "120",
		"volume-targets":    "data=/redis-data",
	} {
		if info[key] != expected {
			t.Errorf("expected %s to be %q, got %q", key, expected, info[key])
		}
	}
}

// A property that was unset reads the same way as one that was never set, since
// unsetting deletes the file rather than emptying it.
func TestInfoReportsAnUnsetPropertyAsEmpty(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	if err := SetProperty(datastore, "lollipop", service.KeyserverProperty, "keys.example.com"); err != nil {
		t.Fatalf("failed to set the property: %s", err)
	}

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	if info[service.KeyserverProperty] != "keys.example.com" {
		t.Errorf("expected the keyserver to be reported, got %q", info[service.KeyserverProperty])
	}

	if err := SetProperty(datastore, "lollipop", service.KeyserverProperty, ""); err != nil {
		t.Fatalf("failed to unset the property: %s", err)
	}

	info = Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	if info[service.KeyserverProperty] != "" {
		t.Errorf("expected an unset keyserver to report empty, got %q", info[service.KeyserverProperty])
	}
}

// The storage class is reported as it was set, so tooling can tell which class
// the next backup will be uploaded with, and as empty once it is cleared.
func TestInfoReportsTheBackupStorageClass(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	if err := SetProperty(datastore, "lollipop", service.BackupStorageClassProperty, "STANDARD_IA"); err != nil {
		t.Fatalf("failed to set the property: %s", err)
	}

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	if info[service.BackupStorageClassProperty] != "STANDARD_IA" {
		t.Errorf("expected the storage class to be reported, got %q", info[service.BackupStorageClassProperty])
	}

	if err := SetProperty(datastore, "lollipop", service.BackupStorageClassProperty, ""); err != nil {
		t.Fatalf("failed to unset the property: %s", err)
	}

	info = Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	if info[service.BackupStorageClassProperty] != "" {
		t.Errorf("expected an unset storage class to report empty, got %q", info[service.BackupStorageClassProperty])
	}
}

// Backup settings are reported as being present and as a fingerprint rather
// than as their values, because the credentials and the passphrase are secrets.
func TestInfoReportsBackupStateWithoutItsSecrets(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	for key, expected := range map[string]string{
		"backup-auth-fingerprint":       "",
		"backup-authenticated":          "false",
		"backup-bucket":                 "",
		"backup-default-region":         "",
		"backup-encrypted":              "false",
		"backup-encryption-fingerprint": "",
		"backup-endpoint-url":           "",
		"backup-public-key-id":          "",
		"backup-schedule":               "",
		"backup-signature-version":      "",
		"backup-storage-class":          "",
		"backup-use-iam":                "false",
	} {
		if info[key] != expected {
			t.Errorf("with nothing configured, expected %s to be %q, got %q", key, expected, info[key])
		}
	}

	// half a pair is not something a backup can authenticate with
	folders := service.Folders(datastore, "lollipop")
	writeInfoFile(t, filepath.Join(folders.Backup, accessKeyIDFile), "AKIAEXAMPLE")

	info = Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	for key, expected := range map[string]string{
		"backup-auth-fingerprint": "",
		"backup-authenticated":    "false",
	} {
		if info[key] != expected {
			t.Errorf("with only an access key id stored, expected %s to be %q, got %q", key, expected, info[key])
		}
	}

	writeInfoFile(t, filepath.Join(folders.Backup, secretAccessKeyFile), "wJalrXUtnFEMI")
	writeInfoFile(t, filepath.Join(folders.Backup, defaultRegionFile), "us-east-1")
	writeInfoFile(t, filepath.Join(folders.Backup, signatureVersionFile), "s3v4")
	writeInfoFile(t, filepath.Join(folders.Backup, endpointURLFile), "http://127.0.0.1:9000")
	// the way the bash plugins wrote it, with echo, which a backup reads past
	writeInfoFile(t, filepath.Join(folders.BackupEncryption, encryptionKeyFile), "a passphrase\n")
	writeInfoFile(t, filepath.Join(folders.BackupEncryption, publicKeyIDFile), "DEADBEEF")

	// the digests are what sha256sum prints, rather than recomputed here, so
	// the format tooling compares against cannot drift unnoticed:
	//   printf '%s\n%s' AKIAEXAMPLE wJalrXUtnFEMI | sha256sum
	//   printf '%s' 'a passphrase' | sha256sum
	info = Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	for key, expected := range map[string]string{
		"backup-auth-fingerprint":       "405216607097c7a8e61ea3d1c6df1c8352cbf9c956da9d3a0e64657f7aa9dcd7",
		"backup-authenticated":          "true",
		"backup-default-region":         "us-east-1",
		"backup-encrypted":              "true",
		"backup-encryption-fingerprint": "33edb1c3746c802e8e12876b4a0ab6d2dbeaf56af9e4d90e51fa649e67230256",
		"backup-endpoint-url":           "http://127.0.0.1:9000",
		"backup-public-key-id":          "DEADBEEF",
		"backup-signature-version":      "s3v4",
	} {
		if info[key] != expected {
			t.Errorf("expected %s to be %q, got %q", key, expected, info[key])
		}
	}

	for _, secret := range []string{"AKIAEXAMPLE", "wJalrXUtnFEMI", "a passphrase"} {
		for key, value := range info {
			if strings.Contains(value, secret) {
				t.Errorf("the %s key leaks a stored secret: %q", key, value)
			}
		}
	}
}

// Info on a service whose container is gone answers what it can rather than
// failing, which is the state a stopped service is always in.
func TestInfoOnAServiceWithNoContainer(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "gone")

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "gone"})

	if info["id"] != "" {
		t.Errorf("expected no container id, got %q", info["id"])
	}

	if info["service-root"] == "" {
		t.Error("expected the service root to be reported")
	}
}

// What was set rather than what the container is made with: a service that
// names no restart policy reports none, and the readme says what it falls back
// to. An export that reads this back and sets it again leaves the service on
// the default rather than pinning it there.
func TestInfoReportsAnUnsetRestartPolicyAsEmpty(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	if policy := info[service.RestartPolicyProperty]; policy != "" {
		t.Errorf("expected an unset restart policy to be reported empty, got %q", policy)
	}
}

// The same for the wait timeout: a service that names none reports none, even
// when the host or the definition would wait for longer than the default.
func TestInfoReportsAnUnsetWaitTimeoutAsEmpty(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")
	t.Setenv(datastore.Properties().WaitTimeoutVariable, "90")

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	if timeout := info[service.WaitTimeoutProperty]; timeout != "" {
		t.Errorf("expected an unset wait timeout to be reported empty, got %q", timeout)
	}
}

// The expose-host is reported as it was set, like every other property, rather
// than as the global domain the exposed dsn falls back to without one, so a
// value read back and set again does not pin the service to today's domain.
func TestInfoReportsAnUnsetExposeHostAsEmpty(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	dokkuRoot := t.TempDir()
	t.Setenv("DOKKU_ROOT", dokkuRoot)
	writeInfoFile(t, filepath.Join(dokkuRoot, "VHOST"), "dokku.me")
	writeInfoFile(t, service.Files(datastore, "lollipop").Port, "33201")

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	if host := info[service.ExposeHostProperty]; host != "" {
		t.Errorf("expected an unset expose-host to be reported empty, got %q", host)
	}

	if dsn := info["exposed-dsn"]; !strings.Contains(dsn, "@dokku.me:33201") {
		t.Errorf("expected the exposed dsn to name the global domain, got %q", dsn)
	}
}

// A service that is not exposed has no dsn a client off the host could use,
// even with a host to name.
func TestInfoReportsNoExposedDsnForAServiceThatIsNotExposed(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	dokkuRoot := t.TempDir()
	t.Setenv("DOKKU_ROOT", dokkuRoot)
	writeInfoFile(t, filepath.Join(dokkuRoot, "VHOST"), "dokku.me")

	if err := SetProperty(datastore, "lollipop", service.ExposeHostProperty, "db.example.com"); err != nil {
		t.Fatalf("failed to set the property: %s", err)
	}

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	if dsn := info["exposed-dsn"]; dsn != "" {
		t.Errorf("expected no exposed dsn for a service that is not exposed, got %q", dsn)
	}
}
