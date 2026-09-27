package service

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// withUserAuthPlugins points the plugin path at a temporary directory holding a
// user-auth-service trigger for each plugin named, and puts a plugn on PATH that
// runs them the way plugn does. It returns the file each trigger records its
// arguments in.
func withUserAuthPlugins(t *testing.T, triggers map[string]string) string {
	t.Helper()

	root := t.TempDir()
	for plugin, script := range triggers {
		directory := filepath.Join(root, "enabled", plugin)
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatalf("unable to create %s: %s", directory, err)
		}

		if err := os.WriteFile(filepath.Join(directory, UserAuthServiceTrigger), []byte(script), 0755); err != nil {
			t.Fatalf("unable to write the %s trigger: %s", plugin, err)
		}
	}

	bin := t.TempDir()
	plugn := "#!/usr/bin/env bash\nshift\ntrigger=\"$1\"\nshift\nfor script in \"$PLUGIN_PATH\"/enabled/*/\"$trigger\"; do\n  \"$script\" \"$@\" || exit $?\ndone\n"
	if err := os.WriteFile(filepath.Join(bin, "plugn"), []byte(plugn), 0755); err != nil {
		t.Fatalf("unable to write plugn: %s", err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PLUGIN_PATH", root)
	previous := PluginPath
	PluginPath = root
	t.Cleanup(func() {
		PluginPath = previous
	})

	return filepath.Join(root, "arguments")
}

// hideLollipop is a trigger that records what it was handed, says something on
// stderr the way a traced script does, and lets every service through but
// lollipop.
const hideLollipop = `#!/usr/bin/env bash
echo "$@" >"$PLUGIN_PATH/arguments"
echo "SSH_USER=$SSH_USER SSH_NAME=$SSH_NAME" >>"$PLUGIN_PATH/arguments"
echo "+ tracing lollipop" >&2
for service in "${@:4}"; do
  [[ "$service" == lollipop ]] || echo "$service"
done
`

func TestFilterServicesAsksTheUserAuthServiceTrigger(t *testing.T) {
	arguments := withUserAuthPlugins(t, map[string]string{"auth": hideLollipop})
	t.Setenv("SSH_USER", "someone")
	t.Setenv("SSH_NAME", "")
	t.Setenv("NAME", "their-key")

	filtered, err := FilterServices(t.Context(), FilterServicesInput{
		Datastore: Datastores["redis"],
		Services:  []string{"lollipop", "gumdrop", "taffy"},
	})
	if err != nil {
		t.Fatalf("unable to filter: %s", err)
	}

	// what stderr says is not a service, and lollipop was left out on stdout
	expected := []string{"gumdrop", "taffy"}
	if !reflect.DeepEqual(filtered, expected) {
		t.Errorf("expected %v, got %v", expected, filtered)
	}

	recorded, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatalf("expected the trigger to have run: %s", err)
	}

	// the key's name comes from the NAME sshcommand sets, as it did in bash
	expectedArguments := "someone their-key redis lollipop gumdrop taffy\nSSH_USER=someone SSH_NAME=their-key\n"
	if string(recorded) != expectedArguments {
		t.Errorf("expected the trigger to be handed %q, got %q", expectedArguments, recorded)
	}
}

func TestFilterServicesDefaultsTheKeyName(t *testing.T) {
	arguments := withUserAuthPlugins(t, map[string]string{"auth": hideLollipop})
	t.Setenv("SSH_USER", "")
	t.Setenv("USER", "local-user")
	t.Setenv("SSH_NAME", "")
	t.Setenv("NAME", "")

	if _, err := FilterServices(t.Context(), FilterServicesInput{
		Datastore: Datastores["redis"],
		Services:  []string{"gumdrop"},
	}); err != nil {
		t.Fatalf("unable to filter: %s", err)
	}

	recorded, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatalf("expected the trigger to have run: %s", err)
	}

	if !strings.HasPrefix(string(recorded), "local-user default redis gumdrop\n") {
		t.Errorf("expected the user and the default key name, got %q", recorded)
	}
}

// A service the trigger names that was not asked about is not one that exists.
func TestFilterServicesKeepsOnlyTheServicesAskedAbout(t *testing.T) {
	withUserAuthPlugins(t, map[string]string{"auth": "#!/usr/bin/env bash\necho gumdrop\necho invented\n"})

	filtered, err := FilterServices(t.Context(), FilterServicesInput{
		Datastore: Datastores["redis"],
		Services:  []string{"lollipop", "gumdrop"},
	})
	if err != nil {
		t.Fatalf("unable to filter: %s", err)
	}

	if !reflect.DeepEqual(filtered, []string{"gumdrop"}) {
		t.Errorf("expected only gumdrop, got %v", filtered)
	}
}

// With no trigger, or only the one dokku's 20_events ships, there is nobody to
// ask, and every service is seen.
func TestFilterServicesWithoutATrigger(t *testing.T) {
	tests := []struct {
		name     string
		triggers map[string]string
	}{
		{name: "no plugin", triggers: map[string]string{}},
		{name: "only 20_events", triggers: map[string]string{"20_events": "#!/usr/bin/env bash\nexit 1\n"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withUserAuthPlugins(t, test.triggers)

			services := []string{"lollipop", "gumdrop"}
			filtered, err := FilterServices(t.Context(), FilterServicesInput{
				Datastore: Datastores["redis"],
				Services:  services,
			})
			if err != nil {
				t.Fatalf("unable to filter: %s", err)
			}

			if !reflect.DeepEqual(filtered, services) {
				t.Errorf("expected every service, got %v", filtered)
			}
		})
	}
}

// A trigger that fails is not read as hiding everything, nor as hiding nothing.
func TestFilterServicesReportsAFailingTrigger(t *testing.T) {
	withUserAuthPlugins(t, map[string]string{"auth": "#!/usr/bin/env bash\nexit 1\n"})

	if _, err := FilterServices(t.Context(), FilterServicesInput{
		Datastore: Datastores["redis"],
		Services:  []string{"lollipop"},
	}); err == nil || !strings.Contains(err.Error(), UserAuthServiceTrigger) {
		t.Errorf("expected an error naming the trigger, got %v", err)
	}
}
