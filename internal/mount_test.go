package internal

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// mountedService is a redis service named lollipop with the given mounts
// already written, and a host directory that exists to mount
func mountedService(t *testing.T, mounts []service.Mount) (*service.Datastore, string) {
	t.Helper()

	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	if err := service.WriteMounts(datastore, "lollipop", mounts); err != nil {
		t.Fatalf("failed to write the mounts: %s", err)
	}

	return datastore, t.TempDir()
}

// readMounts is what the service has after a command ran
func readMounts(t *testing.T, datastore *service.Datastore) []service.Mount {
	t.Helper()

	mounts, err := service.ServiceMounts(datastore, "lollipop")
	if err != nil {
		t.Fatalf("failed to read the mounts: %s", err)
	}

	return mounts
}

func TestMountedSet(t *testing.T) {
	datastore := service.Datastores["redis"]
	current := []service.Mount{
		{Source: "/srv/a", ContainerPath: "/opt/a", Readonly: true, VolumeOptions: "z"},
	}

	tests := []struct {
		name     string
		input    MountServiceInput
		expected []service.Mount
		message  string
	}{
		{
			name:     "a new mount is added",
			input:    MountServiceInput{Specs: []string{"/srv/b:/opt/b:ro"}},
			expected: append(append([]service.Mount{}, current...), service.Mount{Source: "/srv/b", ContainerPath: "/opt/b", Readonly: true}),
			message:  "/srv/b mounted at /opt/b on lollipop",
		},
		{
			name: "the flags apply to the one spec",
			input: MountServiceInput{
				Specs:         []string{"/srv/b:/opt/b"},
				Readonly:      true,
				VolumeOptions: "nocopy",
				Subpath:       "uploads",
				Chown:         "heroku",
			},
			expected: append(append([]service.Mount{}, current...), service.Mount{Source: "/srv/b", ContainerPath: "/opt/b", Readonly: true, VolumeOptions: "nocopy", Subpath: "uploads", Chown: "heroku"}),
			message:  "/srv/b mounted at /opt/b on lollipop",
		},
		{
			// rewritten rather than merged, so what is left off is cleared
			name:     "the same mount again is updated in place",
			input:    MountServiceInput{Specs: []string{"/srv/a:/opt/a/"}},
			expected: []service.Mount{{Source: "/srv/a", ContainerPath: "/opt/a/"}},
			message:  "Mount of /srv/a at /opt/a/ on lollipop updated",
		},
		{
			name:     "replace swaps the whole set",
			input:    MountServiceInput{Replace: true, Specs: []string{"/srv/b:/opt/b", "data:/opt/data:volume-chown=root"}},
			expected: []service.Mount{{Source: "/srv/b", ContainerPath: "/opt/b"}, {Source: "data", ContainerPath: "/opt/data", Chown: "root"}},
			message:  "Mounts replaced on lollipop",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.input.Datastore = datastore
			test.input.ServiceName = "lollipop"

			mounts, message, err := mountedSet(current, test.input)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if !reflect.DeepEqual(mounts, test.expected) {
				t.Errorf("expected %+v, got %+v", test.expected, mounts)
			}

			if message != test.message {
				t.Errorf("expected the message %q, got %q", test.message, message)
			}
		})
	}
}

