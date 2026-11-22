package internal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
	"github.com/mitchellh/cli"
)

// backedUpService writes a service of a datastore onto a temporary services
// root with an image, a password, a link, an edited config file, a property and
// a memory limit, so that anything a backup drops shows up as a difference
func backedUpService(t *testing.T, datastore *service.Datastore, serviceName string) {
	t.Helper()

	withDataRoot(t)

	folders := service.Folders(datastore, serviceName)
	files := service.Files(datastore, serviceName)
	if err := os.MkdirAll(folders.Config, 0755); err != nil {
		t.Fatalf("failed to create the service root: %s", err)
	}

	writes := map[string]string{
		files.Image:         "example/image",
		files.ImageVersion:  "1.2.3",
		files.Memory:        "512",
		files.ConfigOptions: "--appendonly yes",
		files.ID:            "0123abcd",
		filepath.Join(folders.Config, "edited.conf"): "edited by an operator\n",
		filepath.Join(folders.Data, "dump.rdb"):      "raw data that must not be copied",
	}
	for _, secret := range datastore.Definition.Dokku.Secrets {
		writes[datastore.SecretFile(serviceName, secret)] = "secret-" + secret.File
	}
	for filename, contents := range writes {
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatalf("failed to create %s: %s", filepath.Dir(filename), err)
		}
		if err := os.WriteFile(filename, []byte(contents), 0600); err != nil {
			t.Fatalf("failed to write %s: %s", filename, err)
		}
	}

	if err := os.Symlink("/etc/passwd", filepath.Join(folders.Config, "escape")); err != nil {
		t.Fatalf("failed to write the symlink: %s", err)
	}

	if err := service.AddLinkedApp(t.Context(), linkedAppsInput(datastore, serviceName), "my-app"); err != nil {
		t.Fatalf("failed to link my-app: %s", err)
	}

	if err := common.PropertyWrite(datastore.Properties().CommandPrefix, serviceName, service.BackupMailtoProperty, "ops@example.com"); err != nil {
		t.Fatalf("failed to write the property: %s", err)
	}
}

func backupServiceInput(datastore *service.Datastore, serviceType string, serviceName string, scopeDir string) BackupServiceInput {
	return BackupServiceInput{
		TriggerInput: triggerInput(datastore),
		ServiceType:  serviceType,
		ServiceName:  serviceName,
		ScopeDir:     scopeDir,
	}
}

