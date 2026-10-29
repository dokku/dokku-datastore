package internal

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/mitchellh/cli"
)

// linkDocker stands in for docker for a link change. networks is what network
// ls finds, appContainers the running containers of the app, and containers
// what network inspect finds on the link network. Every container inspected is
// on the default bridge alone. It returns the file the calls are recorded to.
func linkDocker(t *testing.T, networks string, appContainers string, containers string) string {
	t.Helper()

	root := t.TempDir()
	calls := filepath.Join(root, "calls")
	script := `#!/usr/bin/env bash
echo "$*" >>"` + calls + `"
case "$1 $2" in
  "network ls") printf '%s' '` + networks + `' ;;
  "network inspect") printf '%s' '` + containers + `' ;;
  "container ls") printf '%s' '` + appContainers + `' ;;
  "container inspect") printf '%s' 'bridge {"bridge":{}}' ;;
esac
`
	docker := filepath.Join(root, "docker")
	if err := os.WriteFile(docker, []byte(script), 0755); err != nil {
		t.Fatalf("unable to write the fake docker: %s", err)
	}

	t.Setenv("DOCKER_BIN", docker)
	return calls
}

// withApp makes an app exist as far as unlink can tell
func withApp(t *testing.T, appName string) {
	t.Helper()

	dokkuRoot := t.TempDir()
	t.Setenv("DOKKU_ROOT", dokkuRoot)
	if err := os.MkdirAll(filepath.Join(dokkuRoot, appName), 0755); err != nil {
		t.Fatalf("failed to create the app root: %s", err)
	}
}

// The option earlier versions linked an app with is what is removed from it,
// so it has to be exactly what they added
func TestLegacyLinkOption(t *testing.T) {
	if actual := LegacyLinkOption(service.Datastores["redis"], "lollipop"); actual != "--link dokku.redis.lollipop:dokku-redis-lollipop" {
		t.Errorf("unexpected option %q", actual)
	}
}

// A link makes the network it runs over and puts the app's running containers
// on it, so the service is reachable without waiting for a restart
func TestLinkServiceJoinsTheAppToItsNetwork(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {}})
	fakeDokku(t, map[string]string{})
	calls := linkDocker(t, "", "web1\nworker1", "")

	if err := LinkService(t.Context(), LinkServiceInput{
		AppName:     "my-app",
		Datastore:   datastore,
		Logger:      Ui{Ui: cli.NewMockUi()},
		NoRestart:   true,
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	actual := recordedDockerCalls(t, calls)
	for _, expected := range []string{
		strings.Join(service.LinkNetworkCreateArgs(datastore, "lollipop", "my-app"), " "),
		"container ls --quiet --no-trunc --filter label=com.dokku.app-name=my-app",
		"network connect dokku.redis.lollipop.my-app web1",
		"network connect dokku.redis.lollipop.my-app worker1",
	} {
		if !slices.Contains(actual, expected) {
			t.Errorf("expected %q among the calls, got %q", expected, actual)
		}
	}

	for _, call := range actual {
		if strings.Contains(call, "--link") {
			t.Errorf("expected no container link, got %q", call)
		}
	}
}

// Unlinking removes the network straight away, which takes the service away
// from the app's running containers even when the app is not restarted
func TestUnlinkServiceRemovesTheNetwork(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"my-app"}})
	withApp(t, "my-app")
	fakeDokku(t, map[string]string{"REDIS_URL": datastore.URL("lollipop", "")})
	calls := linkDocker(t, "0123abcd", "", `{"web1":{},"service1":{}}`)

	if err := UnlinkService(t.Context(), UnlinkServiceInput{
		AppName:     "my-app",
		Datastore:   datastore,
		Logger:      Ui{Ui: cli.NewMockUi()},
		NoRestart:   true,
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	expected := []string{
		`network ls --quiet --filter name=^dokku\.redis\.lollipop\.my-app$`,
		"network inspect --format {{ json .Containers }} dokku.redis.lollipop.my-app",
		"network disconnect --force dokku.redis.lollipop.my-app service1",
		"network disconnect --force dokku.redis.lollipop.my-app web1",
		"network rm dokku.redis.lollipop.my-app",
	}
	if actual := recordedDockerCalls(t, calls); !slices.Equal(actual, expected) {
		t.Errorf("expected the calls %q, got %q", expected, actual)
	}
}

// An app that was deleted still has its network removed, or nothing would
func TestUnlinkServiceFromADeletedAppRemovesTheNetwork(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"gone-app"}})
	t.Setenv("DOKKU_ROOT", t.TempDir())
	calls := linkDocker(t, "0123abcd", "", "{}")

	if err := UnlinkService(t.Context(), UnlinkServiceInput{
		AppName:     "gone-app",
		Datastore:   datastore,
		Logger:      Ui{Ui: cli.NewMockUi()},
		NoRestart:   true,
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	if actual := recordedDockerCalls(t, calls); !slices.Contains(actual, "network rm dokku.redis.lollipop.gone-app") {
		t.Errorf("expected the network to be removed, got %q", actual)
	}
}

// A container dokku makes for an app joins the network of every link the app
// has, and one made for anything else is left alone
func TestConnectContainerToLinks(t *testing.T) {
	tests := []struct {
		name          string
		containerType string
		appName       string
		expected      []string
	}{
		{
			name:          "a container of a linked app",
			containerType: "app",
			appName:       "my-app",
			expected: []string{
				`network ls --quiet --filter name=^dokku\.redis\.lollipop\.my-app$`,
				"container ps -aq --no-trunc --filter name=^/dokku.redis.lollipop$",
				"container inspect --format {{ .HostConfig.NetworkMode }} {{ json .NetworkSettings.Networks }} abc123",
				"network connect dokku.redis.lollipop.my-app abc123",
			},
		},
		{
			name:          "a container of an app with no links",
			containerType: "app",
			appName:       "other-app",
			expected:      []string{},
		},
		{
			name:          "a container that is not an app's",
			containerType: "service",
			appName:       "my-app",
			expected:      []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			datastore := linkedServices(t, map[string][]string{"lollipop": {"my-app"}})
			calls := linkDocker(t, "0123abcd", "", "")

			if err := ConnectContainerToLinks(t.Context(), TriggerInput{
				Datastore: datastore,
				Logger:    Ui{Ui: cli.NewMockUi()},
			}, test.containerType, "abc123", test.appName); err != nil {
				t.Fatalf("expected no error, got %s", err)
			}

			if actual := recordedDockerCalls(t, calls); !slices.Equal(actual, test.expected) {
				t.Errorf("expected the calls %q, got %q", test.expected, actual)
			}
		})
	}
}

