package main

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/datastores"

	"github.com/josegonzalez/cli-skeleton/command"
	flag "github.com/spf13/pflag"
)

// knownGroups are the readme usage sections a command may be documented under
var knownGroups = map[string]bool{
	internal.GroupNone:              true,
	internal.GroupBasicUsage:        true,
	internal.GroupServiceLifecycle:  true,
	internal.GroupServiceAutomation: true,
	internal.GroupDataManagement:    true,
	internal.GroupBackups:           true,
}

// flagPattern matches a long flag named in an argument sketch
var flagPattern = regexp.MustCompile(`--[a-z][a-z-]*`)

// registeredPluginCommands are the commands a dokku datastore plugin exposes as
// subcommands, which are the ones the plugin help and readme document
func registeredPluginCommands(t *testing.T) []internal.PluginCommand {
	t.Helper()

	commands := []internal.PluginCommand{}
	for name, factory := range Commands(context.Background(), command.Meta{}) {
		c, err := factory()
		if err != nil {
			t.Fatalf("unable to build the %s command: %s", name, err)
		}

		if pluginCommand, ok := c.(internal.PluginCommand); ok {
			commands = append(commands, pluginCommand)
		}
	}

	if len(commands) == 0 {
		t.Fatal("expected the registry to hold commands the plugin documents")
	}

	return commands
}

func TestPluginCommandDocumentation(t *testing.T) {
	data := internal.NewDocumentationData(internal.DocumentationDataInput{
		Datastore: datastores.Datastores["redis"],
	})

	for _, c := range registeredPluginCommands(t) {
		t.Run(c.Name(), func(t *testing.T) {
			for name, body := range map[string]string{
				"description":   c.Description(),
				"usage":         c.Usage(),
				"documentation": c.Documentation(),
			} {
				if _, err := internal.RenderDocumentation(body, data); err != nil {
					t.Errorf("the %s template does not render: %s", name, err)
				}
			}

			if strings.TrimSpace(c.Description()) == "" {
				t.Error("expected a description")
			}

			if strings.TrimSpace(c.Documentation()) == "" {
				t.Error("expected documentation")
			}

			if !knownGroups[c.Group()] {
				t.Errorf("unknown readme usage section %q", c.Group())
			}
		})
	}
}

func TestPluginCommandUsageNamesRealFlags(t *testing.T) {
	for _, c := range registeredPluginCommands(t) {
		t.Run(c.Name(), func(t *testing.T) {
			flags := c.FlagSet()
			for _, named := range flagPattern.FindAllString(c.Usage(), -1) {
				name := strings.TrimPrefix(named, "--")
				if strings.HasSuffix(name, "-flags") {
					// the catch all for a command with too many flags to list
					continue
				}

				if flags.Lookup(name) == nil {
					t.Errorf("the usage names --%s, which the command does not accept", name)
				}
			}
		})
	}
}

func TestPluginCommandArgumentsAreDocumented(t *testing.T) {
	for _, c := range registeredPluginCommands(t) {
		t.Run(c.Name(), func(t *testing.T) {
			for _, argument := range c.Arguments() {
				if strings.TrimSpace(argument.Description) == "" {
					t.Errorf("the %s argument has no description", argument.Name)
				}
			}

			c.FlagSet().VisitAll(func(f *flag.Flag) {
				if strings.TrimSpace(f.Usage) == "" {
					t.Errorf("the --%s flag has no description", f.Name)
				}

				if strings.Contains(f.Usage, "$PLUGIN") {
					t.Errorf("the --%s flag description leaks a plugin variable: %s", f.Name, f.Usage)
				}
			})
		})
	}
}

func TestPluginCommandsCoverEveryDocumentedSubcommand(t *testing.T) {
	names := map[string]bool{}
	for _, c := range registeredPluginCommands(t) {
		names[c.Name()] = true
	}

	for _, name := range []string{
		"app-links", "backup", "backup-auth", "backup-deauth", "backup-schedule",
		"backup-schedule-cat", "backup-set-encryption", "backup-set-public-key-encryption",
		"backup-unschedule", "backup-unset-encryption", "backup-unset-public-key-encryption",
		"clone", "connect", "create", "destroy", "enter", "exists", "export", "expose",
		"import", "info", "link", "linked", "links", "list", "logs", "pause", "promote",
		"restart", "set", "start", "stop", "unexpose", "unlink", "upgrade",
	} {
		if !names[name] {
			t.Errorf("the %s command is not documented", name)
		}
	}

	for _, name := range []string{"readme", "version", "trigger-help", "trigger-install", "trigger-service-list"} {
		if names[name] {
			t.Errorf("the %s command is not a plugin subcommand and should not be documented", name)
		}
	}
}

func TestTriggerHelpExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected int
	}{
		{name: "the plugin itself", args: []string{"redis", "redis"}, expected: 0},
		{name: "the plugin help", args: []string{"redis", "redis:help"}, expected: 0},
		{name: "the plugin default", args: []string{"redis", "redis:default"}, expected: 0},
		{name: "a single command", args: []string{"redis", "redis:help", "create"}, expected: 0},
		{name: "dokku asking for the command list", args: []string{"redis", "help"}, expected: 0},
		{name: "a command another plugin owns", args: []string{"redis", "postgres:create", "lollipop"}, expected: 10},
		{name: "a command no plugin owns", args: []string{"redis", "nonsense"}, expected: 10},
		{name: "nothing at all", args: []string{"redis"}, expected: 10},
		{name: "a command that does not exist on this plugin", args: []string{"redis", "redis:help", "nonsense"}, expected: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DOKKU_NO_COLOR", "1")
			if actual := Run(append([]string{"trigger-help"}, test.args...)); actual != test.expected {
				t.Errorf("expected exit code %d, got %d", test.expected, actual)
			}
		})
	}
}

func TestTriggerHelpHonoursTheDokkuExitCode(t *testing.T) {
	t.Setenv("DOKKU_NOT_IMPLEMENTED_EXIT", "42")

	if actual := Run([]string{"trigger-help", "redis", "nonsense"}); actual != 42 {
		t.Errorf("expected exit code 42, got %d", actual)
	}
}
