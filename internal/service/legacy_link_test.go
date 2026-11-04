package service

import (
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// withDockerOptions points dokku's roots at temporary directories, so the
// docker options of an app can be written where dokku keeps them: in
// properties from 0.38, and in a file per phase before it. It returns the lib
// root and the dokku root.
func withDockerOptions(t *testing.T) (string, string) {
	t.Helper()

	// a property write is chowned to the dokku user, which does not exist on a
	// development machine or a CI runner, so point that at whoever is running
	current, err := user.Current()
	if err != nil {
		t.Fatalf("failed to look up the current user: %v", err)
	}
	group, err := user.LookupGroupId(current.Gid)
	if err != nil {
		t.Fatalf("failed to look up the current group: %v", err)
	}
	t.Setenv("DOKKU_SYSTEM_USER", current.Username)
	t.Setenv("DOKKU_SYSTEM_GROUP", group.Name)

	libRoot := t.TempDir()
	dokkuRoot := t.TempDir()
	t.Setenv("DOKKU_LIB_ROOT", libRoot)
	t.Setenv("DOKKU_ROOT", dokkuRoot)

	return libRoot, dokkuRoot
}

// writeDockerOptions stores the docker options of a phase the way dokku 0.38
// and later does
func writeDockerOptions(t *testing.T, libRoot string, appName string, phase string, options ...string) string {
	t.Helper()

	directory := filepath.Join(libRoot, "config", "docker-options", appName)
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatalf("failed to create the docker options: %v", err)
	}

	filename := filepath.Join(directory, "_default_."+phase)
	if err := os.WriteFile(filename, []byte(strings.Join(options, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("failed to write the docker options: %v", err)
	}

	return filename
}

// writeLegacyDockerOptions stores the docker options of a phase the way dokku
// did before 0.38
func writeLegacyDockerOptions(t *testing.T, dokkuRoot string, appName string, phase string, options ...string) {
	t.Helper()

	directory := filepath.Join(dokkuRoot, appName)
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatalf("failed to create the app root: %v", err)
	}

	contents := "# docker options\n" + strings.Join(options, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(directory, "DOCKER_OPTIONS_"+strings.ToUpper(phase)), []byte(contents), 0644); err != nil {
		t.Fatalf("failed to write the docker options: %v", err)
	}
}

// storedOptions reads back the docker options of a phase from properties
func storedOptions(t *testing.T, filename string) []string {
	t.Helper()

	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("failed to read the docker options: %v", err)
	}

	options := []string{}
	for _, line := range strings.Split(string(contents), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			options = append(options, line)
		}
	}

	return options
}

// The option earlier versions linked an app with is what is looked for and
// removed, so it has to be exactly what they added
func TestLegacyLinkOption(t *testing.T) {
	if actual := LegacyLinkOption(redisDatastore(t), "lollipop"); actual != "--link dokku.redis.lollipop:dokku-redis-lollipop" {
		t.Errorf("unexpected option %q", actual)
	}
}

// Whether a link is legacy is read from wherever the installed dokku keeps the
// app's docker options, in any phase, and in either form the option is written
func TestHasLegacyLink(t *testing.T) {
	tests := []struct {
		name     string
		write    func(t *testing.T, libRoot string, dokkuRoot string)
		expected bool
	}{
		{
			name:     "an app with no docker options",
			write:    func(t *testing.T, libRoot string, dokkuRoot string) {},
			expected: false,
		},
		{
			name: "the option in the deploy phase",
			write: func(t *testing.T, libRoot string, dokkuRoot string) {
				writeDockerOptions(t, libRoot, "my-app", "deploy", "--restart=on-failure:10", "--link dokku.redis.lollipop:dokku-redis-lollipop")
			},
			expected: true,
		},
		{
			name: "the option in the run phase alone",
			write: func(t *testing.T, libRoot string, dokkuRoot string) {
				writeDockerOptions(t, libRoot, "my-app", "run", "--link=dokku.redis.lollipop:dokku-redis-lollipop")
			},
			expected: true,
		},
		{
			name: "a link to another service",
			write: func(t *testing.T, libRoot string, dokkuRoot string) {
				writeDockerOptions(t, libRoot, "my-app", "deploy", "--link dokku.redis.gobstopper:dokku-redis-gobstopper")
			},
			expected: false,
		},
		{
			name: "the option in a file a dokku before 0.38 reads",
			write: func(t *testing.T, libRoot string, dokkuRoot string) {
				writeLegacyDockerOptions(t, dokkuRoot, "my-app", "build", "--link dokku.redis.lollipop:dokku-redis-lollipop")
			},
			expected: true,
		},
		{
			// the file is what such a dokku reads, so a property it never
			// looks at says nothing about the link
			name: "a file a dokku before 0.38 reads, without the option",
			write: func(t *testing.T, libRoot string, dokkuRoot string) {
				for _, phase := range legacyLinkPhases {
					writeLegacyDockerOptions(t, dokkuRoot, "my-app", phase, "--restart=on-failure:10")
					writeDockerOptions(t, libRoot, "my-app", phase, "--link dokku.redis.lollipop:dokku-redis-lollipop")
				}
			},
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			libRoot, dokkuRoot := withDockerOptions(t)
			test.write(t, libRoot, dokkuRoot)

			if actual := HasLegacyLink(redisDatastore(t), "lollipop", "my-app"); actual != test.expected {
				t.Errorf("expected %t, got %t", test.expected, actual)
			}
		})
	}
}

