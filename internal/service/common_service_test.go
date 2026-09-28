package service

import (
	"context"
	"github.com/dokku/dokku-datastore/internal/definition"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku/plugins/common"
)

// withServiceRoot points DokkuLibRoot at a temporary directory and returns the
// root folder for the named service. DokkuLibRoot is a package level variable
// assigned in init(), so it has to be swapped directly rather than through the
// environment, which means these tests cannot run in parallel.
func withServiceRoot(t *testing.T, s *Datastore, serviceName string) string {
	t.Helper()

	// the service files are chowned to the dokku user, which does not exist on a
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

	previous := DokkuLibRoot
	DokkuLibRoot = t.TempDir()
	t.Cleanup(func() {
		DokkuLibRoot = previous
	})

	serviceRoot := Folders(s, serviceName).Root
	if err := os.MkdirAll(serviceRoot, 0755); err != nil {
		t.Fatalf("failed to create service root: %v", err)
	}

	return serviceRoot
}

func TestLinkedApps(t *testing.T) {
	tests := []struct {
		name     string
		links    *string
		expected []string
	}{
		{
			name:     "no links file",
			links:    nil,
			expected: []string{},
		},
		{
			name:     "empty links file",
			links:    ptr(""),
			expected: []string{},
		},
		{
			name:     "a single linked app",
			links:    ptr("my-app\n"),
			expected: []string{"my-app"},
		},
		{
			name:     "several linked apps",
			links:    ptr("my-app\nother-app\n"),
			expected: []string{"my-app", "other-app"},
		},
	}

	datastore := Datastores["redis"]
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceRoot := withServiceRoot(t, datastore, "lollipop")
			if test.links != nil {
				if err := os.WriteFile(filepath.Join(serviceRoot, "LINKS"), []byte(*test.links), 0644); err != nil {
					t.Fatalf("failed to write links file: %v", err)
				}
			}

			actual := LinkedApps(context.Background(), LinkedAppsInput{
				Datastore:   datastore,
				ServiceName: "lollipop",
			})
			if !slices.Equal(actual, test.expected) {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

// multiPortDatastore is the redis datastore with a second port, covering the
// multi port paths that no real datastore exercises yet.
//
// A second port is added to the definition rather than to the properties it
// projects, so this exercises the path a real multi port datastore will take
// rather than a shape only a test can produce.
func multiPortDatastore(t *testing.T) *Datastore {
	t.Helper()

	redis, ok := Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	multi := &Datastore{Definition: redis.Definition}
	multi.Definition.Service.Ports = append(
		append([]definition.Port{}, redis.Definition.Service.Ports...),
		definition.Port{Name: "second", Target: 6380},
	)

	return multi
}

func TestExposedHostPorts(t *testing.T) {
	tests := []struct {
		name     string
		portFile *string
		expected []string
	}{
		{
			name:     "no port file",
			portFile: nil,
			expected: []string{},
		},
		{
			name:     "empty port file",
			portFile: ptr(""),
			expected: []string{},
		},
		{
			name:     "a single port",
			portFile: ptr("33201\n"),
			expected: []string{"33201"},
		},
		{
			name:     "several ports on one line",
			portFile: ptr("33201 33202\n"),
			expected: []string{"33201", "33202"},
		},
		{
			name:     "no trailing newline",
			portFile: ptr("33201 33202"),
			expected: []string{"33201", "33202"},
		},
	}

	datastore := Datastores["redis"]
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceRoot := withServiceRoot(t, datastore, "lollipop")
			if test.portFile != nil {
				if err := os.WriteFile(filepath.Join(serviceRoot, "PORT"), []byte(*test.portFile), 0644); err != nil {
					t.Fatalf("failed to write port file: %v", err)
				}
			}

			actual := ExposedHostPorts(datastore, "lollipop")
			if !slices.Equal(actual, test.expected) {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

func TestExposedPorts(t *testing.T) {
	tests := []struct {
		name      string
		datastore *Datastore
		portFile  *string
		address   string
		expected  string
	}{
		{
			name:      "not exposed",
			datastore: Datastores["redis"],
			portFile:  nil,
			expected:  "-",
		},
		{
			name:      "empty port file",
			datastore: Datastores["redis"],
			portFile:  ptr(""),
			expected:  "-",
		},
		{
			name:      "a single port",
			datastore: Datastores["redis"],
			portFile:  ptr("33201\n"),
			expected:  "6379->33201",
		},
		{
			name:      "several ports on one line",
			datastore: multiPortDatastore(t),
			portFile:  ptr("33201 33202\n"),
			expected:  "6379->33201 6380->33202",
		},
		{
			name:      "more ports than the datastore declares",
			datastore: Datastores["redis"],
			portFile:  ptr("33201 33202\n"),
			expected:  "6379->33201",
		},
		{
			name:      "a port on the port-bind-address",
			datastore: Datastores["redis"],
			portFile:  ptr("33201\n"),
			address:   "10.0.0.5",
			expected:  "6379->10.0.0.5:33201",
		},
		{
			name:      "a port on an IPv6 port-bind-address",
			datastore: Datastores["redis"],
			portFile:  ptr("33201\n"),
			address:   "::1",
			expected:  "6379->[::1]:33201",
		},
		{
			name:      "a port with an address of its own keeps it",
			datastore: multiPortDatastore(t),
			portFile:  ptr("127.0.0.1:33201 33202\n"),
			address:   "10.0.0.5",
			expected:  "6379->127.0.0.1:33201 6380->10.0.0.5:33202",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceRoot := withServiceRoot(t, test.datastore, "lollipop")
			t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)
			if test.portFile != nil {
				if err := os.WriteFile(filepath.Join(serviceRoot, "PORT"), []byte(*test.portFile), 0644); err != nil {
					t.Fatalf("failed to write port file: %v", err)
				}
			}

			if test.address != "" {
				if err := common.PropertyWrite(test.datastore.Properties().CommandPrefix, "lollipop", PortBindAddressProperty, test.address); err != nil {
					t.Fatalf("failed to write the property: %v", err)
				}
			}

			if actual := ExposedPorts(test.datastore, "lollipop"); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// dsnSecondPortDatastore is the redis datastore with a port declared ahead of
// the one its dsn names, so that the dsn's port is not the first in the port
// file. No real datastore has that shape yet.
func dsnSecondPortDatastore(t *testing.T) *Datastore {
	t.Helper()

	redis := redisDatastore(t)
	shifted := &Datastore{Definition: redis.Definition}
	shifted.Definition.Service.Ports = append(
		[]definition.Port{{Name: "admin", Target: 6380}},
		redis.Definition.Service.Ports...,
	)

	return shifted
}

func TestExposedURL(t *testing.T) {
	tests := []struct {
		name        string
		datastore   *Datastore
		portFile    *string
		vhost       *string
		exposeHost  string
		bindAddress string
		expected    string
	}{
		{
			name:      "not exposed",
			datastore: Datastores["redis"],
			vhost:     ptr("dokku.me\n"),
			expected:  "",
		},
		{
			name:      "empty port file",
			datastore: Datastores["redis"],
			portFile:  ptr(""),
			vhost:     ptr("dokku.me\n"),
			expected:  "",
		},
		{
			name:      "the global domain",
			datastore: Datastores["redis"],
			portFile:  ptr("33201\n"),
			vhost:     ptr("dokku.me\n"),
			expected:  "redis://:secret@dokku.me:33201",
		},
		{
			name:      "the first of several global domains",
			datastore: Datastores["redis"],
			portFile:  ptr("33201\n"),
			vhost:     ptr("\ndokku.me other.me\nthird.me\n"),
			expected:  "redis://:secret@dokku.me:33201",
		},
		{
			name:      "no global domain and no expose-host",
			datastore: Datastores["redis"],
			portFile:  ptr("33201\n"),
			expected:  "",
		},
		{
			name:       "the expose-host over the global domain",
			datastore:  Datastores["redis"],
			portFile:   ptr("33201\n"),
			vhost:      ptr("dokku.me\n"),
			exposeHost: "db.example.com",
			expected:   "redis://:secret@db.example.com:33201",
		},
		{
			name:       "the expose-host with no global domain",
			datastore:  Datastores["redis"],
			portFile:   ptr("33201\n"),
			exposeHost: "203.0.113.7",
			expected:   "redis://:secret@203.0.113.7:33201",
		},
		{
			name:       "an IPv6 expose-host is bracketed",
			datastore:  Datastores["redis"],
			portFile:   ptr("33201\n"),
			exposeHost: "2001:db8::1",
			expected:   "redis://:secret@[2001:db8::1]:33201",
		},
		{
			name:        "the port-bind-address is not the host",
			datastore:   Datastores["redis"],
			portFile:    ptr("33201\n"),
			vhost:       ptr("dokku.me\n"),
			bindAddress: "10.0.0.5",
			expected:    "redis://:secret@dokku.me:33201",
		},
		{
			name:        "the port-bind-address alone is not a host",
			datastore:   Datastores["redis"],
			portFile:    ptr("33201\n"),
			bindAddress: "10.0.0.5",
			expected:    "",
		},
		{
			name:      "an address in the port is not the host",
			datastore: Datastores["redis"],
			portFile:  ptr("127.0.0.1:33201\n"),
			vhost:     ptr("dokku.me\n"),
			expected:  "redis://:secret@dokku.me:33201",
		},
		{
			name:      "an IPv6 address in the port is not the host",
			datastore: Datastores["redis"],
			portFile:  ptr("[::1]:33201\n"),
			vhost:     ptr("dokku.me\n"),
			expected:  "redis://:secret@dokku.me:33201",
		},
		{
			name:      "the port the dsn names rather than the first",
			datastore: dsnSecondPortDatastore(t),
			portFile:  ptr("33201 33202\n"),
			vhost:     ptr("dokku.me\n"),
			expected:  "redis://:secret@dokku.me:33202",
		},
		{
			name:      "fewer ports than the datastore declares",
			datastore: dsnSecondPortDatastore(t),
			portFile:  ptr("33201\n"),
			vhost:     ptr("dokku.me\n"),
			expected:  "",
		},
		{
			name:      "a database in the dsn",
			datastore: Datastores["postgres"],
			portFile:  ptr("33201\n"),
			vhost:     ptr("dokku.me\n"),
			expected:  "postgres://postgres:secret@dokku.me:33201/lollipop",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceRoot := withServiceRoot(t, test.datastore, "lollipop")
			t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

			dokkuRoot := t.TempDir()
			t.Setenv("DOKKU_ROOT", dokkuRoot)
			if test.vhost != nil {
				if err := os.WriteFile(filepath.Join(dokkuRoot, "VHOST"), []byte(*test.vhost), 0644); err != nil {
					t.Fatalf("failed to write the global domains: %v", err)
				}
			}

			if err := os.WriteFile(filepath.Join(serviceRoot, "PASSWORD"), []byte("secret"), 0600); err != nil {
				t.Fatalf("failed to write the password: %v", err)
			}

			if test.portFile != nil {
				if err := os.WriteFile(filepath.Join(serviceRoot, "PORT"), []byte(*test.portFile), 0644); err != nil {
					t.Fatalf("failed to write port file: %v", err)
				}
			}

			commandPrefix := test.datastore.Properties().CommandPrefix
			for property, value := range map[string]string{
				ExposeHostProperty:      test.exposeHost,
				PortBindAddressProperty: test.bindAddress,
			} {
				if value == "" {
					continue
				}

				if err := common.PropertyWrite(commandPrefix, "lollipop", property, value); err != nil {
					t.Fatalf("failed to write the %s property: %v", property, err)
				}
			}

			if actual := test.datastore.ExposedURL("lollipop"); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// The exposed dsn differs from the one a linked app is handed only in where it
// points, so a client off the host authenticates the way the app does.
func TestExposedURLKeepsTheCredentials(t *testing.T) {
	postgres := Datastores["postgres"]
	serviceRoot := withServiceRoot(t, postgres, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)
	t.Setenv("DOKKU_ROOT", t.TempDir())

	for filename, contents := range map[string]string{
		"PASSWORD":      "secret",
		"DATABASE_NAME": "lollipop_db",
		"PORT":          "33201",
	} {
		if err := os.WriteFile(filepath.Join(serviceRoot, filename), []byte(contents), 0600); err != nil {
			t.Fatalf("failed to write %s: %v", filename, err)
		}
	}

	if err := common.PropertyWrite(postgres.Properties().CommandPrefix, "lollipop", ExposeHostProperty, "db.example.com"); err != nil {
		t.Fatalf("failed to write the property: %v", err)
	}

	internal := postgres.URL("lollipop", "")
	exposed := postgres.ExposedURL("lollipop")

	expected := strings.Replace(internal, DNSHostname(postgres, "lollipop")+":5432", "db.example.com:33201", 1)
	if exposed != expected {
		t.Errorf("expected the exposed dsn to be %q, got %q", expected, exposed)
	}
}

// A host without dokku sets no DOKKU_ROOT. Reading the global domain there
// used to stop the process, so info on any exposed service without an
// expose-host failed rather than reporting an empty exposed dsn.
func TestExposedURLWithoutADokkuRoot(t *testing.T) {
	if _, err := os.Stat("/home/dokku/VHOST"); err == nil {
		t.Skip("the host has a global domain of its own")
	}

	redis := Datastores["redis"]
	serviceRoot := withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)
	t.Setenv("DOKKU_ROOT", "")

	if err := os.WriteFile(filepath.Join(serviceRoot, "PORT"), []byte("33201"), 0644); err != nil {
		t.Fatalf("failed to write port file: %v", err)
	}

	if actual := redis.ExposedURL("lollipop"); actual != "" {
		t.Errorf("expected no exposed dsn without a global domain, got %q", actual)
	}
}

func ptr(s string) *string {
	return &s
}

func TestAddAndRemoveLinkedApp(t *testing.T) {
	datastore := Datastores["redis"]
	ctx := context.Background()
	input := LinkedAppsInput{Datastore: datastore, ServiceName: "lollipop"}

	withServiceRoot(t, datastore, "lollipop")

	// adding to a service with no links file at all
	if err := AddLinkedApp(ctx, input, "my-app"); err != nil {
		t.Fatalf("failed to add a linked app: %v", err)
	}
	if actual := LinkedApps(ctx, input); !slices.Equal(actual, []string{"my-app"}) {
		t.Fatalf("expected [my-app], got %v", actual)
	}

	// a second app, kept sorted
	if err := AddLinkedApp(ctx, input, "another-app"); err != nil {
		t.Fatalf("failed to add a second linked app: %v", err)
	}
	if actual := LinkedApps(ctx, input); !slices.Equal(actual, []string{"another-app", "my-app"}) {
		t.Fatalf("expected [another-app my-app], got %v", actual)
	}

	// adding the same app again does not duplicate it
	if err := AddLinkedApp(ctx, input, "my-app"); err != nil {
		t.Fatalf("failed to re-add a linked app: %v", err)
	}
	if actual := LinkedApps(ctx, input); !slices.Equal(actual, []string{"another-app", "my-app"}) {
		t.Fatalf("expected [another-app my-app], got %v", actual)
	}

	// removing leaves the rest alone
	if err := RemoveLinkedApp(ctx, input, "my-app"); err != nil {
		t.Fatalf("failed to remove a linked app: %v", err)
	}
	if actual := LinkedApps(ctx, input); !slices.Equal(actual, []string{"another-app"}) {
		t.Fatalf("expected [another-app], got %v", actual)
	}

	// removing an app that was never linked is not an error
	if err := RemoveLinkedApp(ctx, input, "never-linked"); err != nil {
		t.Fatalf("expected removing an unlinked app to succeed, got: %v", err)
	}

	// removing the last app empties the file rather than leaving a stale entry
	if err := RemoveLinkedApp(ctx, input, "another-app"); err != nil {
		t.Fatalf("failed to remove the last linked app: %v", err)
	}
	if actual := LinkedApps(ctx, input); len(actual) != 0 {
		t.Fatalf("expected no linked apps, got %v", actual)
	}
}

func TestRemoveLinkedAppWithoutALinksFile(t *testing.T) {
	datastore := Datastores["redis"]
	withServiceRoot(t, datastore, "lollipop")

	err := RemoveLinkedApp(context.Background(), LinkedAppsInput{Datastore: datastore, ServiceName: "lollipop"}, "my-app")
	if err != nil {
		t.Errorf("expected removing from a missing links file to succeed, got: %v", err)
	}
}

// Info on a service whose container is gone used to crash. Two of its entries
// asked for a container id without passing the datastore needed to name one, so
// the lookup they fell into dereferenced nothing the moment there was no
// container to short circuit it - which is every stopped service.
func TestInfoOnAServiceWithNoContainer(t *testing.T) {
	redis := Datastores["redis"]
	withServiceRoot(t, redis, "gone")

	// the property lookups Info makes read the environment rather than the
	// package variable withServiceRoot swaps
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	info := Info(context.Background(), InfoInput{Datastore: redis, ServiceName: "gone"})

	if info["id"] != "" {
		t.Errorf("expected no container id, got %q", info["id"])
	}

	if info["internal-ip"] != "" {
		t.Errorf("expected no internal ip, got %q", info["internal-ip"])
	}

	// the entries that do not need a container are still answered
	if info["service-root"] == "" {
		t.Error("expected the service root to be reported")
	}
}

// The keyserver is read in two places that have to agree about where it is
// stored: the backup path that passes it to the image, and the info that
// reports it back.
func TestKeyserver(t *testing.T) {
	redis := Datastores["redis"]
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	if keyserver := Keyserver(redis, "lollipop"); keyserver != "" {
		t.Errorf("expected an unset keyserver to read empty, got %q", keyserver)
	}

	if err := common.PropertyWrite(redis.Properties().CommandPrefix, "lollipop", KeyserverProperty, "keys.example.com"); err != nil {
		t.Fatalf("failed to write the property: %s", err)
	}

	if keyserver := Keyserver(redis, "lollipop"); keyserver != "keys.example.com" {
		t.Errorf("expected the keyserver to be read back, got %q", keyserver)
	}
}

// A lookup with nothing to name a container with finds nothing, which is the
// same answer a stopped service gives.
func TestLiveContainerIDWithoutADatastore(t *testing.T) {
	if id := LiveContainerID(context.Background(), LiveContainerIDInput{ServiceName: "gone"}); id != "" {
		t.Errorf("expected no container id, got %q", id)
	}
}

// Docker has seven container states and Start used to ask for two of them by
// name, so a container in any of the other five was invisible to it and Start
// went on to build one beside it. Every state is named here, and the default is
// asserted too: a state docker ships in a later release has to land somewhere
// that cannot produce a name conflict.
func TestActionForStatus(t *testing.T) {
	tests := []struct {
		status   string
		expected containerAction
	}{
		{status: "missing", expected: buildContainer},
		{status: "running", expected: keepContainer},
		{status: "restarting", expected: keepContainer},
		{status: "paused", expected: unpauseContainer},
		{status: "created", expected: resumeContainer},
		{status: "exited", expected: resumeContainer},
		{status: "dead", expected: replaceContainer},
		{status: "removing", expected: replaceContainer},
		{status: "hibernating", expected: replaceContainer},
		{status: "", expected: replaceContainer},
	}

	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			if actual := actionForStatus(test.status); actual != test.expected {
				t.Errorf("expected %d for a %q container, got %d", test.expected, test.status, actual)
			}
		})
	}
}
