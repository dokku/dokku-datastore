package internal

import (
	"errors"
	"fmt"
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

// withApps makes several apps exist under one dokku root, where withApp would
// give each a root of its own
func withApps(t *testing.T, appNames ...string) {
	t.Helper()

	dokkuRoot := t.TempDir()
	t.Setenv("DOKKU_ROOT", dokkuRoot)
	for _, appName := range appNames {
		if err := os.MkdirAll(filepath.Join(dokkuRoot, appName), 0755); err != nil {
			t.Fatalf("failed to create the app root: %s", err)
		}
	}
}

// seedLegacyLink gives an app the container link an earlier version linked it
// with, in every phase, where dokku 0.38 and later keeps docker options. It
// returns the file holding the deploy options.
func seedLegacyLink(t *testing.T, datastore *service.Datastore, serviceName string, appName string) string {
	t.Helper()

	directory := filepath.Join(os.Getenv("DOKKU_LIB_ROOT"), "config", "docker-options", appName)
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatalf("failed to create the docker options: %s", err)
	}

	for _, phase := range []string{"build", "deploy", "run"} {
		contents := service.LegacyLinkOption(datastore, serviceName) + "\n--restart=on-failure:10\n"
		if err := os.WriteFile(filepath.Join(directory, "_default_."+phase), []byte(contents), 0644); err != nil {
			t.Fatalf("failed to write the docker options: %s", err)
		}
	}

	return filepath.Join(directory, "_default_.deploy")
}

// scriptedDocker stands in for docker, running the shell given for the first two
// words of each command it is asked, and recording every command. Anything it
// has no answer for succeeds and prints nothing.
func scriptedDocker(t *testing.T, answers map[string]string) string {
	t.Helper()

	root := t.TempDir()
	calls := filepath.Join(root, "calls")
	script := "#!/usr/bin/env bash\necho \"$*\" >>\"" + calls + "\"\ncase \"$1 $2\" in\n"
	for command, answer := range answers {
		script += fmt.Sprintf("  %q) %s ;;\n", command, answer)
	}
	script += "esac\n"

	docker := filepath.Join(root, "docker")
	if err := os.WriteFile(docker, []byte(script), 0755); err != nil {
		t.Fatalf("unable to write the fake docker: %s", err)
	}

	t.Setenv("DOCKER_BIN", docker)
	return calls
}

// exhaustedPools is what docker answers a network create with once its address
// pools are used up
const exhaustedPools = `echo "Error response from daemon: all predefined address pools have been fully subnetted" >&2; exit 1`

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

// Updating the plugin leaves every app on the container link an earlier version
// gave it, touching neither docker nor dokku, and names the apps and the
// command that moves them
func TestWarnLegacyLinks(t *testing.T) {
	tests := []struct {
		name     string
		legacy   []string
		expected []string
	}{
		{
			name:     "no app on a legacy link",
			legacy:   []string{},
			expected: []string{},
		},
		{
			name:   "one app on a legacy link",
			legacy: []string{"my-app"},
			expected: []string{
				"Service lollipop is linked to app my-app over a deprecated docker link, which Docker 30.0 removes",
				"Move it onto a network with: dokku redis:link-migrate lollipop",
			},
		},
		{
			name:   "several apps on legacy links",
			legacy: []string{"my-app", "other-app", "third-app"},
			expected: []string{
				"Service lollipop is linked to apps my-app, other-app and third-app over deprecated docker links, which Docker 30.0 removes",
				"Move them onto networks with: dokku redis:link-migrate lollipop",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			datastore := linkedServices(t, map[string][]string{"lollipop": {"gone-app", "my-app", "net-app", "other-app", "third-app"}})
			withApps(t, "my-app", "net-app", "other-app", "third-app")
			for _, appName := range test.legacy {
				seedLegacyLink(t, datastore, "lollipop", appName)
			}
			dokkuCalls := fakeDokku(t, map[string]string{})
			dockerCalls := scriptedDocker(t, map[string]string{})
			ui := cli.NewMockUi()

			warnLegacyLinks(t.Context(), InstallInput{Datastore: datastore, Logger: Ui{Ui: ui}}, "lollipop")

			for _, expected := range test.expected {
				if !strings.Contains(ui.ErrorWriter.String(), expected) {
					t.Errorf("expected %q among the warnings, got %q", expected, ui.ErrorWriter.String())
				}
			}
			if len(test.expected) == 0 && ui.ErrorWriter.String() != "" {
				t.Errorf("expected no warning, got %q", ui.ErrorWriter.String())
			}
			if strings.Contains(ui.ErrorWriter.String(), "net-app") || strings.Contains(ui.ErrorWriter.String(), "gone-app") {
				t.Errorf("expected only the apps on a legacy link to be named, got %q", ui.ErrorWriter.String())
			}

			if actual := recordedCalls(t, dokkuCalls); len(actual) != 0 {
				t.Errorf("expected dokku not to be asked, got %q", actual)
			}
			if actual := recordedDockerCalls(t, dockerCalls); len(actual) != 0 {
				t.Errorf("expected docker not to be asked, got %q", actual)
			}
		})
	}
}

