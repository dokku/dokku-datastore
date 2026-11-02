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

	created, err := EnsureLinkNetwork(t.Context(), EnsureLinkNetworkInput{
		AppName:     "my-app",
		ContainerID: "abc123",
		Datastore:   redisDatastore(t),
		ServiceName: "lollipop",
	})
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}
	if !created {
		t.Error("expected the network to be reported as made")
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

	created, err := EnsureLinkNetwork(t.Context(), EnsureLinkNetworkInput{
		AppName:     "my-app",
		ContainerID: "abc123",
		Datastore:   redisDatastore(t),
		ServiceName: "lollipop",
	})
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}
	if created {
		t.Error("expected a network that was already there not to be reported as made")
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

// poolDocker stands in for a docker that has run out of address pools, or that
// fails a network create for another reason when exhausted is false. liveRestore
// is what docker info reports, and a daemon that cannot be asked when empty.
func poolDocker(t *testing.T, exhausted bool, liveRestore string) string {
	t.Helper()

	createError := "Error response from daemon: no space left on device"
	if exhausted {
		createError = "Error response from daemon: all predefined address pools have been fully subnetted"
	}

	info := `echo "Cannot connect to the Docker daemon" >&2; exit 1`
	if liveRestore != "" {
		info = "echo " + liveRestore
	}

	root := t.TempDir()
	calls := filepath.Join(root, "calls")
	script := `#!/usr/bin/env bash
echo "$*" >>"` + calls + `"
case "$1 $2" in
  "network create") echo '` + createError + `' >&2; exit 1 ;;
  "info --format") ` + info + ` ;;
esac
`
	docker := filepath.Join(root, "docker")
	if err := os.WriteFile(docker, []byte(script), 0755); err != nil {
		t.Fatalf("unable to write the fake docker: %s", err)
	}

	t.Setenv("DOCKER_BIN", docker)
	return calls
}

// A host out of address pools is told what to change and how to apply it,
// rather than handed docker's error, and the network a link already had is
// said to have gone missing rather than to be new
func TestEnsureLinkNetworkExplainsExhaustedAddressPools(t *testing.T) {
	tests := []struct {
		name        string
		recreate    bool
		liveRestore string
		expected    []string
		unexpected  []string
	}{
		{
			name:        "a new network with live restore off",
			liveRestore: "false",
			expected: []string{
				"Unable to create network dokku.redis.lollipop.my-app for the link to app my-app: docker has no address pool left to create it from",
				`"default-address-pools": [{"base": "172.16.0.0/12", "size": 24}]`,
				"/etc/docker/daemon.json",
				"sudo systemctl restart docker (a reload does not apply it)",
				`Set "live-restore": true and run sudo systemctl reload docker first`,
				LinkNetworksReadme,
			},
		},
		{
			name:        "a recreated network with live restore on",
			recreate:    true,
			liveRestore: "true",
			expected: []string{
				"Unable to recreate network dokku.redis.lollipop.my-app for the link to app my-app, which was removed since the app was linked: docker has no address pool left to create it from",
				"sudo systemctl restart docker",
			},
			unexpected: []string{"live-restore"},
		},
		{
			name:     "a daemon that cannot say whether live restore is on",
			expected: []string{`Set "live-restore": true`},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			poolDocker(t, true, test.liveRestore)

			created, err := EnsureLinkNetwork(t.Context(), EnsureLinkNetworkInput{
				AppName:     "my-app",
				Datastore:   redisDatastore(t),
				Recreate:    test.recreate,
				ServiceName: "lollipop",
			})
			if !errors.Is(err, ErrAddressPoolsExhausted) {
				t.Fatalf("expected the address pools to be reported as exhausted, got %v", err)
			}
			if created {
				t.Error("expected a network that could not be made not to be reported as made")
			}

			for _, expected := range test.expected {
				if !strings.Contains(err.Error(), expected) {
					t.Errorf("expected %q in:\n%s", expected, err)
				}
			}

			for _, unexpected := range test.unexpected {
				if strings.Contains(err.Error(), unexpected) {
					t.Errorf("expected no %q in:\n%s", unexpected, err)
				}
			}
		})
	}
}

// Any other failure to make the network keeps docker's own error, said the
// same way whether the network is new or made again
func TestEnsureLinkNetworkReportsOtherFailures(t *testing.T) {
	for _, test := range []struct {
		recreate bool
		prefix   string
	}{
		{false, "Unable to create network dokku.redis.lollipop.my-app for the link to app my-app: "},
		{true, "Unable to recreate network dokku.redis.lollipop.my-app for the link to app my-app, which was removed since the app was linked: "},
	} {
		calls := poolDocker(t, false, "")

		_, err := EnsureLinkNetwork(t.Context(), EnsureLinkNetworkInput{
			AppName:     "my-app",
			Datastore:   redisDatastore(t),
			Recreate:    test.recreate,
			ServiceName: "lollipop",
		})
		if err == nil || errors.Is(err, ErrAddressPoolsExhausted) {
			t.Fatalf("expected a plain failure, got %v", err)
		}
		if !strings.HasPrefix(err.Error(), test.prefix) || !strings.Contains(err.Error(), "no space left on device") {
			t.Errorf("expected %q followed by docker's error, got %q", test.prefix, err)
		}

		// docker is only asked about live restore for the advice that goes
		// with exhausted pools
		for _, call := range linkNetworkCalls(t, calls) {
			if strings.HasPrefix(call, "info") {
				t.Errorf("expected docker info not to be asked, got %q", call)
			}
		}
	}
}