// Updating the plugin moves every app that still exists off the container link
// an earlier version gave it and onto the network for its link, without
// restarting anything
func TestMigrateLegacyLinks(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"gone-app", "my-app"}})
	withApp(t, "my-app")
	dokkuCalls := fakeDokku(t, map[string]string{})
	dockerCalls := linkDocker(t, "", "web1", "")
	ui := cli.NewMockUi()

	migrateLegacyLinks(t.Context(), InstallInput{Datastore: datastore, Logger: Ui{Ui: ui}}, "lollipop")

	expected := []string{"docker-options:remove my-app build,deploy,run " + LegacyLinkOption(datastore, "lollipop")}
	if actual := recordedCalls(t, dokkuCalls); !slices.Equal(actual, expected) {
		t.Errorf("expected the calls %q, got %q", expected, actual)
	}

	actual := recordedDockerCalls(t, dockerCalls)
	for _, call := range []string{
		strings.Join(service.LinkNetworkCreateArgs(datastore, "lollipop", "my-app"), " "),
		"network connect dokku.redis.lollipop.my-app web1",
	} {
		if !slices.Contains(actual, call) {
			t.Errorf("expected %q among the calls, got %q", call, actual)
		}
	}

	for _, call := range actual {
		if strings.Contains(call, "gone-app") {
			t.Errorf("expected nothing for an app that is gone, got %q", call)
		}
	}

	if ui.ErrorWriter.String() != "" {
		t.Errorf("expected no warning, got %q", ui.ErrorWriter.String())
	}
}

// A link that cannot be moved is warned about rather than failing the install,
// which every other datastore service on the host also waits on
func TestMigrateLegacyLinksWarnsOnFailure(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"my-app"}})
	withApp(t, "my-app")
	fakeDokku(t, map[string]string{})

	docker := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(docker, []byte("#!/usr/bin/env bash\necho 'no daemon' >&2\nexit 1\n"), 0755); err != nil {
		t.Fatalf("unable to write the fake docker: %s", err)
	}
	t.Setenv("DOCKER_BIN", docker)
	ui := cli.NewMockUi()

	migrateLegacyLinks(t.Context(), InstallInput{Datastore: datastore, Logger: Ui{Ui: ui}}, "lollipop")

	if !strings.Contains(ui.ErrorWriter.String(), "Unable to move app my-app onto the network of its link to service lollipop") {
		t.Errorf("expected a warning, got %q", ui.ErrorWriter.String())
	}
}
