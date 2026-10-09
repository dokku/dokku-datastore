package backend

import (
	"context"
	"testing"
)

func TestNetworkIP(t *testing.T) {
	tests := []struct {
		name     string
		networks string
		network  string
		expected string
	}{
		{
			name:     "the default bridge",
			networks: `{"bridge":{"IPAddress":"172.17.0.2"}}`,
			expected: "172.17.0.2",
		},
		{
			// a service attached to post-create and post-start networks reports
			// the network it was created on, even when another sorts first
			name:     "the initial network wins over the networks attached later",
			networks: `{"aaa-attached":{"IPAddress":"10.0.1.2"},"initial":{"IPAddress":"10.0.0.2"},"zzz-attached":{"IPAddress":"10.0.2.2"}}`,
			network:  "initial",
			expected: "10.0.0.2",
		},
		{
			name:     "an initial network with no address falls back to the bridge",
			networks: `{"bridge":{"IPAddress":"172.17.0.2"},"initial":{"IPAddress":""}}`,
			network:  "initial",
			expected: "172.17.0.2",
		},
		{
			name:     "an initial network the container is not on falls back to the first by name",
			networks: `{"zzz-attached":{"IPAddress":"10.0.2.2"},"aaa-attached":{"IPAddress":"10.0.1.2"}}`,
			network:  "initial",
			expected: "10.0.1.2",
		},
		{
			name:     "no initial network and no bridge is the first by name",
			networks: `{"zzz-attached":{"IPAddress":"10.0.2.2"},"aaa-attached":{"IPAddress":"10.0.1.2"}}`,
			expected: "10.0.1.2",
		},
		{
			name:     "a network with no address is skipped",
			networks: `{"aaa-attached":{"IPAddress":""},"zzz-attached":{"IPAddress":"10.0.2.2"}}`,
			expected: "10.0.2.2",
		},
		{
			name:     "no networks at all",
			networks: `{}`,
			expected: "",
		},
		{
			name:     "a null network list",
			networks: `null`,
			expected: "",
		},
		{
			// what an inspect of a container that is not there leaves behind
			name:     "empty output",
			networks: "",
			expected: "",
		},
		{
			name:     "output that is not json",
			networks: "Error: No such object: dokku.redis.gone",
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := NetworkIP(test.networks, test.network); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// A stopped service has no container id, and there is nothing to inspect
func TestIPWithoutAContainer(t *testing.T) {
	if actual := IP(context.Background(), "", "initial"); actual != "" {
		t.Errorf("expected no address, got %q", actual)
	}
}

// A container's bind mounts are read by the host path they were given, which
// is how a service's data volume is found among them. The volumes docker
// manages itself have no host path a service names, and are left out.
func TestMountDestinations(t *testing.T) {
	mounts := `[
		{"Type":"bind","Source":"/var/lib/dokku/services/postgres/db/data","Destination":"/var/lib/postgresql/data"},
		{"Type":"bind","Source":"/var/lib/dokku/services/postgres/db/certs","Destination":"/certs"},
		{"Type":"volume","Name":"0f1e","Source":"/var/lib/docker/volumes/0f1e/_data","Destination":"/scratch"}
	]`

	found := MountDestinations(mounts)
	expected := map[string]string{
		"/var/lib/dokku/services/postgres/db/data":  "/var/lib/postgresql/data",
		"/var/lib/dokku/services/postgres/db/certs": "/certs",
	}
	if len(found) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, found)
	}

	for source, destination := range expected {
		if found[source] != destination {
			t.Errorf("expected %s mounted at %s, got %q", source, destination, found[source])
		}
	}

	// nothing to read is no mounts rather than an error
	if found := MountDestinations(""); len(found) != 0 {
		t.Errorf("expected no mounts, got %v", found)
	}
}

// A service with no container has nothing to inspect
func TestMountsWithoutAContainer(t *testing.T) {
	if found := Mounts(context.Background(), ""); len(found) != 0 {
		t.Errorf("expected no mounts, got %v", found)
	}
}
