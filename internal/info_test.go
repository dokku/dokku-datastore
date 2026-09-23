package internal

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
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
		"initial-network":             "my-network",
		service.LogDriverProperty:     "json-file",
		service.LogOptProperty:        "max-size=20m,max-file=3",
		service.RestartPolicyProperty: "unless-stopped",
	} {
		if err := SetProperty(datastore, "lollipop", key, value); err != nil {
			t.Fatalf("failed to set the %s property: %s", key, err)
		}
	}

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})

	for key, expected := range map[string]string{
		"backend":         "compose",
		"config-options":  "--appendonly yes",
		"custom-env":      "ONE=1;TWO=2",
		"database-name":   "lollipop_db",
		"definition":      "redis",
		"image":           "redis",
		"image-version":   "8.4.2",
		"initial-network": "my-network",
		"log-driver":      "json-file",
		"log-opt":         "max-size=20m,max-file=3",
		"memory":          "512",
		"restart-policy":  "unless-stopped",
		"service":         "lollipop",
		"shm-size":        "128m",
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

// Backup settings are reported as being present rather than as their values,
// because the credentials and the passphrase are secrets.
func TestInfoReportsBackupStateWithoutItsSecrets(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	for key, expected := range map[string]string{
		"backup-authenticated": "false",
		"backup-bucket":        "",
		"backup-encrypted":     "false",
		"backup-public-key-id": "",
		"backup-schedule":      "",
		"backup-use-iam":       "false",
	} {
		if info[key] != expected {
			t.Errorf("with nothing configured, expected %s to be %q, got %q", key, expected, info[key])
		}
	}

	folders := service.Folders(datastore, "lollipop")
	writeInfoFile(t, filepath.Join(folders.Backup, accessKeyIDFile), "AKIAEXAMPLE")
	writeInfoFile(t, filepath.Join(folders.BackupEncryption, encryptionKeyFile), "a passphrase")
	writeInfoFile(t, filepath.Join(folders.BackupEncryption, publicKeyIDFile), "DEADBEEF")

	info = Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	for key, expected := range map[string]string{
		"backup-authenticated": "true",
		"backup-encrypted":     "true",
		"backup-public-key-id": "DEADBEEF",
	} {
		if info[key] != expected {
			t.Errorf("expected %s to be %q, got %q", key, expected, info[key])
		}
	}

	for _, secret := range []string{"AKIAEXAMPLE", "a passphrase"} {
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