// Every datastore is handed every service dokku backs up, so one of another
// type has to be left alone, without even a directory written for it
func TestBackupServiceTriggersIgnoreAnotherDatastore(t *testing.T) {
	withDataRoot(t)
	datastore := service.Datastores["redis"]
	scope := t.TempDir()

	if err := ExportServiceBackup(t.Context(), backupServiceInput(datastore, "postgres", "lollipop", scope)); err != nil {
		t.Errorf("expected the export to ignore a postgres service, got %s", err)
	}

	if err := ImportServiceBackup(t.Context(), backupServiceInput(datastore, "postgres", "lollipop", scope)); err != nil {
		t.Errorf("expected the import to ignore a postgres service, got %s", err)
	}

	entries, err := os.ReadDir(scope)
	if err != nil {
		t.Fatalf("failed to read the scope directory: %s", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected nothing written to the scope directory, got %v", entries)
	}
}

func TestBackupServiceTriggersRequireAServiceAndScope(t *testing.T) {
	withDataRoot(t)
	datastore := service.Datastores["redis"]

	if err := ExportServiceBackup(t.Context(), backupServiceInput(datastore, "redis", "", t.TempDir())); err == nil {
		t.Error("expected the export to refuse a missing service name")
	}

	if err := ImportServiceBackup(t.Context(), backupServiceInput(datastore, "redis", "lollipop", "")); err == nil {
		t.Error("expected the import to refuse a missing scope directory")
	}

	if err := ExportServiceBackup(t.Context(), backupServiceInput(datastore, "redis", "lollipop", t.TempDir())); err == nil {
		t.Error("expected the export to refuse a service that does not exist")
	}
}

func TestServiceRecipe(t *testing.T) {
	recipe, err := ServiceRecipe("redis", "lollipop", []string{"my-app", "other-app"})
	if err != nil {
		t.Fatalf("failed to render the recipe: %s", err)
	}

	expected := `- name: redis service lollipop
  tasks:
    - dokku_service_create:
        type: redis
        service: lollipop
    - dokku_service_link:
        type: redis
        service: lollipop
        app: my-app
    - dokku_service_link:
        type: redis
        service: lollipop
        app: other-app
`
	if string(recipe) != expected {
		t.Errorf("expected the recipe\n%s\ngot\n%s", expected, recipe)
	}
}

func TestServiceRecipeWithoutLinks(t *testing.T) {
	recipe, err := ServiceRecipe("redis", "lollipop", nil)
	if err != nil {
		t.Fatalf("failed to render the recipe: %s", err)
	}

	expected := `- name: redis service lollipop
  tasks:
    - dokku_service_create:
        type: redis
        service: lollipop
`
	if string(recipe) != expected {
		t.Errorf("expected the recipe\n%s\ngot\n%s", expected, recipe)
	}
}

// A datastore that cannot export is still backed up with everything a restore
// needs to make it again, and with no dump
func TestExportServiceBackupWithoutExport(t *testing.T) {
	datastore := service.Datastores["memcached"]
	if datastore.Definition.Implements("export") {
		t.Fatal("expected memcached to have no export")
	}

	backedUpService(t, datastore, "lollipop")
	scope := t.TempDir()

	if err := ExportServiceBackup(t.Context(), backupServiceInput(datastore, "memcached", "lollipop", scope)); err != nil {
		t.Fatalf("failed to export the service: %s", err)
	}

	record, err := readServiceBackupManifest(filepath.Join(scope, "data", ServiceBackupManifest))
	if err != nil {
		t.Fatalf("failed to read the manifest: %s", err)
	}

	if record.Dumped {
		t.Error("expected no dump for a datastore that cannot export")
	}
	if _, err := os.Stat(filepath.Join(scope, "data", serviceBackupDumps)); !os.IsNotExist(err) {
		t.Errorf("expected no dumps directory, got %v", err)
	}

	if record.Type != "memcached" || record.Name != "lollipop" {
		t.Errorf("expected memcached service lollipop, got %s service %s", record.Type, record.Name)
	}
	if record.Image != "example/image" || record.ImageVersion != "1.2.3" {
		t.Errorf("expected example/image:1.2.3, got %s:%s", record.Image, record.ImageVersion)
	}
	if record.Definition != datastore.DefinitionName() {
		t.Errorf("expected the %s definition, got %s", datastore.DefinitionName(), record.Definition)
	}
	if record.Settings.Memory != 512 || record.Settings.ConfigOptions != "--appendonly yes" {
		t.Errorf("expected the memory and config options, got %+v", record.Settings)
	}
	if record.Properties[service.BackupMailtoProperty] != "ops@example.com" {
		t.Errorf("expected the backup-mailto property, got %v", record.Properties)
	}

	recipe, err := os.ReadFile(filepath.Join(scope, "config", "memcached.yml"))
	if err != nil {
		t.Fatalf("failed to read the recipe: %s", err)
	}
	if !strings.Contains(string(recipe), "app: my-app") {
		t.Errorf("expected the recipe to link my-app, got\n%s", recipe)
	}

	root := filepath.Join(scope, "data", serviceBackupSeedRoot)
	for _, name := range []string{"ID", "IMAGE", "MEMORY", "data/dump.rdb", "config/escape"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be left out of the backup, got %v", name, err)
		}
	}
}

// What an export copies out of a service root is what a restore seeds the new
// one with, file for file
func TestSeedRootRoundTrip(t *testing.T) {
	datastore := service.Datastores["redis"]
	backedUpService(t, datastore, "lollipop")

	seed := filepath.Join(t.TempDir(), serviceBackupSeedRoot)
	if err := writeSeedRoot(datastore, "lollipop", seed); err != nil {
		t.Fatalf("failed to write the seed root: %s", err)
	}

	restored := filepath.Join(t.TempDir(), "restored")
	if err := os.MkdirAll(restored, 0755); err != nil {
		t.Fatalf("failed to create the restored root: %s", err)
	}
	if err := CopySeedRoot(seed, restored); err != nil {
		t.Fatalf("failed to copy the seed root: %s", err)
	}

	source := service.Folders(datastore, "lollipop").Root
	expected := []string{"DATABASE_NAME", "LINKS", "config/edited.conf"}
	for _, secret := range datastore.Definition.Dokku.Secrets {
		expected = append(expected, secret.File)
	}
	slices.Sort(expected)

	for _, name := range expected {
		want, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatalf("failed to read %s: %s", name, err)
		}
		got, err := os.ReadFile(filepath.Join(restored, name))
		if err != nil {
			t.Errorf("expected %s to be restored: %s", name, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("expected %s to hold %q, got %q", name, want, got)
		}
	}

	copied := []string{}
	if err := filepath.WalkDir(restored, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, _ := filepath.Rel(restored, path)
		copied = append(copied, relative)
		return nil
	}); err != nil {
		t.Fatalf("failed to walk the restored root: %s", err)
	}
	slices.Sort(copied)
	if !slices.Equal(copied, expected) {
		t.Errorf("expected only %v to be restored, got %v", expected, copied)
	}
}

