package service

import (
	"strings"
	"testing"
)

// The recipients are written into a MAILTO line in the dokku crontab, so
// anything that would end that line is refused
func TestValidateBackupMailto(t *testing.T) {
	valid := []string{"", " ", "ops@example.com", " ops@example.com ", "ops+backups@mail.example.com", "ops@example.com,dba@example.com", "root"}
	for _, value := range valid {
		if err := ValidateBackupMailto(value); err != nil {
			t.Errorf("expected %q to be accepted, got %q", value, err)
		}
	}

	invalid := []string{
		"ops@example.com, dba@example.com", "ops@example.com,", ",ops@example.com", "ops@@example.com",
		"ops@example.com;true", "ops@example.com\n* * * * * true", `"ops@example.com"`, "ops@example.com$(id)",
	}
	for _, value := range invalid {
		if err := ValidateBackupMailto(value); err == nil {
			t.Errorf("expected %q to be refused, got no error", value)
		}
	}
}

func TestValidateBackupMailtoNamesTheProperty(t *testing.T) {
	err := ValidateBackupMailto("ops@example.com;true")
	if err == nil {
		t.Fatal("expected an error")
	}

	if !strings.Contains(err.Error(), BackupMailtoProperty) {
		t.Errorf("expected the error to name %s, got %q", BackupMailtoProperty, err)
	}
}
