package render

import (
	"strings"
	"testing"
)

func TestComposeForRedis(t *testing.T) {
	rendered, err := Compose(redisInput(t))
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	document := string(rendered)

	for _, expected := range []string{
		"container_name: dokku.redis.lollipop",
		"hostname: dokku.redis.lollipop",
		"image: redis:8.8.0",
		"restart: always",
		"dokku: service",
		"dokku.service: redis",
		// compose would otherwise invent a project network, and a service that
		// was on the default bridge would quietly move
		"network_mode: bridge",
	} {
		if !strings.Contains(document, expected) {
			t.Errorf("expected the compose file to contain %q, got:\n%s", expected, document)
		}
	}

	// a container exposes what its image declares and nothing more, which is
	// what the docker path produces. Declaring ports here would exhibit ports
	// the image never had, which mongo showed: it names four and its image
	// declares one.
	for _, absent := range []string{"ports:", "expose:"} {
		if strings.Contains(document, absent) {
			t.Errorf("expected no %s in the rendered file, got:\n%s", absent, document)
		}
	}
}

// The compose file and the docker argv come from one resolution, so a
// difference between them is a bug in the renderer rather than in a backend.
func TestComposeAgreesWithTheArgv(t *testing.T) {
	input := redisInput(t)

	arguments, err := ContainerArgs(input)
	if err != nil {
		t.Fatalf("unable to resolve: %s", err)
	}

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	document := string(rendered)

	if !strings.Contains(document, "image: "+arguments.TaggedImage) {
		t.Errorf("the image differs: argv has %q", arguments.TaggedImage)
	}

	for _, volume := range arguments.Volumes {
		if !strings.Contains(document, "- "+volume) {
			t.Errorf("the compose file is missing the volume %q", volume)
		}
	}

	for _, argument := range arguments.Command {
		if !strings.Contains(document, "- "+argument) {
			t.Errorf("the compose file is missing the command element %q", argument)
		}
	}
}

func TestComposeJoinsANamedNetwork(t *testing.T) {
	input := redisInput(t)
	input.Scope.InitialNetwork = "custom-network"

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	document := string(rendered)

	// external, because dokku's networks are made by dokku: compose must attach
	// to one rather than create it
	for _, expected := range []string{"custom-network:", "external: true", "- dokku-redis-lollipop"} {
		if !strings.Contains(document, expected) {
			t.Errorf("expected %q, got:\n%s", expected, document)
		}
	}

	if strings.Contains(document, "network_mode: bridge") {
		t.Errorf("expected no bridge pin when a network was asked for, got:\n%s", document)
	}
}

func TestComposeEscapesADollarInTheEnvironment(t *testing.T) {
	input := redisInput(t)
	input.Environment = []string{"PRICE=$5", "GREETING=hello"}

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	// compose expands ${...} in a value and docker's env file does not, so a
	// dollar left alone would reach the container as something else
	if !strings.Contains(string(rendered), "PRICE: $$5") {
		t.Errorf("expected the dollar to be escaped, got:\n%s", string(rendered))
	}

	if !strings.Contains(string(rendered), "GREETING: hello") {
		t.Errorf("expected the plain value untouched, got:\n%s", string(rendered))
	}
}

func TestComposeOmitsALimitOfZero(t *testing.T) {
	// the memory file holds "0" for a service that was never given a limit
	input := redisInput(t)
	input.Scope.Memory = "0"

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	if strings.Contains(string(rendered), "memory:") {
		t.Errorf("expected no memory limit, got:\n%s", string(rendered))
	}

	input.Scope.Memory = "512"
	rendered, err = Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	if !strings.Contains(string(rendered), "memory: 512m") {
		t.Errorf("expected a memory limit, got:\n%s", string(rendered))
	}
}
