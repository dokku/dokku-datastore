package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/mitchellh/cli"
)

// Scheduled backups no longer need root, so a datastore that ships no
// privileged script grants the dokku group nothing at all
func TestSudoersContentsIsEmptyWithoutAPrivilegedScript(t *testing.T) {
	redis := service.Datastores["redis"]
	if contents := SudoersContents(redis); contents != "" {
		t.Errorf("expected no grants, got:\n%s", contents)
	}

	if len(PrivilegedFiles(redis)) != 0 {
		t.Error("expected redis to ship no privileged script")
	}
}

func TestLegacyCronHelperPath(t *testing.T) {
	if actual := LegacyCronHelperPath(service.Datastores["redis"]); actual != "/usr/local/bin/dokku-redis-cron" {
		t.Errorf("expected /usr/local/bin/dokku-redis-cron, got %s", actual)
	}
}

func TestSudoersFileIsOwnedByRoot(t *testing.T) {
	// sudo ignores a sudoers file that is not owned by root, and a file the
	// dokku user owns would let it grant itself anything
	file := SudoersFile(service.Datastores["redis"])

	if file.Username != "root" || file.GroupName != "root" {
		t.Errorf("expected root:root, got %s:%s", file.Username, file.GroupName)
	}
	if file.Mode != 0440 {
		t.Errorf("expected mode 440, got %o", file.Mode)
	}
	if file.Filename != "/etc/sudoers.d/dokku-redis" {
		t.Errorf("expected /etc/sudoers.d/dokku-redis, got %s", file.Filename)
	}
}

// Graphite is the only datastore that ships a privileged script. Its sudoers
// file grants that script and nothing else, and every line has to keep the
// shape sudo-rs accepts.
func TestSudoersGrantsAPrivilegedScript(t *testing.T) {
	graphite, ok := service.Datastores["graphite"]
	if !ok {
		t.Fatal("expected graphite to be registered")
	}

	contents := SudoersContents(graphite)
	lines := strings.Split(strings.TrimSpace(contents), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one privileged script, got %d lines: %q", len(lines), contents)
	}

	for _, line := range lines {
		if !strings.HasPrefix(line, "%dokku ALL=(ALL) NOPASSWD:") {
			t.Errorf("expected a bare grant, got %q", line)
		}

		// a rule matching arguments is rejected by sudo-rs outright, which
		// leaves the dokku group with no privileges at all rather than fewer
		for _, character := range []string{"*", "^", "$", "[", "\\"} {
			if strings.Contains(line, character) {
				t.Errorf("expected no %s in %q", character, line)
			}
		}
	}

	// scheduled backups no longer go through a helper run as root
	if strings.Contains(contents, "dokku-graphite-cron") {
		t.Error("expected the cron helper not to be granted")
	}

	// sudo-rs, the default sudo on Ubuntu 25.10 and later, rejects a file with a
	// wildcard or a regular expression in a command argument, and a stray
	// wildcard would hand out far more than intended
	if strings.Contains(contents, "ALL=(ALL) NOPASSWD:ALL") {
		t.Error("the sudoers file must not grant unrestricted access")
	}

	if !strings.Contains(contents, "NOPASSWD:/usr/local/bin/dokku-graphite-nginx\n") {
		t.Error("expected the nginx helper to be granted")
	}
}

// The script has to belong to root and live somewhere the dokku user cannot
// write, because the sudoers rule names it with no constraint on its arguments.
func TestPrivilegedFileIsOwnedByRoot(t *testing.T) {
	graphite, ok := service.Datastores["graphite"]
	if !ok {
		t.Fatal("expected graphite to be registered")
	}

	files := PrivilegedFiles(graphite)
	if len(files) != 1 {
		t.Fatalf("expected one privileged script, got %d", len(files))
	}

	file := files[0]
	if file.Filename != "/usr/local/bin/dokku-graphite-nginx" {
		t.Errorf("expected /usr/local/bin/dokku-graphite-nginx, got %s", file.Filename)
	}

	if file.Username != "root" || file.GroupName != "root" {
		t.Errorf("expected root:root, got %s:%s", file.Username, file.GroupName)
	}

	if file.Mode != 0755 {
		t.Errorf("expected 0755, got %o", file.Mode)
	}

	if !strings.HasPrefix(file.Content, "#!/usr/bin/env bash") {
		t.Error("expected a bash script")
	}

	// the grant constrains no argument, so the script has to
	if !strings.Contains(file.Content, `[[ ! "$service" =~ ^[A-Za-z0-9_-]+$ ]]`) {
		t.Error("expected the service name to be validated in the script")
	}
}