func TestMountedSetRefuses(t *testing.T) {
	datastore := service.Datastores["redis"]
	current := []service.Mount{{Source: "/srv/a", ContainerPath: "/opt/a"}}

	tests := []struct {
		name     string
		input    MountServiceInput
		expected string
	}{
		{name: "no mount", input: MountServiceInput{}, expected: "Must specify a mount"},
		{name: "two mounts without replace", input: MountServiceInput{Specs: []string{"/srv/b:/opt/b", "/srv/c:/opt/c"}}, expected: "Only one mount can be specified without --replace"},
		{name: "a container dir another source holds", input: MountServiceInput{Specs: []string{"/srv/b:/opt/a"}}, expected: "Container path /opt/a is already mounted from /srv/a"},
		{name: "readonly as a flag and a token", input: MountServiceInput{Specs: []string{"/srv/b:/opt/b:rw"}, Readonly: true}, expected: "The --volume-readonly flag cannot be used with a ro or rw option"},
		{name: "options as a flag and a token", input: MountServiceInput{Specs: []string{"/srv/b:/opt/b:z"}, VolumeOptions: "nocopy"}, expected: "The --volume-options flag cannot be used with mount options"},
		{name: "a subpath as a flag and a token", input: MountServiceInput{Specs: []string{"/srv/b:/opt/b:volume-subpath=x"}, Subpath: "y"}, expected: "The --volume-subpath flag cannot be used with volume-subpath"},
		{name: "a chown as a flag and a token", input: MountServiceInput{Specs: []string{"/srv/b:/opt/b:volume-chown=root"}, Chown: "heroku"}, expected: "The --volume-chown flag cannot be used with volume-chown"},
		{name: "an empty replace", input: MountServiceInput{Replace: true}, expected: "Must specify at least one mount, use redis:unmount --all to remove all mounts"},
		{name: "a flag with replace", input: MountServiceInput{Replace: true, Specs: []string{"/srv/b:/opt/b"}, Readonly: true}, expected: "The --volume-readonly flag cannot be used with --replace; set ro in the mount spec instead"},
		{name: "a malformed spec with replace", input: MountServiceInput{Replace: true, Specs: []string{"/srv/b:/opt/b", "/srv/c"}}, expected: "Invalid mount specified: /srv/c"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.input.Datastore = datastore
			test.input.ServiceName = "lollipop"

			_, _, err := mountedSet(current, test.input)
			if err == nil {
				t.Fatal("expected the mount to be refused")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %q", test.expected, err)
			}
		})
	}
}

func TestMountServiceWritesTheMount(t *testing.T) {
	datastore, source := mountedService(t, nil)

	message, err := MountService(MountServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Specs:       []string{source + ":/data/extra:ro"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if !strings.Contains(message, "mounted at /data/extra") {
		t.Errorf("unexpected message %q", message)
	}

	expected := []service.Mount{{Source: source, ContainerPath: "/data/extra", Readonly: true}}
	if actual := readMounts(t, datastore); !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %+v, got %+v", expected, actual)
	}
}

// A refused mount leaves what the service had alone, whichever check refused it
func TestMountServiceRefusalsWriteNothing(t *testing.T) {
	tests := []struct {
		name     string
		spec     func(source string) string
		replace  bool
		expected string
	}{
		{
			name:     "a host path that does not exist",
			spec:     func(source string) string { return filepath.Join(source, "missing") + ":/opt/b" },
			expected: "does not exist",
		},
		{
			name:     "a directory the definition mounts",
			spec:     func(source string) string { return source + ":/data" },
			expected: "Container path /data is already mounted by the redis definition",
		},
		{
			name:     "an option docker does not take",
			spec:     func(source string) string { return source + ":/opt/b:noexec" },
			expected: `Volume option "noexec" is not one docker takes`,
		},
		{
			name:     "a relative container dir",
			spec:     func(source string) string { return source + ":opt/b" },
			expected: "must be absolute",
		},
		{
			name:     "one bad mount in a replacement",
			spec:     func(source string) string { return source + ":/opt/b:noexec" },
			replace:  true,
			expected: "is not one docker takes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := []service.Mount{{Source: "some-volume", ContainerPath: "/opt/a"}}
			datastore, source := mountedService(t, existing)

			_, err := MountService(MountServiceInput{
				Datastore:   datastore,
				Replace:     test.replace,
				ServiceName: "lollipop",
				Specs:       []string{test.spec(source)},
			})
			if err == nil {
				t.Fatal("expected the mount to be refused")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %q", test.expected, err)
			}

			if actual := readMounts(t, datastore); !reflect.DeepEqual(actual, existing) {
				t.Errorf("expected the mounts to be left as %+v, got %+v", existing, actual)
			}
		})
	}
}

