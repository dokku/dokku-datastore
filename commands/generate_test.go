package commands

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/registry"
	"github.com/dokku/dokku-datastore/internal/service"
)

// generated lays a datastore's definitions into a checkout and returns it.
func generated(t *testing.T, datastoreType string) string {
	t.Helper()

	pluginDir := t.TempDir()
	command := &GenerateCommand{pluginDir: pluginDir}

	if _, err := command.writeDefinitions(service.Datastores[datastoreType]); err != nil {
		t.Fatalf("unable to generate %s: %s", datastoreType, err)
	}

	return pluginDir
}

// The check that matters: what generate writes is what the loader reads back. A
// tree the loader refuses would be a plugin whose every command fails, and the
// two halves are written in different packages against the same layout, so
// nothing but this keeps them in step.
func TestGeneratedDefinitionsLoadBack(t *testing.T) {
	pluginDir := generated(t, "postgres")

	loaded, err := registry.Load(registry.LoadInput{PluginDir: pluginDir})
	if err != nil {
		t.Fatalf("the generated tree does not load: %s", err)
	}

	if names := loaded.NamesFor("postgres"); len(names) != 2 || names[0] != "postgres-17" || names[1] != "postgres-18" {
		t.Fatalf("expected both majors to come back, got %v", names)
	}

	for _, name := range loaded.NamesFor("postgres") {
		found, _ := loaded.Definition(name)
		if found.Dokku.Plugin != "postgres" {
			t.Errorf("%s came back under plugin %q", name, found.Dokku.Plugin)
		}
	}
}

// A datastore split by major version has every one of its definitions written.
// Shipping only the newest would leave a service pinned to an older one with no
// definition at all, since a plugin's definitions replace the embedded set.
func TestGenerateWritesEveryVariant(t *testing.T) {
	pluginDir := generated(t, "postgres")

	entries, err := os.ReadDir(filepath.Join(pluginDir, "datastore"))
	if err != nil {
		t.Fatalf("unable to read the generated tree: %s", err)
	}

	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	if len(names) != 2 || names[0] != "postgres-17" || names[1] != "postgres-18" {
		t.Errorf("expected postgres-17 and postgres-18, got %v", names)
	}
}

// The compose file is copied rather than re-serialised: a definition may use
// compose keys this binary does not model, and its comments are where it explains
// itself. Round tripping through the parser would quietly drop both.
func TestGenerateCopiesTheComposeFileVerbatim(t *testing.T) {
	pluginDir := generated(t, "postgres")

	written, err := os.ReadFile(filepath.Join(pluginDir, "datastore", "postgres-17", "docker-compose.yml"))
	if err != nil {
		t.Fatalf("unable to read the generated compose file: %s", err)
	}

	source := service.Datastores["postgres"].ForImageVersion("17.8").Definition
	if string(written) != string(source.Compose) {
		t.Error("expected the compose file to be copied byte for byte")
	}

	if !strings.Contains(string(written), "# where postgres kept its data before eighteen") {
		t.Error("expected the definition's comments to survive")
	}
}

// Running generate twice writes the same tree. A plugin runs it on every change
// and commits the result, so a second run that differed would be permanent churn
// in the diff.
func TestGenerateIsIdempotent(t *testing.T) {
	pluginDir := t.TempDir()
	command := &GenerateCommand{pluginDir: pluginDir}
	redis := service.Datastores["redis"]

	first, err := command.writeDefinitions(redis)
	if err != nil {
		t.Fatalf("unable to generate: %s", err)
	}

	before := treeOf(t, pluginDir)

	second, err := command.writeDefinitions(redis)
	if err != nil {
		t.Fatalf("unable to regenerate: %s", err)
	}

	if strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Errorf("expected the same files to be written, got %v then %v", first, second)
	}

	if after := treeOf(t, pluginDir); before != after {
		t.Errorf("expected the tree to be unchanged:\n%s\nbecame\n%s", before, after)
	}
}

// A payload script arrives able to run. An embedded file has no mode of its own
// to carry, so a rootfs file below a bin directory is written executable, which
// is the rule the mount uses too.
func TestGenerateWritesPayloadScriptsExecutable(t *testing.T) {
	pluginDir := generated(t, "redis")

	path := filepath.Join(pluginDir, "datastore", "redis", "rootfs", "usr", "local", "bin", "dokku-redis-export")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("unable to stat the generated script: %s", err)
	}

	if info.Mode().Perm() != 0755 {
		t.Errorf("expected 0755, got %o", info.Mode().Perm())
	}
}

