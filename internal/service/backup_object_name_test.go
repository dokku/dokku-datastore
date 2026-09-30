package service

import (
	"strings"
	"testing"
)

func TestValidateBackupObjectName(t *testing.T) {
	valid := []string{"", "latest", " latest ", "db/latest", "postgres-backups/db_1.daily", strings.Repeat("a", maxBackupObjectNameLength)}
	for _, value := range valid {
		if err := ValidateBackupObjectName(value); err != nil {
			t.Errorf("expected %q to be accepted, got %q", value, err)
		}
	}

	invalid := []string{
		"/latest", "latest/", "db//latest", ".", "..", "db/../latest", "./latest",
		"db latest", "db;latest", "$(id)", "db\nlatest", "db*latest",
		strings.Repeat("a", maxBackupObjectNameLength+1),
	}
	for _, value := range invalid {
		if err := ValidateBackupObjectName(value); err == nil {
			t.Errorf("expected %q to be refused, got no error", value)
		}
	}
}

func TestValidateBackupObjectNameNamesTheProperty(t *testing.T) {
	err := ValidateBackupObjectName("db latest")
	if err == nil {
		t.Fatal("expected an error")
	}

	if !strings.Contains(err.Error(), BackupObjectNameProperty) {
		t.Errorf("expected the error to name %s, got %q", BackupObjectNameProperty, err)
	}
}

func TestValidateBackupTimestamp(t *testing.T) {
	for _, value := range []string{"", "true", "false", " false "} {
		if err := ValidateBackupTimestamp(value); err != nil {
			t.Errorf("expected %q to be accepted, got %q", value, err)
		}
	}

	// the backup image reads the value exactly, so anything it would refuse
	// only once the service had been exported is refused here
	for _, value := range []string{"no", "0", "FALSE", "False", "off"} {
		err := ValidateBackupTimestamp(value)
		if err == nil {
			t.Errorf("expected %q to be refused, got no error", value)
			continue
		}

		if !strings.Contains(err.Error(), BackupTimestampProperty) {
			t.Errorf("expected the error to name %s, got %q", BackupTimestampProperty, err)
		}
	}
}