func TestServiceBackupManifestRoundTrip(t *testing.T) {
	record := ServiceBackup{
		Type:         "postgres",
		Name:         "lollipop",
		Definition:   "postgres-17",
		Image:        "postgres",
		ImageVersion: "17.8",
		Backend:      "compose",
		Settings:     cloneSource(),
		Properties:   map[string]string{"backup-mailto": "ops@example.com", "link-config-keys": "{\"my-app\":\"DATABASE_URL\"}"},
		Ports:        []string{"5432"},
		Users:        map[string]string{"reporting": "hunter2"},
		Databases:    []string{"analytics"},
		Dumped:       true,
	}

	filename := filepath.Join(t.TempDir(), ServiceBackupManifest)
	if err := writeServiceBackupManifest(filename, record); err != nil {
		t.Fatalf("failed to write the manifest: %s", err)
	}

	stat, err := os.Stat(filename)
	if err != nil {
		t.Fatalf("failed to stat the manifest: %s", err)
	}
	if stat.Mode().Perm() != service.PrivateFileMode {
		t.Errorf("expected the manifest to be private, got %s", stat.Mode().Perm())
	}

	actual, err := readServiceBackupManifest(filename)
	if err != nil {
		t.Fatalf("failed to read the manifest: %s", err)
	}
	if !reflect.DeepEqual(actual, record) {
		t.Errorf("expected %+v, got %+v", record, actual)
	}
}

func TestRestorePropertiesWritesEveryProperty(t *testing.T) {
	withDataRoot(t)
	datastore := service.Datastores["redis"]

	properties := map[string]string{
		service.BackupMailtoProperty: "ops@example.com",
		service.LogOptProperty:       "max-size=20m,max-file=3",
	}
	if err := restoreProperties(datastore, "lollipop", properties); err != nil {
		t.Fatalf("failed to restore the properties: %s", err)
	}

	actual, err := common.PropertyGetAll("redis", "lollipop")
	if err != nil {
		t.Fatalf("failed to read the properties: %s", err)
	}
	if !reflect.DeepEqual(actual, properties) {
		t.Errorf("expected %v, got %v", properties, actual)
	}
}

// dokku decides whether to call import at all, so one that is called for a
// service that is there is refused rather than written over
// stubDestroy replaces the destroy a forced restore runs with one that records
// the service it was asked to destroy and fails with errStubDestroy, so a test
// sees the destroy asked for and stops before anything needs docker
func stubDestroy(t *testing.T) *[]string {
	t.Helper()

	destroyed := []string{}
	previous := destroyExistingService
	destroyExistingService = func(ctx context.Context, input DestroyServiceInput) error {
		destroyed = append(destroyed, input.ServiceName)
		return errStubDestroy
	}
	t.Cleanup(func() { destroyExistingService = previous })

	return &destroyed
}

// errStubDestroy is what stubDestroy fails with
var errStubDestroy = errors.New("destroy stubbed out")

func TestImportServiceBackupRefusesAnExistingService(t *testing.T) {
	datastore := service.Datastores["memcached"]
	backedUpService(t, datastore, "lollipop")
	scope := t.TempDir()
	destroyed := stubDestroy(t)

	if err := ExportServiceBackup(t.Context(), backupServiceInput(datastore, "memcached", "lollipop", scope)); err != nil {
		t.Fatalf("failed to export the service: %s", err)
	}

	err := ImportServiceBackup(t.Context(), backupServiceInput(datastore, "memcached", "lollipop", scope))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected the import to refuse an existing service, got %v", err)
	}

	if _, err := os.Stat(service.Files(datastore, "lollipop").ID); err != nil {
		t.Errorf("expected the existing service to be left alone: %s", err)
	}

	if len(*destroyed) != 0 {
		t.Errorf("expected nothing destroyed without --force, got %v", *destroyed)
	}
}

