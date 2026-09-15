package cron

import (
	"context"
	"strings"
	"testing"
)

func TestSudoersContents(t *testing.T) {
	contents := SudoersContents("redis")

	if contents != "%dokku ALL=(ALL) NOPASSWD:/usr/local/bin/dokku-redis-cron\n" {
		t.Errorf("unexpected sudoers contents: %q", contents)
	}

	// sudo-rs, the default sudo on Ubuntu 25.10 and later, rejects a file with a
	// wildcard or a regular expression in a command argument. A rejected file is
	// not partially applied: the dokku group is left with no privileges at all,
	// which is what broke backup scheduling.
	for _, forbidden := range []string{"*", "^", "$", "[", "\\"} {
		if strings.Contains(contents, forbidden) {
			t.Errorf("%q in a command argument makes the whole file invalid under sudo-rs: %q", forbidden, contents)
		}
	}
}

func TestSudoersNamesOnlyItsOwnHelper(t *testing.T) {
	// one datastore's grant must not cover another's cron files
	if strings.Contains(SudoersContents("redis"), "postgres") {
		t.Error("the redis grant names something other than the redis helper")
	}

	if HelperPath("postgres") == HelperPath("redis") {
		t.Error("two datastores share a helper path")
	}
}

func TestHelperValidatesItsInput(t *testing.T) {
	contents := HelperContents("redis", "/var/lib/dokku/services/redis/.TMP_CRON_FILE")

	// the helper is granted without an argument constraint, because sudo can no
	// longer express one, so it has to apply the rule itself
	if !strings.Contains(contents, `[[ ! "$service" =~ ^[A-Za-z0-9_-]+$ ]]`) {
		t.Errorf("expected the helper to validate the service name:\n%s", contents)
	}

	// the validation has to run in the script, not in a command substitution: a
	// subshell exiting non-zero leaves the caller running with an empty name
	if strings.Contains(contents, `$(cron_file_for`) {
		t.Error("the service name is validated inside a command substitution, where exiting does not stop the script")
	}

	// it moves one fixed path rather than whatever it is handed
	if !strings.Contains(contents, `STAGED_CRON_FILE="/var/lib/dokku/services/redis/.TMP_CRON_FILE"`) {
		t.Errorf("expected the helper to name the staged cron file:\n%s", contents)
	}

	if !strings.Contains(contents, "set -eo pipefail") {
		t.Error("expected the helper to stop on the first failure")
	}
}

func TestHelperFileIsOwnedByRoot(t *testing.T) {
	// the dokku group may run this as root, so being able to rewrite it would be
	// a way to run anything as root
	file := HelperFile("redis", "/tmp/staged")

	if file.Username != "root" || file.GroupName != "root" {
		t.Errorf("expected root:root, got %s:%s", file.Username, file.GroupName)
	}

	if file.Mode != 0755 {
		t.Errorf("expected mode 755, got %o", file.Mode)
	}
}

func TestRefusesAnInvalidServiceName(t *testing.T) {
	// the helper refuses these too, but failing before shelling out means a bad
	// name never reaches a command line at all
	for _, name := range []string{"", "../../../etc/passwd", "foo bar", "foo;touch /tmp/pwned", "foo/../bar", "*"} {
		t.Run(name, func(t *testing.T) {
			if err := Remove(context.Background(), "redis", name); err == nil {
				t.Errorf("expected %q to be refused", name)
			}

			if err := Install(context.Background(), "redis", name); err == nil {
				t.Errorf("expected %q to be refused", name)
			}
		})
	}
}
