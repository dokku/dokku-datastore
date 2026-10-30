package sdk

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
)

// resetNamespace puts the namespace and the datastores back the way the other
// tests expect them once a test that used an entry point is done
func resetNamespace(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		hostenv.SetCommandNamespace("")
		service.Load() //nolint:errcheck
	})
}

// recordCommands swaps the command runner for one that records what it was
// asked to run, failing for any command line a test names
func recordCommands(t *testing.T, failing func(args []string) bool) *[][]string {
	t.Helper()
	resetNamespace(t)

	calls := [][]string{}
	previous := runCommand
	runCommand = func(args []string) int {
		calls = append(calls, append([]string{}, args...))
		if failing != nil && failing(args) {
			return 1
		}

		return 0
	}
	t.Cleanup(func() {
		runCommand = previous
	})

	return &calls
}

// captureStdout returns what a function prints
func captureStdout(t *testing.T, run func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("unable to capture stdout: %s", err)
	}

	previous := os.Stdout
	os.Stdout = writer
	defer func() {
		os.Stdout = previous
	}()

	output := make(chan string)
	go func() {
		var buffer bytes.Buffer
		io.Copy(&buffer, reader) //nolint:errcheck
		output <- buffer.String()
	}()

	run()
	writer.Close() //nolint:errcheck
	return <-output
}

func TestRunSubcommandDispatch(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		args     []string
		expected []string
	}{
		{
			name:     "a built in command keeps its arguments",
			command:  "create",
			args:     []string{"postgres", "lollipop", "--image-version", "17"},
			expected: []string{"create", "postgres", "lollipop", "--image-version", "17"},
		},
		{
			name:     "enter fences off the command run in the container",
			command:  "enter",
			args:     []string{"mysql", "lollipop", "mariabackup", "--backup"},
			expected: []string{"enter", "mysql", "lollipop", "--", "mariabackup", "--backup"},
		},
		{
			name:     "enter with only a service",
			command:  "enter",
			args:     []string{"mysql", "lollipop"},
			expected: []string{"enter", "mysql", "lollipop", "--"},
		},
		{
			name:     "a datastore's own command goes through invoke",
			command:  "certificate",
			args:     []string{"postgres", "lollipop"},
			expected: []string{"invoke", "postgres", "certificate", "lollipop"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := recordCommands(t, nil)

			if actual := RunSubcommand(test.command, test.args); actual != 0 {
				t.Fatalf("expected exit code 0, got %d", actual)
			}

			if len(*calls) != 1 || !slices.Equal((*calls)[0], test.expected) {
				t.Errorf("expected %v to be run, got %v", test.expected, *calls)
			}

			if hostenv.CommandNamespace() != Namespace {
				t.Errorf("expected the %s namespace to be in use, got %q", Namespace, hostenv.CommandNamespace())
			}
		})
	}
}

func TestRunSubcommandRequiresADatastoreTypeForItsOwnCommands(t *testing.T) {
	calls := recordCommands(t, nil)

	if actual := RunSubcommand("certificate", nil); actual != 1 {
		t.Errorf("expected exit code 1, got %d", actual)
	}

	if len(*calls) != 0 {
		t.Errorf("expected nothing to be run, got %v", *calls)
	}
}

func TestRunCommandsDispatch(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected []string
		code     int
	}{
		{name: "the listing dokku asks every plugin for", args: []string{"help"}, expected: []string{"trigger-namespace-help", "help"}},
		{name: "the plugin itself", args: []string{"datastore"}, expected: []string{"trigger-namespace-help", "datastore"}},
		{name: "the plugin help", args: []string{"datastore:help", "redis"}, expected: []string{"trigger-namespace-help", "datastore:help", "redis"}},
		{name: "the plugin default", args: []string{"datastore:default"}, expected: []string{"trigger-namespace-help", "datastore:default"}},
		{name: "a command with no subcommand of its own", args: []string{"datastore:certificate", "postgres", "lollipop"}, expected: []string{"invoke", "postgres", "certificate", "lollipop"}},
		{name: "another plugin's command", args: []string{"postgres:create", "lollipop"}, code: 10},
		{name: "nothing at all", args: []string{}, code: 10},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := recordCommands(t, nil)

			if actual := RunCommands(test.args); actual != test.code {
				t.Fatalf("expected exit code %d, got %d", test.code, actual)
			}

			if test.expected == nil {
				if len(*calls) != 0 {
					t.Errorf("expected nothing to be run, got %v", *calls)
				}
				return
			}

			if len(*calls) != 1 || !slices.Equal((*calls)[0], test.expected) {
				t.Errorf("expected %v to be run, got %v", test.expected, *calls)
			}
		})
	}
}