// Every definition the binary carries can be shipped by its plugin, solr and
// graphite included. A host mode command runs as the dokku user, who already owns
// every script in the plugin tree, and a privileged script is installed by an
// install trigger that root already runs from that same tree: neither is a
// privilege a plugin did not already have.
func TestGeneratedDefinitionsLoadBackForEveryDatastore(t *testing.T) {
	for _, datastoreType := range []string{"solr", "graphite", "redis", "postgres", "elasticsearch"} {
		t.Run(datastoreType, func(t *testing.T) {
			pluginDir := generated(t, datastoreType)

			loaded, err := registry.Load(registry.LoadInput{PluginDir: pluginDir})
			if err != nil {
				t.Fatalf("the generated tree does not load: %s", err)
			}

			if len(loaded.NamesFor(datastoreType)) == 0 {
				t.Errorf("expected %s to come back from its own checkout", datastoreType)
			}
		})
	}
}

// Solr's post-extract trigger runs on the host, which is the whole reason solr
// could not ship its own definition before.
func TestGeneratedSolrKeepsItsHostTrigger(t *testing.T) {
	pluginDir := generated(t, "solr")

	loaded, err := registry.Load(registry.LoadInput{PluginDir: pluginDir})
	if err != nil {
		t.Fatalf("the generated tree does not load: %s", err)
	}

	found, err := loaded.For("solr", "")
	if err != nil {
		t.Fatalf("unable to resolve solr: %s", err)
	}

	trigger, ok := found.TriggerFor("post-extract")
	if !ok {
		t.Fatal("expected the post-extract trigger to survive")
	}

	if trigger.Mode != definition.ModeHost {
		t.Errorf("expected host mode, got %q", trigger.Mode)
	}
}

// Graphite installs a privileged script, which is the other reason.
func TestGeneratedGraphiteKeepsItsPrivilegedScript(t *testing.T) {
	pluginDir := generated(t, "graphite")

	loaded, err := registry.Load(registry.LoadInput{PluginDir: pluginDir})
	if err != nil {
		t.Fatalf("the generated tree does not load: %s", err)
	}

	found, err := loaded.For("graphite", "")
	if err != nil {
		t.Fatalf("unable to resolve graphite: %s", err)
	}

	if len(found.Privileged) == 0 {
		t.Error("expected the privileged scripts to survive")
	}
}

// treeOf renders a directory as sorted "mode path\ncontents" records, so two
// generations can be compared as one string.
func treeOf(t *testing.T, root string) string {
	t.Helper()

	records := []string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		records = append(records, relative+" "+info.Mode().Perm().String()+"\n"+string(contents))
		return nil
	})
	if err != nil {
		t.Fatalf("unable to walk %s: %s", root, err)
	}

	sort.Strings(records)
	return strings.Join(records, "\n")
}

// Every plugin gets a file for each trigger the binary implements, so a new one
// reaches the plugins through their next generate rather than by hand, and a
// definition's own triggers are written beside them.
func TestGenerateWritesEveryTrigger(t *testing.T) {
	pluginDir := t.TempDir()
	command := &GenerateCommand{pluginDir: pluginDir}

	written, err := command.writeTriggers(service.Datastores["solr"])
	if err != nil {
		t.Fatalf("unable to write the triggers: %s", err)
	}

	expected := append(slices.Clone(internal.BuiltinTriggers), "post-extract")
	if len(written) != len(expected) {
		t.Errorf("expected %d triggers, got %v", len(expected), written)
	}

	for _, name := range expected {
		path := filepath.Join(pluginDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s to be written: %s", name, err)
			continue
		}

		if info.Mode().Perm() != 0755 {
			t.Errorf("expected %s to be executable, got %v", name, info.Mode().Perm())
		}

		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("unable to read %s: %s", path, err)
		}

		dispatch := "trigger-" + name
		if name == "post-extract" {
			dispatch = "trigger post-extract"
		}

		if !strings.Contains(string(contents), `dokku-datastore" `+dispatch+` "$PLUGIN_COMMAND_PREFIX" "$@"`) {
			t.Errorf("expected %s to dispatch to %q, got:\n%s", name, dispatch, contents)
		}
	}
}
