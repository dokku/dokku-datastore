package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
)

// TestEveryEmbeddedDefinitionLoads is the check that stops a broken definition
// reaching a release. It runs on every pull request, needs no docker, and is the
// reason a definition can be added with confidence.
func TestEveryEmbeddedDefinitionLoads(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("an embedded definition does not load: %s", err)
	}

	if len(loaded.Names()) == 0 {
		t.Fatal("expected at least one embedded definition")
	}

	for _, name := range loaded.Names() {
		t.Run(name, func(t *testing.T) {
			parsed, ok := loaded.Definition(name)
			if !ok {
				t.Fatalf("%s is listed but does not resolve", name)
			}

			if _, _, _, err := definition.ImageFromDockerfile(parsed.Dockerfile); err != nil {
				t.Errorf("the dockerfile does not pin an image: %s", err)
			}

			if _, ok := parsed.PrimaryPort(); !ok && len(parsed.Service.Ports) > 0 {
				t.Error("expected a primary port")
			}
		})
	}
}

func TestRedisDefinition(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load: %s", err)
	}

	redis, ok := loaded.Definition("redis")
	if !ok {
		t.Fatal("expected an embedded redis definition")
	}

	tests := []struct {
		name     string
		actual   string
		expected string
	}{
		{name: "plugin", actual: redis.Dokku.Plugin, expected: "redis"},
		{name: "title", actual: redis.Dokku.Title, expected: "Redis"},
		{name: "scheme", actual: redis.Dokku.Scheme, expected: "redis"},
		{name: "alias", actual: redis.Dokku.Alias, expected: "REDIS"},
		{name: "alt alias", actual: redis.Dokku.AltAlias, expected: "DOKKU_REDIS"},
		{name: "variable", actual: redis.Dokku.Variable, expected: "REDIS"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, test.actual)
			}
		})
	}

	port, ok := redis.PortFor("native")
	if !ok || port.Target != 6379 {
		t.Errorf("expected a native port on 6379, got %+v", port)
	}
}

func TestLookupAndPlugins(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load: %s", err)
	}

	if _, err := loaded.For("redis", "8.8.0"); err != nil {
		t.Errorf("unexpected error: %s", err)
	}

	if _, err := loaded.For("nonsense", ""); err == nil {
		t.Error("expected an unknown datastore type to be an error")
	}

	found := false
	for _, plugin := range loaded.Plugins() {
		if plugin == "redis" {
			found = true
		}
	}

	if !found {
		t.Errorf("expected redis among the plugins, got %v", loaded.Plugins())
	}
}

// TestPluginOverrideWins covers the layer a plugin checkout supplies. It is worth
// a real test because the resolution is whole-definition rather than per-file: a
// half merged definition is a container nobody wrote down.
func TestPluginOverrideWins(t *testing.T) {
	pluginDir := t.TempDir()
	definitionDir := filepath.Join(pluginDir, "datastore", "redis")
	if err := os.MkdirAll(definitionDir, 0755); err != nil {
		t.Fatalf("unable to create the override directory: %s", err)
	}

	compose := `
services:
  redis:
    image: "{{ .Image }}:{{ .ImageVersion }}"
    volumes:
      - type: bind
        source: "{{ .HostRoot }}/data"
        target: /data
    ports:
      - name: native
        target: 6379
        primary: true
x-dokku:
  plugin: redis
  title: Valkey
  scheme: redis
  alias: REDIS
  dsn: "{{ .Scheme }}://{{ .Host }}:{{ .Port.native }}"
  wait: native
`
	write := func(name string, contents string) {
		if err := os.WriteFile(filepath.Join(definitionDir, name), []byte(contents), 0644); err != nil {
			t.Fatalf("unable to write %s: %s", name, err)
		}
	}
	write("docker-compose.yml", compose)
	write("Dockerfile", "ARG IMAGE=valkey/valkey:9.0.0\nFROM ${IMAGE}\n")

	loaded, err := Load(LoadInput{PluginDir: pluginDir})
	if err != nil {
		t.Fatalf("unable to load with an override: %s", err)
	}

	redis, ok := loaded.Definition("redis")
	if !ok {
		t.Fatal("expected redis to resolve")
	}

	if redis.Dokku.Title != "Valkey" {
		t.Errorf("expected the plugin's definition to win, got title %q", redis.Dokku.Title)
	}

	// the override replaces the embedded definition rather than joining it
	if len(redis.Service.Command) != 0 {
		t.Errorf("expected the embedded command to be gone, got %v", redis.Service.Command)
	}
}

func TestPluginWithoutOverridesFallsBack(t *testing.T) {
	loaded, err := Load(LoadInput{PluginDir: t.TempDir()})
	if err != nil {
		t.Fatalf("unable to load: %s", err)
	}

	redis, ok := loaded.Definition("redis")
	if !ok || redis.Dokku.Title != "Redis" {
		t.Error("expected the embedded definition when a plugin supplies no override")
	}
}

func TestMalformedOverrideIsAnError(t *testing.T) {
	pluginDir := t.TempDir()
	definitionDir := filepath.Join(pluginDir, "datastore", "redis")
	if err := os.MkdirAll(definitionDir, 0755); err != nil {
		t.Fatalf("unable to create the override directory: %s", err)
	}

	if err := os.WriteFile(filepath.Join(definitionDir, "docker-compose.yml"), []byte("services: {}\n"), 0644); err != nil {
		t.Fatalf("unable to write the override: %s", err)
	}

	if err := os.WriteFile(filepath.Join(definitionDir, "Dockerfile"), []byte("FROM redis\n"), 0644); err != nil {
		t.Fatalf("unable to write the dockerfile: %s", err)
	}

	// falling back would quietly run something other than what the operator
	// asked for, so a broken override has to be loud
	if _, err := Load(LoadInput{PluginDir: pluginDir}); err == nil {
		t.Error("expected a malformed override to be an error rather than a fall back")
	}
}

func TestMajorVersion(t *testing.T) {
	tests := []struct {
		version  string
		expected string
	}{
		{version: "18.4", expected: "18"},
		{version: "8.8.0", expected: "8"},
		{version: "v1.45.0", expected: "1"},
		{version: "latest", expected: ""},
		{version: "", expected: ""},
	}

	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			if actual := majorVersion(test.version); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}
