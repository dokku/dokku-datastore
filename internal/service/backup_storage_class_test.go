package service

import (
	"strings"
	"testing"
)

func TestValidateBackupStorageClass(t *testing.T) {
	valid := append([]string{"", " STANDARD_IA "}, backupStorageClasses...)
	for _, value := range valid {
		if err := ValidateBackupStorageClass(value); err != nil {
			t.Errorf("expected %q to be accepted, got %q", value, err)
		}
	}

	// the aws cli matches the name exactly, so a lowercase one is refused by it
	// and has to be refused here too
	invalid := []string{"standard_ia", "Standard", "NOT_A_CLASS", "STANDARD IA", "STANDARD_IA,GLACIER", "EXPRESS_ONEZONE"}
	for _, value := range invalid {
		if err := ValidateBackupStorageClass(value); err == nil {
			t.Errorf("expected %q to be refused, got no error", value)
		}
	}
}

func TestValidateBackupStorageClassNamesTheProperty(t *testing.T) {
	err := ValidateBackupStorageClass("NOT_A_CLASS")
	if err == nil {
		t.Fatal("expected an error")
	}

	if !strings.Contains(err.Error(), BackupStorageClassProperty) {
		t.Errorf("expected the error to name %s, got %q", BackupStorageClassProperty, err)
	}

	// the accepted values are listed, so the refusal says what to use instead
	if !strings.Contains(err.Error(), "STANDARD_IA") {
		t.Errorf("expected the error to list the storage classes, got %q", err)
	}
}
