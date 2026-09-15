package definition

import "testing"

func TestServicePath(t *testing.T) {
	subject := Definition{
		Service: Service{
			Volumes: []Volume{
				{Type: "bind", Source: HostRootTemplate + "/config", Target: "/usr/local/etc/redis"},
				{Type: "bind", Source: HostRootTemplate + "/data", Target: "/data"},
				{Type: "bind", Source: HostRootTemplate + "/certs", Target: "/data/certs"},
			},
		},
	}

	tests := []struct {
		name     string
		target   string
		expected string
		resolves bool
	}{
		{
			name:     "a file inside a mount",
			target:   "/usr/local/etc/redis/redis.conf",
			expected: "config/redis.conf",
			resolves: true,
		},
		{
			name:     "a file below a mount",
			target:   "/usr/local/etc/redis/conf.d/tuning.conf",
			expected: "config/conf.d/tuning.conf",
			resolves: true,
		},
		{
			// otherwise a certificate would be written into the data directory
			// the datastore container owns, rather than into the one mounted for it
			name:     "the longest matching mount wins",
			target:   "/data/certs/server.crt",
			expected: "certs/server.crt",
			resolves: true,
		},
		{
			name:     "the mount point itself",
			target:   "/data",
			expected: "data",
			resolves: true,
		},
		{
			name:   "a path outside every mount",
			target: "/etc/passwd",
		},
		{
			// a prefix match on the string alone would resolve this, and it is
			// a different directory
			name:   "a sibling whose name starts the same way",
			target: "/database/redis.conf",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, ok := subject.ServicePath(test.target)
			if ok != test.resolves {
				t.Fatalf("expected resolves=%v, got %v", test.resolves, ok)
			}

			if actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}