// The path is written by Go and invoked by bash, so the two derivations have to
// be pinned against each other rather than each restating a literal.
func TestTheScriptCallsTheHelperTheInstallWrites(t *testing.T) {
	graphite, ok := service.Datastores["graphite"]
	if !ok {
		t.Fatal("expected graphite to be registered")
	}

	files := PrivilegedFiles(graphite)
	if len(files) != 1 {
		t.Fatalf("expected one privileged script, got %d", len(files))
	}

	installed := files[0].Filename
	for _, name := range []string{"nginx-expose", "nginx-unexpose"} {
		script, ok := graphite.Definition.Scripts[name]
		if !ok {
			t.Fatalf("expected graphite to ship %s", name)
		}

		if !strings.Contains(string(script), installed) {
			t.Errorf("expected %s to call %s", name, installed)
		}
	}
}

// A service made by an older plugin keeps whatever modes it was written with,
// so the install is what takes other users off the files holding its secrets.
func TestRestrictServiceSecretsTightensWhatOlderPluginsLeftOpen(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	folders := service.Folders(datastore, "lollipop")
	files := service.Files(datastore, "lollipop")

	for _, folder := range []string{folders.Backup, folders.BackupEncryption} {
		if err := os.MkdirAll(folder, 0775); err != nil {
			t.Fatalf("failed to create %s: %s", folder, err)
		}
		if err := os.Chmod(folder, 0775); err != nil {
			t.Fatalf("failed to chmod %s: %s", folder, err)
		}
	}

	private := []string{
		filepath.Join(folders.Backup, "AWS_ACCESS_KEY_ID"),
		filepath.Join(folders.Backup, "AWS_SECRET_ACCESS_KEY"),
		filepath.Join(folders.BackupEncryption, "ENCRYPTION_KEY"),
		files.Compose,
		files.Env,
		files.ConfigOptions,
		files.Password,
	}
	for _, filename := range append(private, files.Memory) {
		if err := os.WriteFile(filename, []byte("value"), 0644); err != nil {
			t.Fatalf("failed to write %s: %s", filename, err)
		}
		if err := os.Chmod(filename, 0644); err != nil {
			t.Fatalf("failed to chmod %s: %s", filename, err)
		}
	}

	if err := restrictServiceSecrets(datastore, "lollipop"); err != nil {
		t.Fatalf("failed to restrict the service's secrets: %s", err)
	}

	for _, folder := range []string{folders.Backup, folders.BackupEncryption} {
		if mode := fileMode(t, folder); mode != BackupFolderMode {
			t.Errorf("expected %s to be %o, got %o", folder, BackupFolderMode, mode)
		}
	}

	for _, filename := range private {
		if mode := fileMode(t, filename); mode != service.PrivateFileMode {
			t.Errorf("expected %s to be %o, got %o", filename, service.PrivateFileMode, mode)
		}
	}

	// nothing secret is in it, and other tooling may read it
	if mode := fileMode(t, files.Memory); mode != 0644 {
		t.Errorf("expected %s to be left at 644, got %o", files.Memory, mode)
	}
}

// Most services never set up backups, and a service can predate the compose
// file, so none of what is tightened has to be there.
func TestRestrictServiceSecretsToleratesMissingFiles(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	root := service.Folders(datastore, "lollipop").Root
	if err := os.MkdirAll(root, 0775); err != nil {
		t.Fatalf("failed to create %s: %s", root, err)
	}

	if err := restrictServiceSecrets(datastore, "lollipop"); err != nil {
		t.Fatalf("expected nothing to do, got %s", err)
	}

	if _, err := os.Stat(service.Folders(datastore, "lollipop").Backup); !os.IsNotExist(err) {
		t.Errorf("expected no backup folder to be created, got %v", err)
	}
}

// withLegacyCronFile points the legacy cron directory at a temporary one and
// writes a cron file for the service there, the way an earlier version of the
// plugin did
func withLegacyCronFile(t *testing.T, datastore *service.Datastore, serviceName string, contents string) string {
	t.Helper()

	previous := service.LegacyCronDir
	service.LegacyCronDir = t.TempDir()
	t.Cleanup(func() {
		service.LegacyCronDir = previous
	})

	cronFile := service.LegacyCronFile(datastore, serviceName)
	if err := os.WriteFile(cronFile, []byte(contents), 0644); err != nil {
		t.Fatalf("failed to write %s: %s", cronFile, err)
	}

	return cronFile
}

