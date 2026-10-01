package internal

import (
	"slices"
	"strings"
	"testing"
)

// The log is shown the way logs shows a container's: the last lines of it, and
// followed only when asked. A followed log is followed by name, since logrotate
// truncates it in place and a log not yet written has no descriptor to follow.
func TestBackupLogsArgs(t *testing.T) {
	tests := []struct {
		name     string
		num      int
		tail     bool
		expected []string
	}{
		{
			name:     "the most recent lines",
			num:      100,
			expected: []string{"-n", "100", "/var/log/dokku/redis.lollipop.backup.log"},
		},
		{
			name:     "followed",
			num:      100,
			tail:     true,
			expected: []string{"-n", "100", "-F", "/var/log/dokku/redis.lollipop.backup.log"},
		},
		{
			name:     "followed from a count of its own",
			num:      5,
			tail:     true,
			expected: []string{"-n", "5", "-F", "/var/log/dokku/redis.lollipop.backup.log"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := backupLogsArgs("/var/log/dokku/redis.lollipop.backup.log", test.num, test.tail)
			if !slices.Equal(actual, test.expected) {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// A service whose scheduled backups have never run has no log, which is said
// rather than left to tail to report as a missing file
func TestBackupLogsWithNothingLogged(t *testing.T) {
	datastore := withScheduleService(t, "lollipop")
	logsDir := t.TempDir()
	t.Setenv("DOKKU_LOGS_DIR", logsDir)

	err := BackupLogs(t.Context(), BackupLogsInput{Datastore: datastore, ServiceName: "lollipop", Num: 100})
	if err == nil {
		t.Fatal("expected an error with nothing logged")
	}

	if !strings.Contains(err.Error(), "no scheduled backup of lollipop has been logged yet") {
		t.Errorf("expected the error to say nothing was logged, got %q", err)
	}
	if !strings.Contains(err.Error(), logsDir+"/redis.lollipop.backup.log") {
		t.Errorf("expected the error to name the log file, got %q", err)
	}
}
