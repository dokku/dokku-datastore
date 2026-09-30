package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal"
	"maps"
	"slices"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	flag "github.com/spf13/pflag"
)

// knownGroups are the readme usage sections a command may be documented under.
// definition.Groups is the set a definition may name; a built-in may also be in
// GroupNone, which is documented nowhere.
var knownGroups = func() map[string]bool {
	groups := map[string]bool{definition.GroupNone: true}
	for group := range definition.Groups {
		groups[group] = true
	}

	return groups
}()

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
		Datastore: service.Datastores["redis"],
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

// The link help has to say which variable --alias sets, since the flag alone
// did not make that clear.
func TestLinkHelpShowsTheAliasInUse(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")

	var link internal.PluginCommand
	for _, c := range registeredPluginCommands(t) {
		if c.Name() == "link" {
			link = c
		}
	}
	if link == nil {
		t.Fatal("expected the registry to hold the link command")
	}

	for _, name := range []string{"mysql", "redis"} {
		t.Run(name, func(t *testing.T) {
			data := internal.NewDocumentationData(internal.DocumentationDataInput{
				Datastore: service.Datastores[name],
			})

			help, err := internal.PluginCommandHelp(link, data)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			for _, expected := range []string{
				"dokku " + name + ":link lollipop playground --alias BLUE_" + data.DefaultAlias,
				"BLUE_" + data.DefaultAlias + "_URL=",
				"dokku " + name + ":link lollipop playground --querystring",
				"which is suffixed with _URL",
			} {
				if !strings.Contains(help, expected) {
					t.Errorf("expected the help to contain %q, got:\n%s", expected, help)
				}
			}
		})
	}
}

// The promote help walks through linking a second service and promoting it. The
// variable names it shows are worked out here the way link and promote work
// them out, so the example cannot name variables the code never sets.
func TestPromoteHelpNamesTheGeneratedAliases(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")

	var promote internal.PluginCommand
	for _, c := range registeredPluginCommands(t) {
		if c.Name() == "promote" {
			promote = c
		}
	}
	if promote == nil {
		t.Fatal("expected the registry to hold the promote command")
	}

	for _, name := range []string{"mysql", "redis"} {
		t.Run(name, func(t *testing.T) {
			datastore := service.Datastores[name]
			data := internal.NewDocumentationData(internal.DocumentationDataInput{Datastore: datastore})
			defaultKey := data.DefaultAlias + "_URL"

			// lollipop is linked first and takes the default alias, so linking
			// other_service falls back to a generated one
			lollipopURL := "lollipop-url"
			otherURL := "other-service-url"
			environment := map[string]string{defaultKey: lollipopURL}
			linkedKey := internal.AlternateAlias(datastore, environment) + "_URL"
			environment[linkedKey] = otherURL

			_, displacedKey, err := internal.PromotionEntries(internal.PromoteServiceInput{
				AppName:     "playground",
				Datastore:   datastore,
				ServiceName: "other_service",
			}, environment, internal.ConfigKeysForURL(environment, otherURL))
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if displacedKey == "" {
				t.Fatal("expected promote to keep the displaced url")
			}

			help, err := internal.PluginCommandHelp(promote, data)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			for _, expected := range []string{linkedKey + "=", displacedKey + "="} {
				if !strings.Contains(help, expected) {
					t.Errorf("expected the help to contain %q, got:\n%s", expected, help)
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
		"app-links", "backup", "backup-auth", "backup-deauth", "backup-logs", "backup-schedule",
		"backup-schedule-cat", "backup-set-encryption", "backup-set-public-key-encryption",
		"backup-unschedule", "backup-unset-encryption", "backup-unset-public-key-encryption",
		"clone", "connect", "create", "destroy", "enter", "exists", "export", "expose",
		"import", "info", "link", "linked", "links", "list", "logs", "mount", "pause", "promote",
		"restart", "set", "start", "stop", "unexpose", "unlink", "unmount", "upgrade",
	} {
		if !names[name] {
			t.Errorf("the %s command is not documented", name)
		}
	}

	for _, name := range []string{"readme", "version", "trigger-cron-entries", "trigger-help", "trigger-install", "trigger-service-list"} {
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

// Every command a readme section documents has to be named in that section's
// order, or it renders at the end of the section and nothing says so.
//
// GroupNone is exempt because it is not a section, and the custom command
// section because nothing built in belongs to it.
func TestEveryCommandIsPlacedInItsSection(t *testing.T) {
	for _, c := range registeredPluginCommands(t) {
		group := c.Group()
		if group == definition.GroupNone || group == definition.GroupCustomCommands {
			continue
		}

		t.Run(c.Name(), func(t *testing.T) {
			order, ok := internal.SectionCommands(group)
			if !ok {
				t.Fatalf("is in %q, which the readme has no section for", group)
			}

			if !slices.Contains(order, c.Name()) {
				t.Errorf("is in %q but not in its order, so it documents itself at the end: add it to readmeSections", group)
			}
		})
	}
}

// And the other way: an order that names a command nothing implements is a
// section carrying a name that will never render.
func TestEverySectionOrderNamesARealCommand(t *testing.T) {
	implemented := map[string]bool{}
	for _, c := range registeredPluginCommands(t) {
		implemented[c.Name()] = true
	}

	for _, group := range append(slices.Collect(maps.Keys(definition.Groups)), definition.GroupCustomCommands) {
		order, ok := internal.SectionCommands(group)
		if !ok {
			continue
		}

		for _, name := range order {
			if !implemented[name] {
				t.Errorf("%s names %q, which no command implements", group, name)
			}
		}
	}
}

// generate writes a script for every command a plugin exposes, so a command
// added to the binary reaches a plugin the next time it is regenerated rather
// than when someone remembers to copy a script into it.
func TestGenerateWritesAScriptForEveryPluginCommand(t *testing.T) {
	pluginDir := t.TempDir()
	t.Setenv("DOKKU_NO_COLOR", "1")
	if actual := Run([]string{"generate", "--plugin-dir", pluginDir, "mongo"}); actual != 0 {
		t.Fatalf("expected generate to succeed, got exit code %d", actual)
	}

	expected := []string{}
	for _, c := range registeredPluginCommands(t) {
		expected = append(expected, c.Name())
	}
	for _, c := range internal.CustomCommands(service.Datastores["mongo"]) {
		expected = append(expected, c.Name())
	}
	slices.Sort(expected)

	entries, err := os.ReadDir(filepath.Join(pluginDir, "subcommands"))
	if err != nil {
		t.Fatalf("unable to read the generated subcommands: %s", err)
	}

	written := []string{}
	for _, entry := range entries {
		written = append(written, entry.Name())
	}

	if !slices.Equal(written, expected) {
		t.Errorf("expected a script for each of %v, got %v", expected, written)
	}
}
