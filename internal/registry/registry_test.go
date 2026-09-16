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

// Rabbitmq mounts its configuration as a file rather than a directory. Docker
// makes a missing bind source a directory, so the file has to be seeded before
// the container is created, and the definition has to declare it for that to
// happen at all.
func TestRabbitmqSeedsTheFileItMounts(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	rabbitmq, ok := loaded.Definition("rabbitmq")
	if !ok {
		t.Fatal("expected a rabbitmq definition")
	}

	target := "/etc/rabbitmq/rabbitmq.conf"

	mounted := false
	for _, volume := range rabbitmq.Service.Volumes {
		if volume.Target == target {
			mounted = true
		}
	}

	if !mounted {
		t.Fatalf("expected %s to be mounted", target)
	}

	seeded := false
	for _, config := range rabbitmq.Service.Configs {
		if config.Target == target {
			seeded = true
		}
	}

	if !seeded {
		t.Errorf("expected %s to be seeded, or docker will make it a directory", target)
	}
}

// Clickhouse answers on two ports with two protocols, and an app says which it
// wants by overriding the scheme. The url has to follow: the http port takes no
// database in the path and the native one does, so the two are not the same url
// with a different number in it.
func TestClickhouseUrlFollowsTheScheme(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	clickhouse, ok := loaded.Definition("clickhouse")
	if !ok {
		t.Fatal("expected a clickhouse definition")
	}

	scope := definition.Scope{
		ServiceName: "analytics",
		Database:    "analytics",
		Host:        "dokku-clickhouse-analytics",
		Secret:      map[string]string{"password": "hunter2"},
		Port:        map[string]int{"native": 9000, "http": 8123},
	}

	tests := []struct {
		name     string
		scheme   string
		expected string
	}{
		{
			name:     "the native protocol",
			scheme:   "clickhouse",
			expected: "clickhouse://analytics:hunter2@dokku-clickhouse-analytics:9000/analytics",
		},
		{
			// no database in the path: over http the database is a parameter of
			// the query rather than part of the address
			name:     "an app asking for http",
			scheme:   "http",
			expected: "http://analytics:hunter2@dokku-clickhouse-analytics:8123",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scope.Scheme = test.scheme
			actual, err := definition.Render(clickhouse.Dokku.DSN, scope)
			if err != nil {
				t.Fatalf("unable to render the dsn: %s", err)
			}

			if actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// Elasticsearch is the first datastore split by major version, so it is the
// first to be selected between: a service says which it runs by the version it
// pinned, and gets the definition for that line rather than the newest.
func TestElasticsearchVariantFollowsTheImageVersion(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	tests := []struct {
		name         string
		imageVersion string
		expected     string
	}{
		{name: "a seven", imageVersion: "7.17.28", expected: "elasticsearch-7"},
		{name: "an eight", imageVersion: "8.19.10", expected: "elasticsearch-8"},
		{name: "a nine", imageVersion: "9.4.1", expected: "elasticsearch-9"},
		{
			// a service whose container is gone has no version recorded, and
			// reporting on it must not be an error
			name:         "nothing recorded",
			imageVersion: "",
			expected:     "elasticsearch-9",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found, err := loaded.For("elasticsearch", test.imageVersion)
			if err != nil {
				t.Fatalf("unable to select a definition: %s", err)
			}

			if found.Name != test.expected {
				t.Errorf("expected %s, got %s", test.expected, found.Name)
			}
		})
	}
}

// The versions differ in one setting: seven names a master node explicitly and
// the later lines dropped that, which is the whole reason they are separate
// definitions rather than one.
func TestElasticsearchVersionsDifferWhereTheyShould(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	seven, _ := loaded.Definition("elasticsearch-7")
	nine, _ := loaded.Definition("elasticsearch-9")

	if !strings.Contains(seven.Configs["elasticsearch_yml"].Content, "node.master") {
		t.Error("expected seven to name a master node")
	}

	if strings.Contains(nine.Configs["elasticsearch_yml"].Content, "node.master") {
		t.Error("expected nine to have dropped the master node setting")
	}

	// security arrived on by default in eight, and the plugin hands over an
	// address with nothing to authenticate with
	if strings.Contains(seven.Configs["elasticsearch_yml"].Content, "xpack.security") {
		t.Error("expected seven not to mention security, which it predates")
	}

	if !strings.Contains(nine.Configs["elasticsearch_yml"].Content, "xpack.security.enabled: false") {
		t.Error("expected nine to turn security off")
	}

	// they are one datastore, however many definitions describe it
	for _, found := range []definition.Definition{seven, nine} {
		if found.Dokku.Plugin != "elasticsearch" {
			t.Errorf("expected %s to be the elasticsearch plugin, got %q", found.Name, found.Dokku.Plugin)
		}
	}
}

// Solr moved where it keeps its cores in eight, which is the whole reason the
// two lines have definitions of their own: the same data directory has to be
// mounted somewhere different.
func TestSolrMountsItsCoresWhereTheVersionKeepsThem(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	tests := []struct {
		name     string
		variant  string
		expected string
	}{
		{name: "before eight", variant: "solr-7", expected: "/opt/solr/server/solr/mycores"},
		{name: "eight and since", variant: "solr-8", expected: "/var/solr/data"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found, ok := loaded.Definition(test.variant)
			if !ok {
				t.Fatalf("expected a %s definition", test.variant)
			}

			if len(found.Service.Volumes) != 1 {
				t.Fatalf("expected one volume, got %d", len(found.Service.Volumes))
			}

			if found.Service.Volumes[0].Target != test.expected {
				t.Errorf("expected the cores at %s, got %s", test.expected, found.Service.Volumes[0].Target)
			}
		})
	}
}

// Solr ships a tenth major version, so the fallback a service with no recorded
// version takes has to count rather than compare names: ten sorts before seven
// as a word.
func TestVariantsAreOrderedByNumber(t *testing.T) {
	names := []string{"solr-8", "solr-10", "solr-7"}
	sortVariants(names)

	expected := []string{"solr-7", "solr-8", "solr-10"}
	for i, name := range expected {
		if names[i] != name {
			t.Fatalf("expected %v, got %v", expected, names)
		}
	}

	// a datastore that is not split by version is unaffected
	single := []string{"redis"}
	sortVariants(single)
	if single[0] != "redis" {
		t.Errorf("expected redis, got %v", single)
	}
}

// Solr's default image is a tenth major version, and there is no definition
// named for it, so it takes the newest line rather than erroring or taking the
// oldest.
func TestSolrTakesTheNewestLineForAVersionWithNoDefinition(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	found, err := loaded.For("solr", "10.0.0")
	if err != nil {
		t.Fatalf("unable to select a definition: %s", err)
	}

	if found.Name != "solr-8" {
		t.Errorf("expected solr-8, got %s", found.Name)
	}

	seven, err := loaded.For("solr", "7.7.3")
	if err != nil {
		t.Fatalf("unable to select a definition: %s", err)
	}

	if seven.Name != "solr-7" {
		t.Errorf("expected solr-7, got %s", seven.Name)
	}
}