// Moving a legacy link puts the app's running containers on the network before
// the container link is removed, and changes no config
func TestMigrateLinksMovesALegacyLink(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"my-app"}})
	withApp(t, "my-app")
	deploy := seedLegacyLink(t, datastore, "lollipop", "my-app")
	dokkuCalls := fakeDokku(t, map[string]string{})
	dockerCalls := scriptedDocker(t, map[string]string{
		"container ls":      "printf web1",
		"container inspect": `printf '%s' 'bridge {"bridge":{}}'`,
	})
	ui := cli.NewMockUi()

	if err := MigrateLinks(t.Context(), MigrateLinksInput{
		Datastore:   datastore,
		Logger:      Ui{Ui: ui},
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	actual := recordedDockerCalls(t, dockerCalls)
	for _, expected := range []string{
		strings.Join(service.LinkNetworkCreateArgs(datastore, "lollipop", "my-app"), " "),
		"network connect dokku.redis.lollipop.my-app web1",
	} {
		if !slices.Contains(actual, expected) {
			t.Errorf("expected %q among the calls, got %q", expected, actual)
		}
	}

	if service.HasLegacyLink(datastore, "lollipop", "my-app") {
		t.Error("expected the container link to be removed")
	}
	contents, err := os.ReadFile(deploy)
	if err != nil {
		t.Fatalf("failed to read the docker options: %s", err)
	}
	if strings.TrimSpace(string(contents)) != "--restart=on-failure:10" {
		t.Errorf("expected the other options to be kept, got %q", contents)
	}

	// nothing is set, unset or restarted, and the options are not changed through dokku
	if actual := recordedCalls(t, dokkuCalls); len(actual) != 0 {
		t.Errorf("expected dokku not to be asked, got %q", actual)
	}

	output := ui.OutputWriter.String()
	for _, expected := range []string{
		"Attaching container web1 to network dokku.redis.lollipop.my-app for the link to redis service lollipop",
		"Removing the deprecated docker link from app my-app",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected %q in the output, got %q", expected, output)
		}
	}
}

