package internal

import (
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/hostenv"
)

func TestWaitArgs(t *testing.T) {
	tests := []struct {
		name     string
		input    WaitArgsInput
		expected string
	}{
		{
			name: "a service on the default bridge",
			input: WaitArgsInput{
				ContainerName: "dokku.redis.lollipop",
				NetworkAlias:  "dokku-redis-lollipop",
				Port:          6379,
			},
			expected: "container run --rm --link=dokku.redis.lollipop:dokku-redis-lollipop " + hostenv.WaitImage + " -c dokku-redis-lollipop:6379",
		},
		{
			// the probe has to join the network the service was created on, or
			// it cannot see the service it is waiting for
			name: "a service on its own network",
			input: WaitArgsInput{
				ContainerName:  "dokku.redis.lollipop",
				NetworkAlias:   "dokku-redis-lollipop",
				InitialNetwork: "custom-network",
				Port:           6379,
			},
			expected: "container run --rm --link=dokku.redis.lollipop:dokku-redis-lollipop --network=custom-network " + hostenv.WaitImage + " -c dokku-redis-lollipop:6379",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := strings.Join(WaitArgs(test.input), " "); actual != test.expected {
				t.Errorf("expected:\n%s\ngot:\n%s", test.expected, actual)
			}
		})
	}
}

// The probe image is pinned rather than floating, so a service that waited
// yesterday cannot start failing because a tag moved.
func TestWaitArgsUsesThePinnedProbe(t *testing.T) {
	args := WaitArgs(WaitArgsInput{ContainerName: "c", NetworkAlias: "a", Port: 1})

	if !strings.Contains(strings.Join(args, " "), ":") {
		t.Fatal("expected a tagged probe image")
	}

	for _, arg := range args {
		if arg == hostenv.WaitImage {
			return
		}
	}

	t.Errorf("expected the pinned probe image, got %v", args)
}

// Anything after the image is the probe's own argv, so a flag landing there
// would be read by the probe rather than by docker.
func TestWaitArgsPutsTheProbeArgumentsLast(t *testing.T) {
	args := WaitArgs(WaitArgsInput{
		ContainerName:  "dokku.redis.lollipop",
		NetworkAlias:   "dokku-redis-lollipop",
		InitialNetwork: "custom-network",
		Port:           6379,
	})

	image := -1
	for i, arg := range args {
		if arg == hostenv.WaitImage {
			image = i
		}
	}

	if image == -1 {
		t.Fatalf("the probe image is not in the arguments: %v", args)
	}

	if strings.Join(args[image+1:], " ") != "-c dokku-redis-lollipop:6379" {
		t.Errorf("expected the probe arguments to follow the image, got %v", args[image+1:])
	}
}
