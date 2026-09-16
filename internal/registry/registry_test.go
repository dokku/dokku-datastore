package registry

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"strings"
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

func TestOverrideReadsBinAndRootfsAsTrees(t *testing.T) {
	pluginDir := t.TempDir()
	definitionDir := filepath.Join(pluginDir, "datastore", "redis")

	write := func(name string, contents string) {
		t.Helper()

		full := filepath.Join(definitionDir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("unable to create the directory for %s: %s", name, err)
		}

		if err := os.WriteFile(full, []byte(contents), 0644); err != nil {
			t.Fatalf("unable to write %s: %s", name, err)
		}
	}

	write("docker-compose.yml", `
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
  title: Redis
  scheme: redis
  alias: REDIS
  dsn: "{{ .Scheme }}://{{ .Host }}:{{ .Port.native }}"
  wait: native
`)
	write("Dockerfile", "ARG IMAGE=redis:8.8.0\nFROM ${IMAGE}\nCOPY rootfs/ /\n")
	write("bin/pre-create", "#!/usr/bin/env bash\n")
	// rootfs is a tree, not a flat list, so a walk is what lets a definition
	// place a file anywhere in the image
	write("rootfs/usr/local/bin/dokku-redis-export", "#!/usr/bin/env bash\n")

	loaded, err := Load(LoadInput{PluginDir: pluginDir})
	if err != nil {
		t.Fatalf("unable to load with an override: %s", err)
	}

	redis, ok := loaded.Definition("redis")
	if !ok {
		t.Fatal("expected redis to resolve")
	}

	if _, ok := redis.Scripts["pre-create"]; !ok {
		t.Errorf("expected bin/pre-create, got %v", keys(redis.Scripts))
	}

	if _, ok := redis.Rootfs["usr/local/bin/dokku-redis-export"]; !ok {
		t.Errorf("expected the nested rootfs file, got %v", keys(redis.Rootfs))
	}

	// a Dockerfile that copies a payload in is a real build, unlike the
	// eighteen that only declare a base
	if !redis.Builds {
		t.Error("expected a definition copying a payload in to build")
	}

	if redis.DefaultImage != "redis" || redis.DefaultImageVersion != "8.8.0" {
		t.Errorf("expected redis:8.8.0, got %s:%s", redis.DefaultImage, redis.DefaultImageVersion)
	}
}

func keys(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	sort.Strings(names)
	return names
}

// What a datastore cannot do is derived from what it declares, so the list the
// bash plugin maintained by hand is worth checking against once: memcached is a
// cache, so it has nothing to dump and nothing built on dumping.
func TestMemcachedImplementsOnlyWhatItCan(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	memcached, ok := loaded.Definition("memcached")
	if !ok {
		t.Fatal("expected a memcached definition")
	}

	for _, subcommand := range []string{
		"backup", "backup-auth", "backup-deauth", "backup-schedule",
		"backup-schedule-cat", "backup-set-encryption", "backup-unschedule",
		"backup-unset-encryption", "clone", "export", "import",
	} {
		if memcached.Implements(subcommand) {
			t.Errorf("expected memcached not to implement %s", subcommand)
		}
	}

	if !memcached.Implements("connect") {
		t.Error("expected memcached to implement connect")
	}

	// no credentials and nothing on disk, which is what makes it the simplest
	// definition there is
	if len(memcached.Dokku.Secrets) != 0 {
		t.Errorf("expected no secrets, got %v", memcached.Dokku.Secrets)
	}

	if len(memcached.Service.Volumes) != 0 {
		t.Errorf("expected no volumes, got %v", memcached.Service.Volumes)
	}
}

// Couchdb's sidecar image is one dokku already ships, so it is pinned in two
// places: here and in the tool. They have to be the same image, or a service
// would reach for one that was never pulled.
func TestCouchdbSidecarImageIsOnePluginShips(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	couchdb, ok := loaded.Definition("couchdb")
	if !ok {
		t.Fatal("expected a couchdb definition")
	}

	commands := map[string]definition.Command{
		"export":            couchdb.Dokku.Commands["export"],
		"import":            couchdb.Dokku.Commands["import"],
		"hooks.post_create": *couchdb.Dokku.Hooks.PostCreate,
	}

	for name, command := range commands {
		if command.Image != hostenv.S3BackupImage {
			t.Errorf("%s runs in %q, which is not the image the plugin pulls (%q)", name, command.Image, hostenv.S3BackupImage)
		}
	}
}

// The bash plugin downloads a dump tool into the running container on every
// export and import, from a branch rather than a tag.
func TestCouchdbFetchesNothingAtRuntime(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	couchdb, _ := loaded.Definition("couchdb")
	for name, command := range couchdb.Dokku.Commands {
		for _, argument := range command.Exec {
			if strings.Contains(argument, "http://") || strings.Contains(argument, "https://") {
				t.Errorf("%s reaches out to %q", name, argument)
			}
		}
	}

	for path := range couchdb.Rootfs {
		if strings.Contains(string(couchdb.Rootfs[path]), "githubusercontent") {
			t.Errorf("%s fetches from github", path)
		}
	}
}