// An app that cannot be moved keeps its container link and is reported with
// what to do, a network the attempt made is removed again, and the other apps
// are still moved
func TestMigrateLinksKeepsALinkThatCannotBeMoved(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"bad-app", "my-app"}})
	withApps(t, "bad-app", "my-app")
	seedLegacyLink(t, datastore, "lollipop", "bad-app")
	seedLegacyLink(t, datastore, "lollipop", "my-app")
	fakeDokku(t, map[string]string{})
	dockerCalls := scriptedDocker(t, map[string]string{
		// the network is made for both, and is found again when the failed
		// one is undone
		"network ls":        `[[ "$*" == *bad-app* && -f "$(dirname "$0")/made" ]] && printf 0123abcd; true`,
		"network create":    `[[ "$*" == *bad-app* ]] && touch "$(dirname "$0")/made"; true`,
		"network inspect":   "printf '{}'",
		"container ls":      `[[ "$*" == *bad-app* ]] && printf bad1 || printf web1`,
		"container inspect": `printf '%s' 'bridge {"bridge":{}}'`,
		"network connect":   `[[ "$*" == *bad1* ]] && { echo "Error response from daemon: container is restarting" >&2; exit 1; }; true`,
	})
	ui := cli.NewMockUi()

	err := MigrateLinks(t.Context(), MigrateLinksInput{
		Datastore:   datastore,
		Logger:      Ui{Ui: ui},
		ServiceName: "lollipop",
	})
	if err == nil || err.Error() != "Unable to move app bad-app onto a network, it keeps its docker link; rerun the command once the errors above are fixed" {
		t.Fatalf("expected the app that failed to be named, got %v", err)
	}

	if !service.HasLegacyLink(datastore, "lollipop", "bad-app") {
		t.Error("expected the app that could not be moved to keep its container link")
	}
	if service.HasLegacyLink(datastore, "lollipop", "my-app") {
		t.Error("expected the other app to still be moved")
	}

	if actual := recordedDockerCalls(t, dockerCalls); !slices.Contains(actual, "network rm dokku.redis.lollipop.bad-app") {
		t.Errorf("expected the network made for the failed move to be removed, got %q", actual)
	}

	if !strings.Contains(ui.ErrorWriter.String(), "container is restarting") {
		t.Errorf("expected docker's error to be shown, got %q", ui.ErrorWriter.String())
	}
}

// A host out of address pools is told how to fix it right where the move was
// asked for, every app keeps its container link, and no network is left behind
func TestMigrateLinksExplainsExhaustedAddressPools(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"data-api", "hub"}})
	withApps(t, "data-api", "hub")
	seedLegacyLink(t, datastore, "lollipop", "data-api")
	seedLegacyLink(t, datastore, "lollipop", "hub")
	fakeDokku(t, map[string]string{})
	dockerCalls := scriptedDocker(t, map[string]string{
		"network create": exhaustedPools,
		"info --format":  "echo false",
	})
	ui := cli.NewMockUi()

	err := MigrateLinks(t.Context(), MigrateLinksInput{
		Datastore:   datastore,
		Logger:      Ui{Ui: ui},
		ServiceName: "lollipop",
	})
	if err == nil || err.Error() != "Unable to move apps data-api and hub onto networks, they keep their docker links; rerun the command once the errors above are fixed" {
		t.Fatalf("expected both apps to be named, got %v", err)
	}

	warnings := ui.ErrorWriter.String()
	for _, appName := range []string{"data-api", "hub"} {
		if !strings.Contains(warnings, fmt.Sprintf("Unable to create network dokku.redis.lollipop.%s for the link to app %s: docker has no address pool left to create it from", appName, appName)) {
			t.Errorf("expected the address pool error for %s, got %q", appName, warnings)
		}
		if !service.HasLegacyLink(datastore, "lollipop", appName) {
			t.Errorf("expected %s to keep its container link", appName)
		}
	}
	for _, expected := range []string{`"default-address-pools"`, "sudo systemctl restart docker", `Set "live-restore": true`} {
		if strings.Count(warnings, expected) != 2 {
			t.Errorf("expected %q once for each app, got %q", expected, warnings)
		}
	}

	for _, call := range recordedDockerCalls(t, dockerCalls) {
		if strings.HasPrefix(call, "network rm") || strings.HasPrefix(call, "network connect") {
			t.Errorf("expected no network to be left behind or joined, got %q", call)
		}
	}
}

