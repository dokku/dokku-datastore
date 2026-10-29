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
			// the probe shares the service's network namespace, which reaches
			// it whatever network it is on and without a container link
			name: "a service with an address",
			input: WaitArgsInput{
				ContainerName: "dokku.redis.lollipop",
				Host:          "172.17.0.2",
				Port:          6379,
			},
			expected: "container run --rm --network=container:dokku.redis.lollipop " + hostenv.WaitImage + " -c 172.17.0.2:6379",
		},
		{
			// a container whose address could not be read is still probed, on
			// the loopback interface it shares with the probe
			name: "a service with no address",
			input: WaitArgsInput{
				ContainerName: "dokku.redis.lollipop",
				Port:          6379,
			},
			expected: "container run --rm --network=container:dokku.redis.lollipop " + hostenv.WaitImage + " -c 127.0.0.1:6379",
		},
		{
			// a timeout is handed to the probe after what it connects to, and
			// none at all leaves the probe's own default in place
			name: "a service waited on for longer than the default",
			input: WaitArgsInput{
				ContainerName: "dokku.redis.lollipop",
				Host:          "172.17.0.2",
				Port:          6379,
				Timeout:       90,
			},
			expected: "container run --rm --network=container:dokku.redis.lollipop " + hostenv.WaitImage + " -c 172.17.0.2:6379 -t 90",
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

// The timeout is said when there is one, so an operator who raised it can see
// that it took.
func TestWaitHeader(t *testing.T) {
	if header := WaitHeader("lollipop", 90); header != "Waiting for lollipop container to be ready (timeout: 90s)" {
		t.Errorf("unexpected header %q", header)
	}

	if header := WaitHeader("lollipop", 0); header != "Waiting for lollipop container to be ready" {
		t.Errorf("unexpected header %q", header)
	}
}

// The probe image is pinned rather than floating, so a service that waited
// yesterday cannot start failing because a tag moved.
func TestWaitArgsUsesThePinnedProbe(t *testing.T) {
	args := WaitArgs(WaitArgsInput{ContainerName: "c", Host: "a", Port: 1})

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
		ContainerName: "dokku.redis.lollipop",
		Host:          "172.17.0.2",
		Port:          6379,
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

	if strings.Join(args[image+1:], " ") != "-c 172.17.0.2:6379" {
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