// A schedule an earlier version wrote to the cron directory is moved onto the
// properties the cron-entries trigger reads, so it keeps running from the dokku
// crontab rather than from a file dokku cannot see
func TestMigrateLegacyCronFileMovesAValidSchedule(t *testing.T) {
	datastore := withScheduleService(t, "lollipop")
	cronFile := withLegacyCronFile(t, datastore, "lollipop", "0 3 * * * dokku /usr/bin/dokku redis:backup lollipop my-bucket --use-iam\n")

	ui := cli.NewMockUi()
	changed, err := migrateLegacyCronFile(InstallInput{Datastore: datastore, Logger: Ui{Ui: ui}}, "lollipop")
	if err != nil {
		t.Fatalf("failed to migrate the cron file: %s", err)
	}
	if !changed {
		t.Error("expected the migration to report a change")
	}

	if _, err := os.Stat(cronFile); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed, got %v", cronFile, err)
	}

	schedule, ok := ReadBackupSchedule(datastore, "lollipop")
	expected := BackupSchedule{Schedule: "0 3 * * *", BucketName: "my-bucket", UseIAM: true}
	if !ok || schedule != expected {
		t.Errorf("expected %+v, got %+v", expected, schedule)
	}
}

// "daily" is what issue 6 was scheduled with. Cron never ran it, and carried
// into the dokku crontab it would stop every other task from running too, so it
// is dropped with a warning saying how to schedule it again
func TestMigrateLegacyCronFileDropsAScheduleCronCannotRun(t *testing.T) {
	datastore := withScheduleService(t, "lollipop")
	cronFile := withLegacyCronFile(t, datastore, "lollipop", "daily dokku /usr/bin/dokku redis:backup lollipop my-bucket\n")

	ui := cli.NewMockUi()
	changed, err := migrateLegacyCronFile(InstallInput{Datastore: datastore, Logger: Ui{Ui: ui}}, "lollipop")
	if err != nil {
		t.Fatalf("failed to migrate the cron file: %s", err)
	}
	if !changed {
		t.Error("expected the migration to report a change")
	}

	if _, err := os.Stat(cronFile); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed, got %v", cronFile, err)
	}

	if _, ok := ReadBackupSchedule(datastore, "lollipop"); ok {
		t.Error("expected no schedule to be recorded")
	}

	if warnings := ui.ErrorWriter.String(); !strings.Contains(warnings, "redis:backup-schedule lollipop") {
		t.Errorf("expected a warning saying how to schedule the backup again, got %q", warnings)
	}
}

// A file that is not one the plugin wrote is left where it is, since removing it
// could stop something the plugin does not know about
func TestMigrateLegacyCronFileLeavesAnUnreadableFile(t *testing.T) {
	datastore := withScheduleService(t, "lollipop")
	cronFile := withLegacyCronFile(t, datastore, "lollipop", "0 3 * * * root /usr/local/bin/something-else\n")

	ui := cli.NewMockUi()
	changed, err := migrateLegacyCronFile(InstallInput{Datastore: datastore, Logger: Ui{Ui: ui}}, "lollipop")
	if err != nil {
		t.Fatalf("failed to migrate the cron file: %s", err)
	}
	if changed {
		t.Error("expected nothing to change")
	}

	if _, err := os.Stat(cronFile); err != nil {
		t.Errorf("expected %s to be left in place, got %v", cronFile, err)
	}

	if _, ok := ReadBackupSchedule(datastore, "lollipop"); ok {
		t.Error("expected no schedule to be recorded")
	}
}

// A service that was never scheduled has nothing to migrate
func TestMigrateLegacyCronFileWithNoCronFile(t *testing.T) {
	datastore := withScheduleService(t, "lollipop")

	previous := service.LegacyCronDir
	service.LegacyCronDir = t.TempDir()
	t.Cleanup(func() {
		service.LegacyCronDir = previous
	})

	changed, err := migrateLegacyCronFile(InstallInput{Datastore: datastore, Logger: Ui{Ui: cli.NewMockUi()}}, "lollipop")
	if err != nil {
		t.Fatalf("expected nothing to do, got %s", err)
	}
	if changed {
		t.Error("expected nothing to change")
	}
}