// An app already on its network is put back on it, a network it already had is
// never removed for a failure, and the error does not claim a docker link it no
// longer has
func TestMigrateLinksRepairsAnAppAlreadyMoved(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"my-app"}})
	withApp(t, "my-app")
	fakeDokku(t, map[string]string{})
	dockerCalls := scriptedDocker(t, map[string]string{
		"network ls":        "printf 0123abcd",
		"container ls":      "printf web1",
		"container inspect": `printf '%s' 'bridge {"bridge":{}}'`,
		"network connect":   `[[ "$*" == *web1* ]] && { echo "Error response from daemon: boom" >&2; exit 1; }; true`,
	})
	ui := cli.NewMockUi()

	err := MigrateLinks(t.Context(), MigrateLinksInput{
		AppName:     "my-app",
		Datastore:   datastore,
		Logger:      Ui{Ui: ui},
		ServiceName: "lollipop",
	})
	if err == nil || err.Error() != "Unable to move app my-app onto a network; rerun the command once the errors above are fixed" {
		t.Fatalf("expected a failure that claims no docker link, got %v", err)
	}

	if !strings.Contains(ui.OutputWriter.String(), "App my-app already uses a network link to redis service lollipop") {
		t.Errorf("expected the app to be said to be on its network, got %q", ui.OutputWriter.String())
	}

	for _, call := range recordedDockerCalls(t, dockerCalls) {
		if strings.HasPrefix(call, "network create") || strings.HasPrefix(call, "network rm") {
			t.Errorf("expected the network the app already had to be left alone, got %q", call)
		}
	}
}

// Only an app linked to the service can be named
func TestMigrateLinksRefusesAnAppNotLinked(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"my-app"}})
	dockerCalls := scriptedDocker(t, map[string]string{})

	err := MigrateLinks(t.Context(), MigrateLinksInput{
		AppName:     "other-app",
		Datastore:   datastore,
		Logger:      Ui{Ui: cli.NewMockUi()},
		ServiceName: "lollipop",
	})
	if err == nil || err.Error() != "Not linked to app other-app" {
		t.Fatalf("expected the app to be refused, got %v", err)
	}

	if actual := recordedDockerCalls(t, dockerCalls); len(actual) != 0 {
		t.Errorf("expected docker not to be asked, got %q", actual)
	}
}

// A new link that cannot get a network fails before the app is recorded as
// linked or given any config, so its deploys are not broken by a half link
func TestLinkServiceThatCannotGetANetworkChangesNothing(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {}})
	dokkuCalls := fakeDokku(t, map[string]string{})
	scriptedDocker(t, map[string]string{
		"network create": exhaustedPools,
		"info --format":  "echo true",
	})

	err := LinkService(t.Context(), LinkServiceInput{
		AppName:     "my-app",
		Datastore:   datastore,
		Logger:      Ui{Ui: cli.NewMockUi()},
		NoRestart:   true,
		ServiceName: "lollipop",
	})
	if !errors.Is(err, service.ErrAddressPoolsExhausted) {
		t.Fatalf("expected the address pools to be reported as exhausted, got %v", err)
	}
	if strings.Contains(err.Error(), "live-restore") {
		t.Errorf("expected no live-restore advice when it is already on, got %q", err)
	}

	assertLinkedApps(t, datastore, "lollipop", []string{})

	for _, call := range recordedCalls(t, dokkuCalls) {
		if strings.HasPrefix(call, "config:set") {
			t.Errorf("expected no config to be set, got %q", call)
		}
	}
}

// A container made for an app on a legacy link is left to that link and warned
// about, while its network links are joined, each saying so
func TestConnectContainerToLinksWithALegacyLink(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"cache": {"hub"}, "lollipop": {"hub"}})
	seedLegacyLink(t, datastore, "lollipop", "hub")
	dockerCalls := scriptedDocker(t, map[string]string{
		"network ls":        "printf 0123abcd",
		"container inspect": `printf '%s' 'bridge {"bridge":{}}'`,
	})
	ui := cli.NewMockUi()

	if err := ConnectContainerToLinks(t.Context(), TriggerInput{Datastore: datastore, Logger: Ui{Ui: ui}}, "app", "0123abcd4567890", "hub"); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	for _, call := range recordedDockerCalls(t, dockerCalls) {
		if strings.Contains(call, "lollipop") {
			t.Errorf("expected nothing for the legacy link, got %q", call)
		}
	}

	for _, expected := range []string{
		"App hub reaches redis service lollipop over a deprecated docker link, which Docker 30.0 removes",
		"Move it onto a network with: dokku redis:link-migrate lollipop hub",
	} {
		if !strings.Contains(ui.ErrorWriter.String(), expected) {
			t.Errorf("expected %q among the warnings, got %q", expected, ui.ErrorWriter.String())
		}
	}

	if expected := "Attaching container 0123abcd4567 to network dokku.redis.cache.hub for the link to redis service cache"; !strings.Contains(ui.OutputWriter.String(), expected) {
		t.Errorf("expected %q in the output, got %q", expected, ui.OutputWriter.String())
	}
}

