package service

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// linkNetworkDocker stands in for docker, answering the questions the link
// network helpers ask from what it is given and recording everything it is
// asked to run. networks is what network ls finds, attached what the
// container inspected is already on, mode its network mode, and containers
// what network inspect finds on a network.
func linkNetworkDocker(t *testing.T, networks string, mode string, attached string, containers string) string {
	t.Helper()

	root := t.TempDir()
	calls := filepath.Join(root, "calls")
	script := `#!/usr/bin/env bash
echo "$*" >>"` + calls + `"
case "$1 $2" in
  "network ls") printf '%s' '` + networks + `' ;;
  "network inspect") printf '%s' '` + containers + `' ;;
  "container inspect") printf '%s %s' '` + mode + `' '` + attached + `' ;;
esac
`
	docker := filepath.Join(root, "docker")
	if err := os.WriteFile(docker, []byte(script), 0755); err != nil {
		t.Fatalf("unable to write the fake docker: %s", err)
	}

	t.Setenv("DOCKER_BIN", docker)
	return calls
}

// linkNetworkCalls returns what the fake docker was asked to run
func linkNetworkCalls(t *testing.T, calls string) []string {
	t.Helper()

	contents, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return []string{}
	}
	if err != nil {
		t.Fatalf("unable to read the recorded calls: %s", err)
	}

	return strings.Split(strings.TrimSpace(string(contents)), "\n")
}

// Every link has a network of its own, named for both ends of it, so two apps
// linked to one service never share one and cannot find each other through it.
func TestLinkNetworkName(t *testing.T) {
	redis := redisDatastore(t)

	if actual := LinkNetworkName(redis, "lollipop", "my-app"); actual != "dokku.redis.lollipop.my-app" {
		t.Errorf("unexpected network name %q", actual)
	}

	if LinkNetworkName(redis, "lollipop", "my-app") == LinkNetworkName(redis, "lollipop", "other-app") {
		t.Error("expected two apps linked to one service to have networks of their own")
	}
}

// The network is internal, so joining it changes no container's route to
// anything else, and labeled, so a destroy can find every one a service has.
func TestLinkNetworkCreateArgs(t *testing.T) {
	expected := "network create --internal --label=dokku=service-link --label=dokku.service=redis --label=dokku.service.name=lollipop --label=dokku.service.app=my-app dokku.redis.lollipop.my-app"
	if actual := strings.Join(LinkNetworkCreateArgs(redisDatastore(t), "lollipop", "my-app"), " "); actual != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, actual)
	}
}

// A missing network is made, and the service joins it under the name its url
// points at, which is what the app resolves.
func TestEnsureLinkNetworkCreatesAndAttaches(t *testing.T) {
	calls := linkNetworkDocker(t, "", "bridge", `{"bridge":{}}`, "")

	err := EnsureLinkNetwork(t.Context(), EnsureLinkNetworkInput{
		AppName:     "my-app",
		ContainerID: "abc123",
		Datastore:   redisDatastore(t),
		ServiceName: "lollipop",
	})
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	expected := []string{
		`network ls --quiet --filter name=^dokku\.redis\.lollipop\.my-app$`,
		strings.Join(LinkNetworkCreateArgs(redisDatastore(t), "lollipop", "my-app"), " "),
		"container inspect --format {{ .HostConfig.NetworkMode }} {{ json .NetworkSettings.Networks }} abc123",
		"network connect --alias dokku-redis-lollipop dokku.redis.lollipop.my-app abc123",
	}
	if actual := linkNetworkCalls(t, calls); !slices.Equal(actual, expected) {
		t.Errorf("expected the calls %q, got %q", expected, actual)
	}
}

// Ensuring a link network is repeated on every link, deploy and container
// create, so one that is already in place is left as it is.
func TestEnsureLinkNetworkIsSafeToRepeat(t *testing.T) {
	calls := linkNetworkDocker(t, "0123abcd", "bridge", `{"bridge":{},"dokku.redis.lollipop.my-app":{}}`, "")

	err := EnsureLinkNetwork(t.Context(), EnsureLinkNetworkInput{
		AppName:     "my-app",
		ContainerID: "abc123",
		Datastore:   redisDatastore(t),
		ServiceName: "lollipop",
	})
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	for _, call := range linkNetworkCalls(t, calls) {
		if strings.HasPrefix(call, "network create") || strings.HasPrefix(call, "network connect") {
			t.Errorf("expected nothing to change, got %q", call)
		}
	}
}

// A container on the host's network can join no other, and is refused with an
// error the caller can tell apart from a failure.
func TestConnectAppContainerRefusesTheHostNetwork(t *testing.T) {
	calls := linkNetworkDocker(t, "", "host", "{}", "")

	err := ConnectAppContainer(t.Context(), redisDatastore(t), "lollipop", "my-app", "abc123")
	if !errors.Is(err, ErrHostNetworkContainer) {
		t.Fatalf("expected the host network to be refused, got %v", err)
	}

	for _, call := range linkNetworkCalls(t, calls) {
		if strings.HasPrefix(call, "network connect") {
			t.Errorf("expected no connect, got %q", call)
		}
	}
}

// An app container joins the link network under no alias of its own, as the
// service has no reason to look it up.
func TestConnectAppContainer(t *testing.T) {
	calls := linkNetworkDocker(t, "", "bridge", `{"bridge":{}}`, "")

	if err := ConnectAppContainer(t.Context(), redisDatastore(t), "lollipop", "my-app", "abc123"); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	actual := linkNetworkCalls(t, calls)
	if last := actual[len(actual)-1]; last != "network connect dokku.redis.lollipop.my-app abc123" {
		t.Errorf("expected the container to be connected, got %q", actual)
	}
}

// Removing a link network takes it away from everything on it at once, so an
// app that is unlinked without a restart loses the service straight away.
func TestRemoveLinkNetwork(t *testing.T) {
	calls := linkNetworkDocker(t, "0123abcd", "", "", `{"bbb":{},"aaa":{}}`)

	if err := RemoveLinkNetwork(t.Context(), "dokku.redis.lollipop.my-app"); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	expected := []string{
		`network ls --quiet --filter name=^dokku\.redis\.lollipop\.my-app$`,
		"network inspect --format {{ json .Containers }} dokku.redis.lollipop.my-app",
		"network disconnect --force dokku.redis.lollipop.my-app aaa",
		"network disconnect --force dokku.redis.lollipop.my-app bbb",
		"network rm dokku.redis.lollipop.my-app",
	}
	if actual := linkNetworkCalls(t, calls); !slices.Equal(actual, expected) {
		t.Errorf("expected the calls %q, got %q", expected, actual)
	}
}

// A network that is already gone is what removing it was for.
func TestRemoveLinkNetworkThatIsGone(t *testing.T) {
	calls := linkNetworkDocker(t, "", "", "", "")

	if err := RemoveLinkNetwork(t.Context(), "dokku.redis.lollipop.my-app"); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	if actual := linkNetworkCalls(t, calls); len(actual) != 1 {
		t.Errorf("expected only the lookup, got %q", actual)
	}
}