// dokku builds its listing in a pipeline under set -e, so the listing must not
// fail even when the help it asked for does
func TestRunCommandsNeverFailsTheListing(t *testing.T) {
	recordCommands(t, func([]string) bool { return true })

	if actual := RunCommands([]string{"help"}); actual != 0 {
		t.Errorf("expected exit code 0, got %d", actual)
	}
}

func TestRunCommandsPrintsTheVersion(t *testing.T) {
	recordCommands(t, nil)

	previous := version
	version = "v1.2.3"
	t.Cleanup(func() {
		version = previous
	})

	output := captureStdout(t, func() {
		if actual := RunCommands([]string{"datastore:version"}); actual != 0 {
			t.Errorf("expected exit code 0, got %d", actual)
		}
	})

	if strings.TrimSpace(output) != "v1.2.3" {
		t.Errorf("expected the version, got %q", output)
	}
}

// fakePlugin enables a plugin directory, with a config file naming the
// datastore it serves when one is given
func fakePlugin(t *testing.T, enabled string, name string, commandPrefix string) {
	t.Helper()

	dir := filepath.Join(enabled, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("unable to create %s: %s", dir, err)
	}

	if commandPrefix == "" {
		return
	}

	config := "#!/usr/bin/env bash\nexport PLUGIN_COMMAND_PREFIX=\"" + commandPrefix + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(config), 0644); err != nil {
		t.Fatalf("unable to write the config: %s", err)
	}
}

func TestRunTriggerSkipsDatastoresAnotherPluginServes(t *testing.T) {
	enabled := t.TempDir()
	t.Setenv("PLUGIN_ENABLED_PATH", enabled)

	// one plugin named after its datastore, one installed under another name,
	// and one that serves no datastore at all
	fakePlugin(t, enabled, "redis", "redis")
	fakePlugin(t, enabled, "my-mysql", "mysql")
	fakePlugin(t, enabled, "apps", "")

	calls := recordCommands(t, nil)
	if actual := RunTrigger("pre-delete", []string{"my-app"}); actual != 0 {
		t.Fatalf("expected exit code 0, got %d", actual)
	}

	ran := map[string][]string{}
	for _, call := range *calls {
		ran[call[1]] = call
	}

	for _, owned := range []string{"redis", "mysql"} {
		if _, ok := ran[owned]; ok {
			t.Errorf("expected %s to be left to its own plugin", owned)
		}
	}

	for _, datastoreType := range service.Types() {
		if datastoreType == "redis" || datastoreType == "mysql" {
			continue
		}

		expected := []string{"trigger-pre-delete", datastoreType, "my-app"}
		if !slices.Equal(ran[datastoreType], expected) {
			t.Errorf("expected %v to be run, got %v", expected, ran[datastoreType])
		}
	}
}