func TestUnmountedSet(t *testing.T) {
	datastore := service.Datastores["redis"]
	current := []service.Mount{
		{Source: "/srv/a", ContainerPath: "/opt/a", Readonly: true},
		{Source: "/srv/b", ContainerPath: "/opt/b"},
		{Source: "data", ContainerPath: "/opt/data"},
	}

	tests := []struct {
		name     string
		input    UnmountServiceInput
		expected []service.Mount
		message  string
	}{
		{
			// the options a mount was made with do not have to be repeated
			name:     "one mount, named without its options",
			input:    UnmountServiceInput{Specs: []string{"/srv/a:/opt/a"}},
			expected: []service.Mount{current[1], current[2]},
			message:  "Removed 1 mount(s) from lollipop",
		},
		{
			name:     "several mounts",
			input:    UnmountServiceInput{Specs: []string{"/srv/b:/opt/b/", "data:/opt/data:ro"}},
			expected: []service.Mount{current[0]},
			message:  "Removed 2 mount(s) from lollipop",
		},
		{
			name:    "every mount",
			input:   UnmountServiceInput{All: true},
			message: "Removed all mounts on lollipop",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.input.Datastore = datastore
			test.input.ServiceName = "lollipop"

			mounts, message, err := unmountedSet(current, test.input)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if !reflect.DeepEqual(mounts, test.expected) {
				t.Errorf("expected %+v, got %+v", test.expected, mounts)
			}

			if message != test.message {
				t.Errorf("expected the message %q, got %q", test.message, message)
			}
		})
	}
}

func TestUnmountedSetRefuses(t *testing.T) {
	datastore := service.Datastores["redis"]
	current := []service.Mount{{Source: "/srv/a", ContainerPath: "/opt/a"}}

	tests := []struct {
		name     string
		input    UnmountServiceInput
		expected string
	}{
		{name: "no mount", input: UnmountServiceInput{}, expected: "Must specify at least one mount, use redis:unmount --all to remove all mounts"},
		{name: "a mount with all", input: UnmountServiceInput{All: true, Specs: []string{"/srv/a:/opt/a"}}, expected: "A mount cannot be specified with --all"},
		{name: "a mount the service does not have", input: UnmountServiceInput{Specs: []string{"/srv/b:/opt/a"}}, expected: "Mount path does not exist."},
		{name: "a malformed mount", input: UnmountServiceInput{Specs: []string{"/srv/a"}}, expected: "Invalid mount specified: /srv/a"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.input.Datastore = datastore
			test.input.ServiceName = "lollipop"

			_, _, err := unmountedSet(current, test.input)
			if err == nil {
				t.Fatal("expected the unmount to be refused")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %q", test.expected, err)
			}
		})
	}
}

// One argument naming a mount the service does not have leaves every other one
// in place, rather than removing the ones named before it
func TestUnmountServiceIsAllOrNothing(t *testing.T) {
	existing := []service.Mount{{Source: "/srv/a", ContainerPath: "/opt/a"}, {Source: "/srv/b", ContainerPath: "/opt/b"}}
	datastore, _ := mountedService(t, existing)

	_, err := UnmountService(UnmountServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Specs:       []string{"/srv/a:/opt/a", "/srv/c:/opt/c"},
	})
	if err == nil {
		t.Fatal("expected the unmount to be refused")
	}

	if actual := readMounts(t, datastore); !reflect.DeepEqual(actual, existing) {
		t.Errorf("expected the mounts to be left as %+v, got %+v", existing, actual)
	}
}

// Nothing is checked against the host on the way out, so a mount whose host
// path has gone can still be removed
func TestUnmountServiceRemovesAMountWhoseHostPathIsGone(t *testing.T) {
	existing := []service.Mount{{Source: "/does/not/exist", ContainerPath: "/opt/a"}}
	datastore, _ := mountedService(t, existing)

	if _, err := UnmountService(UnmountServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Specs:       []string{"/does/not/exist:/opt/a"},
	}); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if actual := readMounts(t, datastore); len(actual) != 0 {
		t.Errorf("expected no mounts, got %+v", actual)
	}

	// and --all a second time has nothing to do, which is not an error
	for range 2 {
		if _, err := UnmountService(UnmountServiceInput{Datastore: datastore, ServiceName: "lollipop", All: true}); err != nil {
			t.Errorf("unexpected error: %s", err)
		}
	}
}

