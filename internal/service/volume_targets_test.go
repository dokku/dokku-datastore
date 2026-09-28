package service

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseVolumeTargets(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected map[string]string
	}{
		{name: "nothing moved", value: "", expected: map[string]string{}},
		{name: "one volume", value: "data=/bitnami/postgresql", expected: map[string]string{"data": "/bitnami/postgresql"}},
		{name: "a key with a slash", value: "data/grafana=/grafana", expected: map[string]string{"data/grafana": "/grafana"}},
		{name: "several, however they are spaced", value: "  data=/a \t config=/b ", expected: map[string]string{"data": "/a", "config": "/b"}},
		{name: "a path written loosely", value: "data=/srv//data/", expected: map[string]string{"data": "/srv/data"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			targets, err := ParseVolumeTargets(test.value)
			if err != nil {
				t.Fatalf("expected %q to parse, got %v", test.value, err)
			}

			if !reflect.DeepEqual(targets, test.expected) {
				t.Errorf("expected %v, got %v", test.expected, targets)
			}
		})
	}
}

func TestParseVolumeTargetsRefusesAMalformedValue(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{name: "no equals sign", value: "data", expected: "must be written as <volume>=<container-path>"},
		{name: "no volume", value: "=/data", expected: "must be written as <volume>=<container-path>"},
		{name: "no path", value: "data=", expected: "must be written as <volume>=<container-path>"},
		{name: "a relative path", value: "data=srv/data", expected: "must be absolute"},
		{name: "the container root", value: "data=/", expected: "must not be mounted at /"},
		{name: "a path that cleans to the root", value: "data=/srv/..", expected: "must not be mounted at /"},
		{name: "a colon", value: "data=/srv:ro", expected: "must not contain a colon or a comma"},
		{name: "a comma", value: "data=/srv,ro", expected: "must not contain a colon or a comma"},
		{name: "one volume twice", value: "data=/a data=/b", expected: "moved more than once"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseVolumeTargets(test.value)
			if err == nil {
				t.Fatalf("expected %q to be refused", test.value)
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %q", test.expected, err)
			}
		})
	}
}

// the property and info hold one spelling for one set of targets, whatever
// order they were given in
func TestFormatVolumeTargets(t *testing.T) {
	if formatted := FormatVolumeTargets(map[string]string{"data": "/b", "config": "/a"}); formatted != "config=/a data=/b" {
		t.Errorf("expected the targets sorted by volume, got %q", formatted)
	}

	if formatted := FormatVolumeTargets(nil); formatted != "" {
		t.Errorf("expected no targets to format as nothing, got %q", formatted)
	}

	parsed, err := ParseVolumeTargets(FormatVolumeTargets(map[string]string{"data": "/b", "config": "/a"}))
	if err != nil || !reflect.DeepEqual(parsed, map[string]string{"data": "/b", "config": "/a"}) {
		t.Errorf("expected the formatted targets to parse back, got %v and %v", parsed, err)
	}
}

func TestCheckVolumeTargets(t *testing.T) {
	redis := redisDatastore(t)
	memcached, ok := Datastores["memcached"]
	if !ok {
		t.Fatal("expected memcached to be registered")
	}

	tests := []struct {
		name      string
		datastore *Datastore
		targets   map[string]string
		expected  string
	}{
		{name: "nothing moved", datastore: redis},
		{name: "a volume moved", datastore: redis, targets: map[string]string{"data": "/redis-data"}},
		{name: "a volume moved inside another", datastore: redis, targets: map[string]string{"data": "/usr/local/etc/redis/data"}},
		{name: "both volumes swapped", datastore: redis, targets: map[string]string{"data": "/usr/local/etc/redis", "config": "/data"}},
		{
			name:      "a volume the definition does not have",
			datastore: redis,
			targets:   map[string]string{"certs": "/certs"},
			expected:  "has no volume certs, must be one of [config, data]",
		},
		{
			name:      "a definition with no volumes",
			datastore: memcached,
			targets:   map[string]string{"data": "/data"},
			expected:  "mounts no volumes",
		},
		{
			name:      "onto another volume",
			datastore: redis,
			targets:   map[string]string{"data": "/usr/local/etc/redis"},
			expected:  "the volumes config and data would both be mounted at /usr/local/etc/redis",
		},
		{
			name:      "onto a payload file",
			datastore: redis,
			targets:   map[string]string{"data": "/usr/local/bin/dokku-redis-export"},
			expected:  "which holds the /usr/local/bin/dokku-redis-export",
		},
		{
			// docker would make the payload's mount point inside the volume,
			// on the host, where the data is
			name:      "over a payload file",
			datastore: redis,
			targets:   map[string]string{"data": "/usr/local/bin"},
			expected:  "which holds the /usr/local/bin/dokku-redis-export",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := CheckVolumeTargets(test.datastore.Definition, test.targets)
			if test.expected == "" {
				if err != nil {
					t.Errorf("expected the targets to be accepted, got %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected the targets to be refused")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %q", test.expected, err)
			}
		})
	}
}

func TestServiceVolumeTargetsRoundTrip(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	targets, err := ServiceVolumeTargets(redis, "lollipop")
	if err != nil || targets != nil {
		t.Fatalf("expected a service with no targets to report none, got %v and %v", targets, err)
	}

	written := map[string]string{"data": "/redis-data", "config": "/etc/redis"}
	if err := WriteVolumeTargets(redis, "lollipop", written); err != nil {
		t.Fatalf("failed to write the targets: %v", err)
	}

	read, err := ServiceVolumeTargets(redis, "lollipop")
	if err != nil {
		t.Fatalf("failed to read the targets: %v", err)
	}
	if !reflect.DeepEqual(read, written) {
		t.Errorf("expected %v, got %v", written, read)
	}

	if err := WriteVolumeTargets(redis, "lollipop", nil); err != nil {
		t.Fatalf("failed to clear the targets: %v", err)
	}

	read, err = ServiceVolumeTargets(redis, "lollipop")
	if err != nil || read != nil {
		t.Errorf("expected no targets once cleared, got %v and %v", read, err)
	}
}

// the container is built from what create committed, so the targets have to
// be written by the time it is
func TestCommitServiceConfigWritesTheVolumeTargets(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	if err := CommitServiceConfig(CommitServiceConfigInput{
		Datastore:     redis,
		Image:         "redis",
		ImageVersion:  "8.9.0",
		VolumeTargets: map[string]string{"data": "/redis-data"},
		ServiceName:   "lollipop",
	}); err != nil {
		t.Fatalf("failed to commit the service config: %v", err)
	}

	read, err := ServiceVolumeTargets(redis, "lollipop")
	if err != nil {
		t.Fatalf("failed to read the targets: %v", err)
	}
	if !reflect.DeepEqual(read, map[string]string{"data": "/redis-data"}) {
		t.Errorf("expected the data volume moved, got %v", read)
	}

	if scope := redis.scope("lollipop"); scope.Target["data"] != "/redis-data" || scope.Target["config"] != "/usr/local/etc/redis" {
		t.Errorf("expected the scope to carry the moved target beside the definition's own, got %v", scope.Target)
	}
}