// backup:import --force replaces a service that is there, through the destroy
// that destroy runs, before restoring it
func TestImportServiceBackupForcedDestroysTheExistingService(t *testing.T) {
	datastore := service.Datastores["memcached"]
	backedUpService(t, datastore, "lollipop")
	scope := t.TempDir()
	destroyed := stubDestroy(t)

	if err := ExportServiceBackup(t.Context(), backupServiceInput(datastore, "memcached", "lollipop", scope)); err != nil {
		t.Fatalf("failed to export the service: %s", err)
	}

	input := backupServiceInput(datastore, "memcached", "lollipop", scope)
	input.Force = true
	if err := ImportServiceBackup(t.Context(), input); !errors.Is(err, errStubDestroy) {
		t.Errorf("expected the import to destroy the existing service, got %v", err)
	}

	if !slices.Equal(*destroyed, []string{"lollipop"}) {
		t.Errorf("expected lollipop to be destroyed, got %v", *destroyed)
	}

	// the links are the restored service's to keep, so they are never undone
	assertLinkedApps(t, datastore, "lollipop", []string{"my-app"})
}

func TestBackupForceRequested(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{value: "1", expected: true},
		{value: "", expected: false},
		{value: "0", expected: false},
		{value: "true", expected: false},
	}

	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv(BackupForceEnv, test.value)
			if actual := BackupForceRequested(); actual != test.expected {
				t.Errorf("expected %s=%q to ask for force: %v, got %v", BackupForceEnv, test.value, test.expected, actual)
			}
		})
	}
}

// A stopped service is backed up with everything but its data, and warned
// about, so that one stopped service does not stop the rest of a backup
func TestExportServiceBackupOfAStoppedService(t *testing.T) {
	datastore := service.Datastores["redis"]
	if !datastore.Definition.Implements("export") {
		t.Fatal("expected redis to export")
	}

	backedUpService(t, datastore, "lollipop")
	scope := t.TempDir()

	ui := cli.NewMockUi()
	input := backupServiceInput(datastore, "redis", "lollipop", scope)
	input.Logger = Ui{Ui: ui}

	if err := ExportServiceBackup(t.Context(), input); err != nil {
		t.Fatalf("expected a stopped service to be backed up, got %s", err)
	}

	if !strings.Contains(ui.ErrorWriter.String(), "is not running") {
		t.Errorf("expected a warning that the service is not running, got %q", ui.ErrorWriter.String())
	}

	record, err := readServiceBackupManifest(filepath.Join(scope, "data", ServiceBackupManifest))
	if err != nil {
		t.Fatalf("failed to read the manifest: %s", err)
	}
	if record.Dumped {
		t.Error("expected the manifest to record that no dump was taken")
	}

	if _, err := os.Stat(filepath.Join(scope, "data", serviceBackupDumps)); !os.IsNotExist(err) {
		t.Errorf("expected no dumps directory, got %v", err)
	}

	for _, name := range []string{
		filepath.Join("data", serviceBackupSeedRoot, "LINKS"),
		filepath.Join("data", serviceBackupSeedRoot, "config", "edited.conf"),
		filepath.Join("config", "redis.yml"),
	} {
		if _, err := os.Stat(filepath.Join(scope, name)); err != nil {
			t.Errorf("expected %s to be backed up: %s", name, err)
		}
	}
}

// A backup with no dump restores the service without reading one, even for a
// datastore that imports
func TestRestoreServiceDataSkipsAMissingDump(t *testing.T) {
	withDataRoot(t)
	datastore := service.Datastores["redis"]
	if !datastore.Definition.Implements("import") {
		t.Fatal("expected redis to import")
	}

	input := backupServiceInput(datastore, "redis", "lollipop", t.TempDir())
	if err := restoreServiceData(t.Context(), input, ServiceBackup{Type: "redis", Name: "lollipop"}); err != nil {
		t.Errorf("expected nothing to be imported, got %s", err)
	}
}

func TestImportServiceBackupRefusesAnotherServicesBackup(t *testing.T) {
	datastore := service.Datastores["memcached"]
	backedUpService(t, datastore, "lollipop")
	scope := t.TempDir()

	if err := ExportServiceBackup(t.Context(), backupServiceInput(datastore, "memcached", "lollipop", scope)); err != nil {
		t.Fatalf("failed to export the service: %s", err)
	}

	err := ImportServiceBackup(t.Context(), backupServiceInput(datastore, "memcached", "gobstopper", scope))
	if err == nil || !strings.Contains(err.Error(), "not memcached service gobstopper") {
		t.Errorf("expected the import to refuse a backup of another service, got %v", err)
	}
}

func TestImportServiceBackupRequiresAManifest(t *testing.T) {
	withDataRoot(t)
	datastore := service.Datastores["redis"]

	if err := ImportServiceBackup(t.Context(), backupServiceInput(datastore, "redis", "lollipop", t.TempDir())); err == nil {
		t.Error("expected the import to refuse a scope with no backup in it")
	}
}
