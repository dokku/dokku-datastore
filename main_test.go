package main

import (
	"testing"
)

// The cli is the sdk with the version the build stamped, so the binary reports
// what it was released as
func TestRunReportsTheStampedVersion(t *testing.T) {
	previous := Version
	Version = "v0.0.0-test"
	t.Cleanup(func() {
		Version = previous
	})

	if actual := Run([]string{"version"}); actual != 0 {
		t.Errorf("expected the version command to succeed, got exit code %d", actual)
	}
}

func TestRunRefusesAnUnknownCommand(t *testing.T) {
	if actual := Run([]string{"nonsense"}); actual == 0 {
		t.Error("expected an unknown command to fail")
	}
}