// The option is removed from properties without asking dokku, as stored, and
// every other option is kept
func TestRemoveLegacyLinkOption(t *testing.T) {
	libRoot, _ := withDockerOptions(t)
	calls := fakeDokkuBinary(t)

	deploy := writeDockerOptions(t, libRoot, "my-app", "deploy", "--link dokku.redis.lollipop:dokku-redis-lollipop", "--restart=on-failure:10")
	run := writeDockerOptions(t, libRoot, "my-app", "run", "--link=dokku.redis.lollipop:dokku-redis-lollipop")
	build := writeDockerOptions(t, libRoot, "my-app", "build", "--link dokku.redis.gobstopper:dokku-redis-gobstopper")

	datastore := redisDatastore(t)
	if err := RemoveLegacyLinkOption(t.Context(), datastore, "lollipop", "my-app"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if actual := storedOptions(t, deploy); !slices.Equal(actual, []string{"--restart=on-failure:10"}) {
		t.Errorf("expected the deploy options to keep only the restart policy, got %q", actual)
	}
	if actual := storedOptions(t, run); len(actual) != 0 {
		t.Errorf("expected the run options to be empty, got %q", actual)
	}
	if actual := storedOptions(t, build); !slices.Equal(actual, []string{"--link dokku.redis.gobstopper:dokku-redis-gobstopper"}) {
		t.Errorf("expected the link to another service to be kept, got %q", actual)
	}

	if HasLegacyLink(datastore, "lollipop", "my-app") {
		t.Error("expected the app to no longer have a legacy link")
	}

	if actual := recordedDokkuCalls(t, calls); len(actual) != 0 {
		t.Errorf("expected dokku not to be asked, got %q", actual)
	}
}

// A dokku before 0.38 reads the options from a file the package does not
// write, so it is asked to remove them itself, for the phases that have it
func TestRemoveLegacyLinkOptionBefore038(t *testing.T) {
	_, dokkuRoot := withDockerOptions(t)
	calls := fakeDokkuBinary(t)

	writeLegacyDockerOptions(t, dokkuRoot, "my-app", "deploy", "--link dokku.redis.lollipop:dokku-redis-lollipop")
	writeLegacyDockerOptions(t, dokkuRoot, "my-app", "build", "--restart=on-failure:10")

	if err := RemoveLegacyLinkOption(t.Context(), redisDatastore(t), "lollipop", "my-app"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := []string{"docker-options:remove my-app deploy --link dokku.redis.lollipop:dokku-redis-lollipop"}
	if actual := recordedDokkuCalls(t, calls); !slices.Equal(actual, expected) {
		t.Errorf("expected the calls %q, got %q", expected, actual)
	}
}

// fakeDokkuBinary puts a dokku on the path that records what it is asked to
// run and does nothing else
func fakeDokkuBinary(t *testing.T) string {
	t.Helper()

	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho \"$*\" >> \"" + calls + "\"\n"
	if err := os.WriteFile(filepath.Join(bin, "dokku"), []byte(script), 0755); err != nil {
		t.Fatalf("failed to write the fake dokku: %v", err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

// recordedDokkuCalls returns what the fake dokku was asked to run
func recordedDokkuCalls(t *testing.T, calls string) []string {
	t.Helper()

	contents, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return []string{}
	}
	if err != nil {
		t.Fatalf("failed to read the recorded calls: %v", err)
	}

	return strings.Split(strings.TrimSpace(string(contents)), "\n")
}
