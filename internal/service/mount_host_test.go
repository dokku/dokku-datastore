package service

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The names are dokku's storage plugin's, so a mount spec written for one reads
// the same in the other.
func TestResolveChownID(t *testing.T) {
	tests := []struct {
		value    string
		uid      int
		chown    bool
		expected string
	}{
		{value: ""},
		{value: "false"},
		{value: "herokuish", uid: 32767, chown: true},
		{value: "heroku", uid: 1000, chown: true},
		{value: "paketo", uid: 2000, chown: true},
		{value: "packeto", uid: 2000, chown: true},
		{value: "root", uid: 0, chown: true},
		{value: "999", uid: 999, chown: true},
		{value: "nobody", expected: "Unsupported chown permissions"},
		{value: "70000", expected: "Unsupported chown permissions"},
	}

	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			uid, chown, err := ResolveChownID(test.value)
			if test.expected != "" {
				if err == nil || !strings.Contains(err.Error(), test.expected) {
					t.Fatalf("expected an error containing %q, got %v", test.expected, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %s", err)
			}

			if uid != test.uid || chown != test.chown {
				t.Errorf("expected %d and %v, got %d and %v", test.uid, test.chown, uid, chown)
			}
		})
	}
}

// withDockerAPIVersion stands in for the daemon, and fails the test if it is
// asked when it should not have been.
func withDockerAPIVersion(t *testing.T, version string, err error) *bool {
	t.Helper()

	asked := false
	previous := dockerAPIVersion
	dockerAPIVersion = func(ctx context.Context) (string, error) {
		asked = true
		return version, err
	}
	t.Cleanup(func() {
		dockerAPIVersion = previous
	})

	return &asked
}

// A chown is only applied inside the service's own directory, and a volume
// subpath only on a daemon that takes one.
func TestCheckMountsOnHost(t *testing.T) {
	root := "/var/lib/dokku/services/redis/lollipop"

	tests := []struct {
		name     string
		mounts   []Mount
		version  string
		expected string
		asks     bool
	}{
		{name: "no mounts"},
		{name: "a chown inside the service", mounts: []Mount{{Source: root + "/extra", ContainerPath: "/opt/a", Chown: "heroku"}}},
		{name: "a chown inside the service through a subpath", mounts: []Mount{{Source: root, ContainerPath: "/opt/a", Subpath: "extra", Chown: "1000"}}},
		{name: "a chown of false anywhere", mounts: []Mount{{Source: "/srv/a", ContainerPath: "/opt/a", Chown: "false"}}},
		{name: "a chown outside the service", mounts: []Mount{{Source: "/srv/a", ContainerPath: "/opt/a", Chown: "heroku"}}, expected: "only supported on a host path inside"},
		{name: "a chown of a sibling service", mounts: []Mount{{Source: root + "-2/extra", ContainerPath: "/opt/a", Chown: "heroku"}}, expected: "only supported on a host path inside"},
		{name: "a chown of the service root itself", mounts: []Mount{{Source: root, ContainerPath: "/opt/a", Chown: "heroku"}}, expected: "only supported on a host path inside"},
		{name: "a chown on a docker volume", mounts: []Mount{{Source: "data", ContainerPath: "/opt/a", Chown: "heroku"}}, expected: "not supported on docker volume"},
		{name: "a volume subpath on a new daemon", mounts: []Mount{{Source: "data", ContainerPath: "/opt/a", Subpath: "one"}}, version: "1.51", asks: true},
		{name: "a volume subpath on an old daemon", mounts: []Mount{{Source: "data", ContainerPath: "/opt/a", Subpath: "one"}}, version: "1.44", asks: true, expected: "needs docker api 1.45"},
		{name: "a host path subpath asks nothing", mounts: []Mount{{Source: "/srv/a", ContainerPath: "/opt/a", Subpath: "one"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			asked := withDockerAPIVersion(t, test.version, nil)

			err := CheckMountsOnHost(t.Context(), root, test.mounts)
			if *asked != test.asks {
				t.Errorf("expected the daemon to be asked: %v, was asked: %v", test.asks, *asked)
			}

			if test.expected == "" {
				if err != nil {
					t.Errorf("expected the mounts to be accepted, got %v", err)
				}
				return
			}

			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %v", test.expected, err)
			}
		})
	}
}

func TestCheckMountsOnHostReportsAnUnreachableDaemon(t *testing.T) {
	withDockerAPIVersion(t, "", errors.New("no daemon"))

	err := CheckMountsOnHost(t.Context(), "/root", []Mount{{Source: "data", ContainerPath: "/opt/a", Subpath: "one"}})
	if err == nil || !strings.Contains(err.Error(), "no daemon") {
		t.Errorf("expected the daemon's error, got %v", err)
	}
}

func TestAPIVersionAtLeast(t *testing.T) {
	tests := []struct {
		version  string
		expected bool
	}{
		{version: "1.45", expected: true},
		{version: "1.51", expected: true},
		{version: "2.0", expected: true},
		{version: "1.44", expected: false},
		{version: "1.9", expected: false},
		{version: "", expected: false},
		{version: "garbage", expected: false},
	}

	for _, test := range tests {
		if actual := apiVersionAtLeast(test.version, "1.45"); actual != test.expected {
			t.Errorf("expected %q to be %v, got %v", test.version, test.expected, actual)
		}
	}
}
