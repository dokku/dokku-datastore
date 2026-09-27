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
