package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain points every test at a fake docker, so that no test reaches the
// daemon of the machine it runs on. Linking an app makes the network the link
// runs over, and a test that links without asking for a fake would otherwise
// leave networks behind on the host. The fake succeeds and prints nothing. A
// test that needs docker to answer sets DOCKER_BIN to a fake of its own.
func TestMain(m *testing.M) {
	os.Exit(runWithFakeDocker(m))
}

func runWithFakeDocker(m *testing.M) int {
	bin, err := os.MkdirTemp("", "dokku-datastore-docker")
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to create the fake docker: %s\n", err)
		return 1
	}
	defer os.RemoveAll(bin) //nolint:errcheck

	script := "#!/bin/sh\nexit 0\n"
	docker := filepath.Join(bin, "docker")
	if err := os.WriteFile(docker, []byte(script), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "unable to write the fake docker: %s\n", err)
		return 1
	}

	if err := os.Setenv("DOCKER_BIN", docker); err != nil {
		fmt.Fprintf(os.Stderr, "unable to use the fake docker: %s\n", err)
		return 1
	}

	return m.Run()
}

// recordedDockerCalls returns the docker commands a fake was asked to run
func recordedDockerCalls(t *testing.T, calls string) []string {
	t.Helper()

	contents, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return []string{}
	}
	if err != nil {
		t.Fatalf("failed to read the docker calls: %s", err)
	}

	return strings.Split(strings.TrimSpace(string(contents)), "\n")
}
