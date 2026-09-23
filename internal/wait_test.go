package internal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
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

// A definition that declares no ports has nothing to connect to. Every
// definition shipped today declares at least one, so this is here to keep a
// future one from probing port zero and waiting out the timeout - and it
// returns before anything is run, which is why it needs no docker daemon.
func TestWaitForServiceSkipsADefinitionWithNoWaitPort(t *testing.T) {
	redis, ok := service.Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	portless := &service.Datastore{Definition: redis.Definition}
	portless.Definition.Service.Ports = nil

	if port := portless.Properties().WaitPort; port != 0 {
		t.Fatalf("expected a definition with no ports to have no wait port, got %d", port)
	}

	// a zero value logger, which would panic if the wait got as far as
	// announcing itself
	if err := WaitForService(context.Background(), WaitForServiceInput{
		Datastore:   portless,
		ServiceName: "lollipop",
	}); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// The commands that wait wire SIGINT to the context, so an operator who gives
// up on a service that is not coming back gets their terminal returned rather
// than the rest of the timeout.
func TestWaitForRunningContainerStopsWhenTheContextIsCancelled(t *testing.T) {
	redis, ok := service.Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	err := waitForRunningContainer(ctx, WaitForServiceInput{
		Datastore:   redis,
		ServiceName: "a-service-that-does-not-exist",
	})
	if err == nil {
		t.Error("expected the cancelled context to be reported")
	}

	if elapsed := time.Since(started); elapsed > RunningContainerTimeout/2 {
		t.Errorf("expected the wait to give up at once, took %s", elapsed)
	}
}