func TestRunTriggerDispatch(t *testing.T) {
	t.Setenv("PLUGIN_ENABLED_PATH", t.TempDir())

	tests := []struct {
		name     string
		trigger  string
		args     []string
		expected []string
	}{
		{name: "a built in trigger", trigger: "post-container-create", args: []string{"app", "my-app"}, expected: []string{"trigger-post-container-create", "redis", "app", "my-app"}},
		{name: "install", trigger: "install", expected: []string{"trigger-install", "redis"}},
		{name: "update installs again", trigger: "update", expected: []string{"trigger-install", "redis"}},
		{name: "a trigger a definition declares", trigger: "post-extract", args: []string{"my-app"}, expected: []string{"trigger", "post-extract", "redis", "my-app"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := recordCommands(t, nil)
			if actual := RunTrigger(test.trigger, test.args); actual != 0 {
				t.Fatalf("expected exit code 0, got %d", actual)
			}

			found := false
			for _, call := range *calls {
				if slices.Equal(call, test.expected) {
					found = true
				}
			}

			if !found {
				t.Errorf("expected %v among %v", test.expected, *calls)
			}

			if len(*calls) != len(service.Types()) {
				t.Errorf("expected one run per datastore, got %d for %d datastores", len(*calls), len(service.Types()))
			}
		})
	}
}

// dokku stops at the first plugin whose trigger fails, so the datastores after
// a failing one are not run either
func TestRunTriggerStopsAtTheFirstFailure(t *testing.T) {
	t.Setenv("PLUGIN_ENABLED_PATH", t.TempDir())

	types := service.Types()
	failing := types[1]
	calls := recordCommands(t, func(args []string) bool { return args[1] == failing })

	if actual := RunTrigger("pre-start", []string{"my-app"}); actual != 1 {
		t.Fatalf("expected exit code 1, got %d", actual)
	}

	if len(*calls) != 2 {
		t.Errorf("expected the run to stop at %s, got %v", failing, *calls)
	}
}

func TestSubcommandNames(t *testing.T) {
	names := SubcommandNames()

	for _, expected := range []string{"create", "destroy", "enter", "link", "upgrade"} {
		if !slices.Contains(names, expected) {
			t.Errorf("expected %s among %v", expected, names)
		}
	}

	for _, unexpected := range []string{"invoke", "trigger-help", "trigger-namespace-help", "version", "readme", "generate"} {
		if slices.Contains(names, unexpected) {
			t.Errorf("expected %s not to be a subcommand", unexpected)
		}
	}

	if !slices.IsSorted(names) {
		t.Errorf("expected the names sorted, got %v", names)
	}
}

func TestTriggerNames(t *testing.T) {
	names := TriggerNames()

	expected := append([]string{"install", "update"}, internal.BuiltinTriggers...)
	for _, datastore := range service.Datastores {
		expected = append(expected, datastore.TriggerNames()...)
	}

	for _, name := range expected {
		if !slices.Contains(names, name) {
			t.Errorf("expected %s among %v", name, names)
		}
	}

	if !slices.IsSorted(names) || len(slices.Compact(slices.Clone(names))) != len(names) {
		t.Errorf("expected the names sorted and unique, got %v", names)
	}
}

func TestVersionFallsBackToTheBuild(t *testing.T) {
	previous := version
	version = ""
	t.Cleanup(func() {
		version = previous
	})

	// a test binary is this module, which go records as built from source
	if actual := Version(); actual == "" {
		t.Error("expected a version from the build")
	}
}

// runNamespaced runs one of the binary's commands the way the plugin serving
// every datastore does, and returns what it printed
func runNamespaced(t *testing.T, args ...string) (string, int) {
	t.Helper()
	resetNamespace(t)
	t.Setenv("DOKKU_NO_COLOR", "1")

	if _, ok := useNamespace(); !ok {
		t.Fatal("expected the datastores to load")
	}

	code := 0
	output := captureStdout(t, func() {
		code = Run(RunInput{Args: args})
	})

	return output, code
}

