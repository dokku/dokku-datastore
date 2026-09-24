package service

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseMountSpec(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		expected Mount
		fields   MountFields
	}{
		{
			name:     "a host path",
			spec:     "/srv/hunspell:/usr/share/elasticsearch/config/hunspell",
			expected: Mount{Source: "/srv/hunspell", ContainerPath: "/usr/share/elasticsearch/config/hunspell"},
		},
		{
			name:     "a docker volume",
			spec:     "hunspell:/opt/hunspell",
			expected: Mount{Source: "hunspell", ContainerPath: "/opt/hunspell"},
		},
		{
			name:     "a trailing colon is no options rather than an empty one",
			spec:     "/srv/a:/opt/a:",
			expected: Mount{Source: "/srv/a", ContainerPath: "/opt/a"},
		},
		{
			name:     "ro is lifted out of the docker options",
			spec:     "/srv/a:/opt/a:z,ro,nocopy",
			expected: Mount{Source: "/srv/a", ContainerPath: "/opt/a", Readonly: true, VolumeOptions: "z,nocopy"},
			fields:   MountFields{Readonly: true, VolumeOptions: true},
		},
		{
			name:     "rw is the explicit opposite of ro",
			spec:     "/srv/a:/opt/a:rw",
			expected: Mount{Source: "/srv/a", ContainerPath: "/opt/a"},
			fields:   MountFields{Readonly: true},
		},
		{
			name:     "the recorded keys",
			spec:     "/srv/a:/opt/a:volume-subpath=uploads,volume-chown=herokuish",
			expected: Mount{Source: "/srv/a", ContainerPath: "/opt/a", Subpath: "uploads", Chown: "herokuish"},
			fields:   MountFields{Subpath: true, Chown: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mount, fields, err := ParseMountSpec(test.spec)
			if err != nil {
				t.Fatalf("expected %q to parse, got %v", test.spec, err)
			}

			if !reflect.DeepEqual(mount, test.expected) {
				t.Errorf("expected %+v, got %+v", test.expected, mount)
			}

			if fields != test.fields {
				t.Errorf("expected fields %+v, got %+v", test.fields, fields)
			}
		})
	}
}

// Order within an option list is not significant, so anything it can say twice
// is refused rather than resolved by position, and a key nothing reads is
// refused rather than handed to docker.
func TestParseMountSpecRefusesAMalformedSpec(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		expected string
	}{
		{name: "no container dir", spec: "/srv/a", expected: "Invalid mount specified"},
		{name: "an empty source", spec: ":/opt/a", expected: "Invalid mount specified"},
		{name: "an empty container dir", spec: "/srv/a::ro", expected: "Invalid mount specified"},
		{name: "an empty option", spec: "/srv/a:/opt/a:ro,,z", expected: "has an empty mount option"},
		{name: "both ro and rw", spec: "/srv/a:/opt/a:ro,rw", expected: "sets both ro and rw"},
		{name: "an unknown key", spec: "/srv/a:/opt/a:phase=deploy", expected: `specifies unknown key "phase"`},
		{name: "an empty value", spec: "/srv/a:/opt/a:volume-subpath=", expected: "has an empty value for volume-subpath"},
		{name: "a key given twice", spec: "/srv/a:/opt/a:volume-chown=root,volume-chown=heroku", expected: "specifies volume-chown more than once"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := ParseMountSpec(test.spec)
			if err == nil {
				t.Fatalf("expected %q to be refused", test.spec)
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %q", test.expected, err)
			}
		})
	}
}