func TestParseMountSpecsSkipsAnEmptySpec(t *testing.T) {
	mounts, err := ParseMountSpecs([]string{"", "/srv/a:/opt/a:ro,z"})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []service.Mount{{Source: "/srv/a", ContainerPath: "/opt/a", Readonly: true, VolumeOptions: "z"}}
	if !reflect.DeepEqual(mounts, expected) {
		t.Errorf("expected %+v, got %+v", expected, mounts)
	}

	if _, err := ParseMountSpecs([]string{"/srv/a"}); err == nil {
		t.Error("expected a malformed spec to be refused")
	}
}

// A mount docker would refuse, or would create an empty directory for, is
// refused before the service root is written
func TestCreateServiceRefusesAnUnusableMount(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	err := CreateService(t.Context(), CreateServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Mounts:      []service.Mount{{Source: filepath.Join(t.TempDir(), "missing"), ContainerPath: "/opt/a"}},
	})
	if err == nil {
		t.Fatal("expected a missing host path to be refused, got no error")
	}

	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected the error to name the host path, got %q", err)
	}

	if root := service.Folders(datastore, "lollipop").Root; common.DirectoryExists(root) {
		t.Errorf("a refused create left %s behind", root)
	}
}

func TestUpgradeChangesSettingsWithMounts(t *testing.T) {
	none := []service.Mount{}
	if !(UpgradeServiceInput{Mounts: &none}).changesSettings() {
		t.Error("expected clearing the mounts to count as a change")
	}
}

// The mounts an upgrade will build the container with are checked before the
// old container is taken away: the ones asked for, and otherwise the ones the
// service already has, since a host path removed since it was mounted is found
// here rather than once there is no container to go back to
func TestCheckUpgradeMounts(t *testing.T) {
	gone := []service.Mount{{Source: "/does/not/exist", ContainerPath: "/opt/a"}}
	datastore, source := mountedService(t, gone)

	input := UpgradeServiceInput{Datastore: datastore, ServiceName: "lollipop"}
	err := checkUpgradeMounts(input, "redis:8.4.2")
	if err == nil || !strings.Contains(err.Error(), "Host path /does/not/exist does not exist") {
		t.Errorf("expected the stored mount to be refused, got %v", err)
	}

	replacement := []service.Mount{{Source: source, ContainerPath: "/opt/a"}}
	input.Mounts = &replacement
	if err := checkUpgradeMounts(input, "redis:8.4.2"); err != nil {
		t.Errorf("expected the mounts asked for to be checked instead, got %v", err)
	}

	clash := []service.Mount{{Source: source, ContainerPath: "/data"}}
	input.Mounts = &clash
	if err := checkUpgradeMounts(input, "redis:8.4.2"); err == nil {
		t.Error("expected a directory the definition mounts to be refused")
	}
}

func TestApplyUpgradeSettingsWritesTheMounts(t *testing.T) {
	datastore, _ := mountedService(t, []service.Mount{{Source: "/srv/a", ContainerPath: "/opt/a"}})

	// left alone when the upgrade was not asked about them
	if err := applyUpgradeSettings(UpgradeServiceInput{Datastore: datastore, ServiceName: "lollipop"}); err != nil {
		t.Fatalf("failed to apply the settings: %s", err)
	}
	if actual := readMounts(t, datastore); len(actual) != 1 {
		t.Errorf("expected the mounts to be kept, got %+v", actual)
	}

	replacement := []service.Mount{{Source: "data", ContainerPath: "/opt/data"}}
	if err := applyUpgradeSettings(UpgradeServiceInput{Datastore: datastore, ServiceName: "lollipop", Mounts: &replacement}); err != nil {
		t.Fatalf("failed to apply the settings: %s", err)
	}
	if actual := readMounts(t, datastore); !reflect.DeepEqual(actual, replacement) {
		t.Errorf("expected %+v, got %+v", replacement, actual)
	}
}

func TestInfoReportsTheMounts(t *testing.T) {
	datastore, _ := mountedService(t, []service.Mount{
		{Source: "/srv/a", ContainerPath: "/opt/a", Readonly: true, Subpath: "uploads"},
		{Source: "data", ContainerPath: "/opt/data"},
	})

	info := Info(context.Background(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	if expected := "/srv/a:/opt/a:ro,volume-subpath=uploads data:/opt/data"; info["mounts"] != expected {
		t.Errorf("expected %q, got %q", expected, info["mounts"])
	}
}