// A network that went missing since the app was linked, and that docker has no
// address pool left for, fails the container with the fix, said as a network
// made again rather than a new one
func TestConnectContainerToLinksCannotRecreateTheNetwork(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"hub"}})
	scriptedDocker(t, map[string]string{
		"network create": exhaustedPools,
		"info --format":  "echo false",
	})

	err := ConnectContainerToLinks(t.Context(), TriggerInput{Datastore: datastore, Logger: Ui{Ui: cli.NewMockUi()}}, "app", "abc123", "hub")
	if !errors.Is(err, service.ErrAddressPoolsExhausted) {
		t.Fatalf("expected the address pools to be reported as exhausted, got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "Unable to recreate network dokku.redis.lollipop.hub for the link to app hub, which was removed since the app was linked") {
		t.Errorf("expected the network to be said to be made again, got %q", err)
	}
}

// The triggers fired before a build, a release and a start make a missing
// network again, so a deploy fails before the build rather than at its first
// container, and leave a legacy link alone
func TestStartLinkedServicesEnsuresTheNetworks(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"cache": {"hub"}, "lollipop": {"hub"}})
	seedLegacyLink(t, datastore, "lollipop", "hub")
	dockerCalls := scriptedDocker(t, map[string]string{
		"container ps":      "printf abc123",
		"container inspect": `printf '%s' 'bridge {"bridge":{}}'`,
		"inspect --format":  "printf running",
	})

	if err := StartLinkedServices(t.Context(), TriggerInput{Datastore: datastore, Logger: Ui{Ui: cli.NewMockUi()}}, "hub"); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	actual := recordedDockerCalls(t, dockerCalls)
	if expected := strings.Join(service.LinkNetworkCreateArgs(datastore, "cache", "hub"), " "); !slices.Contains(actual, expected) {
		t.Errorf("expected %q among the calls, got %q", expected, actual)
	}
	for _, call := range actual {
		if strings.HasPrefix(call, "network") && strings.Contains(call, "lollipop") {
			t.Errorf("expected nothing for the legacy link, got %q", call)
		}
	}
}

// A network that cannot be made again stops the trigger, and so the deploy,
// with the fix
func TestStartLinkedServicesCannotRecreateTheNetwork(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"cache": {"hub"}})
	scriptedDocker(t, map[string]string{
		"container ps":      "printf abc123",
		"container inspect": `printf '%s' 'bridge {"bridge":{}}'`,
		"inspect --format":  "printf running",
		"network create":    exhaustedPools,
		"info --format":     "echo false",
	})

	err := StartLinkedServices(t.Context(), TriggerInput{Datastore: datastore, Logger: Ui{Ui: cli.NewMockUi()}}, "hub")
	if !errors.Is(err, service.ErrAddressPoolsExhausted) {
		t.Fatalf("expected the address pools to be reported as exhausted, got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "Unable to recreate network dokku.redis.cache.hub") {
		t.Errorf("expected the network to be said to be made again, got %q", err)
	}
}

// Ahead of a restore a network that cannot be made is only warned about, as a
// service that cannot be started is
func TestStartAllLinkedServicesWarnsOnANetwork(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"cache": {"hub"}})
	withApp(t, "hub")
	scriptedDocker(t, map[string]string{
		"container ps":      "printf abc123",
		"container inspect": `printf '%s' 'bridge {"bridge":{}}'`,
		"inspect --format":  "printf running",
		"network create":    exhaustedPools,
		"info --format":     "echo true",
	})
	ui := cli.NewMockUi()

	if err := StartAllLinkedServices(t.Context(), TriggerInput{Datastore: datastore, Logger: Ui{Ui: ui}}); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	if !strings.Contains(ui.ErrorWriter.String(), "Unable to recreate network dokku.redis.cache.hub") {
		t.Errorf("expected a warning, got %q", ui.ErrorWriter.String())
	}
}
