package backend

import (
	"strings"
	"testing"
)

func TestRunArgs(t *testing.T) {
	tests := []struct {
		name     string
		input    RunInput
		expected string
	}{
		{
			name:     "a bare command",
			input:    RunInput{Image: "redis:8.8.0", Argv: []string{"redis-cli"}},
			expected: "container run --rm -i redis:8.8.0 redis-cli",
		},
		{
			// the service's own mounts, which is how a command reaches the data
			// with the service container down
			name: "the service's volumes",
			input: RunInput{
				Image: "redis:8.8.0",
				Argv:  []string{"sh", "-c", "cat > /data/dump.rdb"},
				Volumes: []string{
					"/var/lib/dokku/services/redis/lollipop/config:/usr/local/etc/redis",
					"/var/lib/dokku/services/redis/lollipop/data:/data",
				},
			},
			expected: "container run --rm --volume=/var/lib/dokku/services/redis/lollipop/config:/usr/local/etc/redis --volume=/var/lib/dokku/services/redis/lollipop/data:/data -i redis:8.8.0 sh -c cat > /data/dump.rdb",
		},
		{
			name: "environment variables come out sorted",
			input: RunInput{
				Image: "redis:8.8.0",
				Argv:  []string{"dokku-redis-export"},
				Env:   map[string]string{"REDISCLI_AUTH": "hunter2", "LANG": "C.UTF-8"},
			},
			expected: "container run --rm --env=LANG=C.UTF-8 --env=REDISCLI_AUTH=hunter2 -i redis:8.8.0 dokku-redis-export",
		},
		{
			name: "a sidecar on the service's network",
			input: RunInput{
				Image:   "busybox:1.37.0",
				Argv:    []string{"telnet", "dokku-memcached-lollipop", "11211"},
				Network: "bridge",
				User:    "nobody",
				TTY:     true,
			},
			expected: "container run --rm --network=bridge --user=nobody -i -t busybox:1.37.0 telnet dokku-memcached-lollipop 11211",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := strings.Join(RunArgs(test.input), " "); actual != test.expected {
				t.Errorf("expected:\n%s\ngot:\n%s", test.expected, actual)
			}
		})
	}
}

func TestRunArgsPutsTheCommandLast(t *testing.T) {
	// anything after the image is the command, so a flag appearing there would
	// be passed to the command rather than to docker
	args := RunArgs(RunInput{
		Image:   "redis:8.8.0",
		Argv:    []string{"sh", "-c", "--", "echo hello"},
		Env:     map[string]string{"A": "1"},
		Volumes: []string{"/data:/data"},
		TTY:     true,
	})

	image := -1
	for i, arg := range args {
		if arg == "redis:8.8.0" {
			image = i
		}
	}

	if image == -1 {
		t.Fatalf("the image is not in the arguments: %v", args)
	}

	if strings.Join(args[image+1:], " ") != "sh -c -- echo hello" {
		t.Errorf("expected the command to follow the image, got %v", args[image+1:])
	}
}

func TestRunRejectsAnIncompleteInvocation(t *testing.T) {
	tests := []struct {
		name  string
		input RunInput
	}{
		{name: "no image", input: RunInput{Argv: []string{"true"}}},
		{name: "no command", input: RunInput{Image: "redis:8.8.0"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Run(t.Context(), test.input); err == nil {
				t.Error("expected an error rather than a docker invocation")
			}
		})
	}
}