func TestNamespaceHelp(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected []string
		code     int
	}{
		{
			name: "the listing dokku asks every plugin for",
			args: []string{"trigger-namespace-help", "help"},
			expected: []string{
				"    datastore, Manage datastore services",
			},
		},
		{
			name: "the plugin as a whole",
			args: []string{"trigger-namespace-help", "datastore:help"},
			expected: []string{
				"usage: dokku datastore[:COMMAND] <datastore-type>",
				"postgres, ",
				"redis",
				"get help with dokku datastore:help <datastore-type> [SUBCOMMAND]",
				"datastore:create <datastore-type> <service>",
			},
		},
		{
			name: "one datastore",
			args: []string{"trigger-namespace-help", "datastore:help", "redis"},
			expected: []string{
				"usage: dokku datastore[:COMMAND] redis",
				"$ dokku datastore:list redis",
				"get help with dokku datastore:help redis SUBCOMMAND",
				"datastore:create redis <service>",
			},
		},
		{
			name: "one of a datastore's commands",
			args: []string{"trigger-namespace-help", "datastore:help", "postgres", "create"},
			expected: []string{
				"usage: dokku datastore:create postgres <service>",
				"dokku datastore:create postgres lollipop",
			},
		},
		{
			name: "one of a datastore's own commands",
			args: []string{"trigger-namespace-help", "datastore:help", "postgres", "certificate"},
			expected: []string{
				"usage: dokku datastore:certificate postgres <service>",
				"dokku datastore:certificate postgres lollipop",
			},
		},
		{
			name: "a datastore that does not exist",
			args: []string{"trigger-namespace-help", "datastore:help", "nonsense"},
			code: 1,
		},
		{
			name: "another plugin's command",
			args: []string{"trigger-namespace-help", "postgres:help"},
			code: 10,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, code := runNamespaced(t, test.args...)
			if code != test.code {
				t.Fatalf("expected exit code %d, got %d:\n%s", test.code, code, output)
			}

			for _, expected := range test.expected {
				if !strings.Contains(output, expected) {
					t.Errorf("expected the help to contain %q, got:\n%s", expected, output)
				}
			}

			// no help under the namespace may point at a per-datastore command
			for _, datastoreType := range service.Types() {
				if strings.Contains(output, "dokku "+datastoreType+":") {
					t.Errorf("expected no per-datastore command in the help, found dokku %s: in:\n%s", datastoreType, output)
				}
			}
		})
	}
}

// Every command's help renders under the namespace for every datastore that
// implements it, and points only at commands that exist there
func TestNamespaceCommandHelpNamesNamespacedCommands(t *testing.T) {
	for _, datastoreType := range service.Types() {
		t.Run(datastoreType, func(t *testing.T) {
			output, code := runNamespaced(t, "trigger-namespace-help", "datastore:help", datastoreType)
			if code != 0 {
				t.Fatalf("expected exit code 0, got %d:\n%s", code, output)
			}

			if strings.Contains(output, "dokku "+datastoreType+":") {
				t.Errorf("expected no per-datastore command in the help:\n%s", output)
			}
		})
	}
}

// The trigger-namespace-help command only answers for the namespaced plugin
func TestNamespaceHelpRefusesWithoutANamespace(t *testing.T) {
	resetNamespace(t)
	if actual := Run(RunInput{Args: []string{"trigger-namespace-help", "datastore:help"}}); actual != 1 {
		t.Errorf("expected exit code 1, got %d", actual)
	}
}

// Every command's help, for every datastore, points only at commands that
// exist under the namespace, so a template that spells a command out by hand
// rather than with .Command is caught
func TestEveryNamespacedCommandHelpNamesNamespacedCommands(t *testing.T) {
	resetNamespace(t)
	t.Setenv("DOKKU_NO_COLOR", "1")
	if _, ok := useNamespace(); !ok {
		t.Fatal("expected the datastores to load")
	}

	builtins := registeredPluginCommands(t)
	for _, datastoreType := range service.Types() {
		datastore := service.Datastores[datastoreType]
		data := internal.NewDocumentationData(internal.DocumentationDataInput{Datastore: datastore})

		for _, c := range append(slices.Clone(builtins), internal.CustomCommands(datastore)...) {
			help, err := internal.PluginCommandHelp(c, data)
			if err != nil {
				t.Errorf("%s %s: unable to render the help: %s", datastoreType, c.Name(), err)
				continue
			}

			if strings.Contains(help, "dokku "+datastoreType+":") || strings.Contains(help, "`"+datastoreType+":") {
				t.Errorf("%s %s: expected no per-datastore command in the help:\n%s", datastoreType, c.Name(), help)
			}
		}
	}
}
