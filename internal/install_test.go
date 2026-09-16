package internal

import (
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

func TestSudoersContents(t *testing.T) {
	datastore := service.Datastores["redis"]
	contents := SudoersContents(datastore)

	// every line has to be a NOPASSWD grant to the dokku group, and nothing else
	for _, line := range strings.Split(strings.TrimSpace(contents), "\n") {
		if !strings.HasPrefix(line, "%dokku ALL=(ALL) NOPASSWD:") {
			t.Errorf("unexpected sudoers line: %q", line)
		}
	}

	// the grant names the datastore's own helper, or one datastore's privileges
	// would cover another's cron files
	if !strings.Contains(contents, "NOPASSWD:/usr/local/bin/dokku-redis-cron\n") {
		t.Errorf("expected the sudoers file to grant the redis cron helper, got:\n%s", contents)
	}

	// sudo-rs, the default sudo on Ubuntu 25.10 and later, rejects a file with a
	// wildcard or a regular expression in a command argument, and rejecting one
	// file means the dokku group gets no privileges at all
	for _, forbidden := range []string{"*", "^", "$", "["} {
		if strings.Contains(contents, forbidden) {
			t.Errorf("a command argument pattern (%q) makes the whole file invalid under sudo-rs:\n%s", forbidden, contents)
		}
	}

	// a stray wildcard here would hand out far more than intended
	if strings.Contains(contents, "ALL=(ALL) NOPASSWD:ALL") {
		t.Error("the sudoers file must not grant unrestricted access")
	}
}

func TestCronHelperIsOwnedByRoot(t *testing.T) {
	// the dokku group may run this as root, so being able to rewrite it, or to
	// replace the directory holding it, would be a way to run anything as root
	file := CronHelperFile(service.Datastores["redis"])

	if file.Username != "root" || file.GroupName != "root" {
		t.Errorf("expected root:root, got %s:%s", file.Username, file.GroupName)
	}

	if file.Mode != 0755 {
		t.Errorf("expected mode 755, got %o", file.Mode)
	}

	if file.Filename != "/usr/local/bin/dokku-redis-cron" {
		t.Errorf("expected /usr/local/bin/dokku-redis-cron, got %s", file.Filename)
	}

	if !strings.HasPrefix(file.Content, "#!/usr/bin/env bash\n") {
		t.Error("expected the helper to start with a shebang")
	}

	// the helper is what constrains the service name now that sudo cannot, so it
	// has to apply the same rule the binary validates against
	if !strings.Contains(file.Content, `[[ ! "$service" =~ ^[A-Za-z0-9_-]+$ ]]`) {
		t.Errorf("expected the helper to validate the service name, got:\n%s", file.Content)
	}

	// the staged path is derived from the validated service name, and sits
	// inside the service rather than beside it where listing the services would
	// report it as a service of its own
	if !strings.Contains(file.Content, `DATA_ROOT="/var/lib/dokku/services/redis"`) {
		t.Errorf("expected the helper to name the data root, got:\n%s", file.Content)
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

// The staged path is worked out twice, once in Go to write the file and once in
// bash to move it, from two separately derived roots. They agree today, and if
// they ever stop agreeing every backup-schedule fails with "no cron file staged
// at", so the agreement is pinned by reading the path back out of the generated
// helper rather than restating it.
func TestTheHelperStagesWhereTheBinaryWrites(t *testing.T) {
	datastore := service.Datastores["redis"]

	dataRoot := ""
	for _, line := range strings.Split(CronHelperFile(datastore).Content, "\n") {
		if value, found := strings.CutPrefix(line, "DATA_ROOT="); found {
			dataRoot = strings.Trim(value, `"`)
			break
		}
	}

	if dataRoot == "" {
		t.Fatal("the helper does not set DATA_ROOT")
	}

	expected := dataRoot + "/lollipop/.TMP_CRON_FILE"
	if actual := StagedCronFile(datastore, "lollipop"); actual != expected {
		t.Errorf("the binary writes %s but the helper moves %s", actual, expected)
	}
}

// Graphite is the only datastore that ships a privileged script. Its sudoers
// file has to grant that script as well as the cron helper, and every line has
// to keep the shape sudo-rs accepts.
func TestSudoersGrantsAPrivilegedScript(t *testing.T) {
	graphite, ok := service.Datastores["graphite"]
	if !ok {
		t.Fatal("expected graphite to be registered")
	}

	contents := SudoersContents(graphite)
	lines := strings.Split(strings.TrimSpace(contents), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected the cron helper and one privileged script, got %d lines: %q", len(lines), contents)
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

	if !strings.Contains(contents, "NOPASSWD:/usr/local/bin/dokku-graphite-cron\n") {
		t.Error("expected the cron helper to still be granted")
	}

	if !strings.Contains(contents, "NOPASSWD:/usr/local/bin/dokku-graphite-nginx\n") {
		t.Error("expected the nginx helper to be granted")
	}
}

// A datastore that ships no privileged script is granted exactly what it was
// before, so adding the mechanism widened nothing for the other twenty-one.
func TestSudoersIsUnchangedWithoutAPrivilegedScript(t *testing.T) {
	redis, ok := service.Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	contents := SudoersContents(redis)
	if lines := strings.Split(strings.TrimSpace(contents), "\n"); len(lines) != 1 {
		t.Errorf("expected one grant, got %d: %q", len(lines), contents)
	}

	if len(PrivilegedFiles(redis)) != 0 {
		t.Error("expected redis to ship no privileged script")
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

	// the same validation discipline the cron helper has, since the grant is
	// the same shape
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