// docker only refuses a malformed mount when the container is made, which for
// an upgrade is after the old one is gone
func TestValidateMount(t *testing.T) {
	tests := []struct {
		name     string
		mount    Mount
		expected string
	}{
		{name: "a host path", mount: Mount{Source: "/srv/a", ContainerPath: "/opt/a"}},
		{name: "a docker volume", mount: Mount{Source: "my-volume.1", ContainerPath: "/opt/a"}},
		{name: "every group once", mount: Mount{Source: "/srv/a", ContainerPath: "/opt/a", VolumeOptions: "Z,rslave,nocopy,cached"}},
		{name: "a numeric chown", mount: Mount{Source: "/srv/a", ContainerPath: "/opt/a", Chown: "1000"}},
		{name: "a nested subpath", mount: Mount{Source: "/srv/a", ContainerPath: "/opt/a", Subpath: "one/two"}},
		{name: "a relative source", mount: Mount{Source: "srv/a", ContainerPath: "/opt/a"}, expected: "must be an absolute host path or a docker volume name"},
		{name: "a one character volume", mount: Mount{Source: "a", ContainerPath: "/opt/a"}, expected: "must be an absolute host path or a docker volume name"},
		{name: "a relative container dir", mount: Mount{Source: "/srv/a", ContainerPath: "opt/a"}, expected: `Container path "opt/a" must be absolute`},
		{name: "the container root", mount: Mount{Source: "/srv/a", ContainerPath: "/"}, expected: "Container path must not be /"},
		{name: "an option docker does not take", mount: Mount{Source: "/srv/a", ContainerPath: "/opt/a", VolumeOptions: "noexec"}, expected: `Volume option "noexec" is not one docker takes`},
		{name: "ro as a volume option", mount: Mount{Source: "/srv/a", ContainerPath: "/opt/a", VolumeOptions: "ro"}, expected: `Volume option "ro" is not a volume option`},
		{name: "two from one group", mount: Mount{Source: "/srv/a", ContainerPath: "/opt/a", VolumeOptions: "z,Z"}, expected: `Volume options "z" and "Z" cannot be used together`},
		{name: "an absolute subpath", mount: Mount{Source: "/srv/a", ContainerPath: "/opt/a", Subpath: "/etc"}, expected: "must be relative"},
		{name: "a subpath that leaves the source", mount: Mount{Source: "/srv/a", ContainerPath: "/opt/a", Subpath: "one/../../etc"}, expected: "must not leave the mount source"},
		{name: "an unknown chown", mount: Mount{Source: "/srv/a", ContainerPath: "/opt/a", Chown: "nobody"}, expected: "Unsupported chown permissions"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateMount(test.mount)
			if test.expected == "" {
				if err != nil {
					t.Errorf("expected %+v to be valid, got %v", test.mount, err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected %+v to be refused", test.mount)
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %q", test.expected, err)
			}
		})
	}
}

// docker is handed the options it understands and nothing else, the way dokku's
// storage plugin renders a docker-local mount, while the spec shows every field
func TestMountVolumeAndSpec(t *testing.T) {
	mount := Mount{
		Source:        "/srv/a",
		ContainerPath: "/opt/a",
		Readonly:      true,
		VolumeOptions: "z",
		Subpath:       "uploads",
		Chown:         "herokuish",
	}

	if volume := mount.Volume(); volume != "/srv/a:/opt/a:ro,z" {
		t.Errorf("expected the volume /srv/a:/opt/a:ro,z, got %s", volume)
	}

	spec := mount.Spec()
	if spec != "/srv/a:/opt/a:ro,z,volume-subpath=uploads,volume-chown=herokuish" {
		t.Errorf("unexpected spec %s", spec)
	}

	// what info shows can be handed back to the mount command
	parsed, _, err := ParseMountSpec(spec)
	if err != nil {
		t.Fatalf("expected the spec to parse, got %v", err)
	}
	if !reflect.DeepEqual(parsed, mount) {
		t.Errorf("expected the spec to read back as %+v, got %+v", mount, parsed)
	}

	plain := Mount{Source: "data", ContainerPath: "/opt/data"}
	if plain.Volume() != "data:/opt/data" || plain.Spec() != "data:/opt/data" {
		t.Errorf("expected a mount with no options to have none, got %s and %s", plain.Volume(), plain.Spec())
	}
}

func TestReservedMountTargets(t *testing.T) {
	targets := ReservedMountTargets(redisDatastore(t).Definition)

	for _, expected := range []string{"/data", "/usr/local/etc/redis", "/usr/local/bin/dokku-redis-export"} {
		found := false
		for _, target := range targets {
			if target == expected {
				found = true
			}
		}

		if !found {
			t.Errorf("expected %s to be reserved, got %v", expected, targets)
		}
	}
}

func TestCheckMounts(t *testing.T) {
	redis := redisDatastore(t)
	source := t.TempDir()
	missing := filepath.Join(source, "missing")

	tests := []struct {
		name     string
		mounts   []Mount
		expected string
	}{
		{
			name:   "an existing host path and a docker volume",
			mounts: []Mount{{Source: source, ContainerPath: "/opt/a"}, {Source: "some-volume", ContainerPath: "/opt/b"}},
		},
		{
			// the case from the issue: a directory added inside one the
			// definition mounts
			name:   "a mount below a definition volume",
			mounts: []Mount{{Source: source, ContainerPath: "/data/extra"}},
		},
		{
			name:     "a definition volume",
			mounts:   []Mount{{Source: source, ContainerPath: "/data/"}},
			expected: "Container path /data/ is already mounted by the redis definition",
		},
		{
			name:     "a payload file",
			mounts:   []Mount{{Source: source, ContainerPath: "/usr/local/bin/dokku-redis-export"}},
			expected: "is already mounted by the redis definition",
		},
		{
			name:     "one container dir twice",
			mounts:   []Mount{{Source: source, ContainerPath: "/opt/a"}, {Source: "other", ContainerPath: "/opt/a/"}},
			expected: "Container path /opt/a/ is specified more than once",
		},
		{
			name:     "a host path that does not exist",
			mounts:   []Mount{{Source: missing, ContainerPath: "/opt/a"}},
			expected: "Host path " + missing + " does not exist",
		},
		{
			name:     "a malformed mount",
			mounts:   []Mount{{Source: source, ContainerPath: "/opt/a", VolumeOptions: "noexec"}},
			expected: "is not one docker takes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := CheckMounts(redis.Definition, test.mounts)
			if test.expected == "" {
				if err != nil {
					t.Errorf("expected the mounts to be accepted, got %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected the mounts to be refused")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %q", test.expected, err)
			}
		})
	}
}

// On a docker-in-docker install the host path is one dockerd resolves and this
// process cannot see, so its absence says nothing
func TestCheckMountsLeavesAHostItCannotSeeAlone(t *testing.T) {
	t.Setenv("DOKKU_LIB_ROOT", "/var/lib/dokku")
	t.Setenv("DOKKU_LIB_HOST_ROOT", "/srv/dokku")

	mounts := []Mount{{Source: filepath.Join(t.TempDir(), "missing"), ContainerPath: "/opt/a"}}
	if err := CheckMounts(redisDatastore(t).Definition, mounts); err != nil {
		t.Errorf("expected a host path on a docker-in-docker install not to be checked, got %v", err)
	}
}

// Stored as json rather than joined on a comma, because a mount's own options
// are joined on one
func TestServiceMountsRoundTrip(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	mounts, err := ServiceMounts(redis, "lollipop")
	if err != nil || len(mounts) != 0 {
		t.Fatalf("expected a service with no mounts to report none, got %v and %v", mounts, err)
	}

	written := []Mount{
		{Source: "/srv/a", ContainerPath: "/opt/a", Readonly: true, VolumeOptions: "z,nocopy"},
		{Source: "data", ContainerPath: "/opt/data", Subpath: "one", Chown: "root"},
	}
	if err := WriteMounts(redis, "lollipop", written); err != nil {
		t.Fatalf("failed to write the mounts: %v", err)
	}

	read, err := ServiceMounts(redis, "lollipop")
	if err != nil {
		t.Fatalf("failed to read the mounts: %v", err)
	}
	if !reflect.DeepEqual(read, written) {
		t.Errorf("expected %+v, got %+v", written, read)
	}

	if specs := MountSpecs(read); specs != "/srv/a:/opt/a:ro,z,nocopy data:/opt/data:volume-subpath=one,volume-chown=root" {
		t.Errorf("unexpected specs %s", specs)
	}

	if err := WriteMounts(redis, "lollipop", nil); err != nil {
		t.Fatalf("failed to clear the mounts: %v", err)
	}

	read, err = ServiceMounts(redis, "lollipop")
	if err != nil || len(read) != 0 {
		t.Errorf("expected no mounts once cleared, got %v and %v", read, err)
	}
}

func TestCommitServiceConfigWritesTheMounts(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	mounts := []Mount{{Source: "/srv/a", ContainerPath: "/opt/a", Readonly: true}}
	if err := CommitServiceConfig(CommitServiceConfigInput{
		Datastore:    redis,
		Image:        "redis",
		ImageVersion: "8.9.0",
		Mounts:       mounts,
		ServiceName:  "lollipop",
	}); err != nil {
		t.Fatalf("failed to commit the service config: %v", err)
	}

	read, err := ServiceMounts(redis, "lollipop")
	if err != nil {
		t.Fatalf("failed to read the mounts: %v", err)
	}
	if !reflect.DeepEqual(read, mounts) {
		t.Errorf("expected %+v, got %+v", mounts, read)
	}
}
