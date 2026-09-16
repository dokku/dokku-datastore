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
