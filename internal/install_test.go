package internal

import (
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/datastores"
)

func TestSudoersContents(t *testing.T) {
	datastore := datastores.Datastores["redis"]
	contents := SudoersContents(datastore)

	// every line has to be a NOPASSWD grant to the dokku group, and nothing else
	for _, line := range strings.Split(strings.TrimSpace(contents), "\n") {
		if !strings.HasPrefix(line, "%dokku ALL=(ALL) NOPASSWD:") {
			t.Errorf("unexpected sudoers line: %q", line)
		}
	}

	// the cron path has to carry the command prefix, or one datastore's grant
	// would cover another's cron files
	for _, expected := range []string{
		"/bin/rm -f /etc/cron.d/dokku-redis-*",
		`/bin/chown root\:root /etc/cron.d/dokku-redis-*`,
		"/bin/chmod 644 /etc/cron.d/dokku-redis-*",
	} {
		if !strings.Contains(contents, expected) {
			t.Errorf("expected the sudoers file to grant %q", expected)
		}
	}

	// the temporary cron file the schedule command writes lives under the
	// plugin's own data directory, and the grant has to name that exact path
	if !strings.Contains(contents, "/bin/mv /var/lib/dokku/services/redis/.TMP_CRON_FILE /etc/cron.d/dokku-redis-*") {
		t.Errorf("expected the sudoers file to permit moving the temporary cron file, got:\n%s", contents)
	}

	// a stray wildcard here would hand out far more than intended
	if strings.Contains(contents, "ALL=(ALL) NOPASSWD:ALL") {
		t.Error("the sudoers file must not grant unrestricted access")
	}
}

func TestSudoersFileIsOwnedByRoot(t *testing.T) {
	// sudo ignores a sudoers file that is not owned by root, and a file the
	// dokku user owns would let it grant itself anything
	file := SudoersFile(datastores.Datastores["redis"])

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
