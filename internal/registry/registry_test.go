package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
		"backup", "backup-auth", "backup-deauth", "backup-logs", "backup-schedule",
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
// places: here and in the tool. An image a command names is now fetched when the
// command runs, so this is no longer what stands between a service and an image
// nothing ever pulled; it is what keeps couchdb from adding a fifth image to
// every host that installs it.
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
		"export":               couchdb.Dokku.Commands["export"],
		"export.all_databases": *couchdb.Dokku.Commands["export"].AllDatabases,
		"import":               couchdb.Dokku.Commands["import"],
		"import.all_databases": *couchdb.Dokku.Commands["import"].AllDatabases,
		"reset":                couchdb.Dokku.Commands["reset"],
		"hooks.post_create":    *couchdb.Dokku.Hooks.PostCreate,
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

	// the operator's own config, and the one that turns tls on
	for _, target := range []string{"/etc/rabbitmq/rabbitmq.conf", "/etc/rabbitmq/conf.d/20-dokku-tls.conf"} {
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
}

// A service exposed before rabbitmq served tls holds a host port for each of
// the four ports it had then, in their order. The tls ports come after them, so
// each of those host ports stays on the container port it was exposed on and
// the tls ports are the ones left unexposed.
func TestRabbitmqTLSPortsComeLast(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	rabbitmq, ok := loaded.Definition("rabbitmq")
	if !ok {
		t.Fatal("expected a rabbitmq definition")
	}

	targets := []int{}
	for _, port := range rabbitmq.Service.Ports {
		targets = append(targets, port.Target)
	}

	expected := []int{5672, 4369, 35197, 15672, 5671, 15671}
	if !slices.Equal(targets, expected) {
		t.Errorf("expected the rabbitmq ports to be %v, got %v", expected, targets)
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

// The versions differ in one setting: security, which seven predates and the
// later lines turn on by default, which is the reason they are separate
// definitions rather than one.
func TestElasticsearchVersionsDifferWhereTheyShould(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	seven, _ := loaded.Definition("elasticsearch-7")
	nine, _ := loaded.Definition("elasticsearch-9")

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

// Elasticsearch sizes its own heap from the container's memory limit unless
// elasticsearch.yml names a legacy role setting, in which case it pins the heap
// to a gigabyte whatever the limit. That is what kept a seven with a 512m limit
// from ever starting. The heap is set from the environment, so nothing in the
// create hook edits jvm.options either: since 7.11 it names no heap to edit.
func TestElasticsearchLeavesTheHeapToTheMemoryLimit(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	legacyRoleSettings := []string{
		"node.master",
		"node.ingest",
		"node.data",
		"node.voting_only",
		"node.ml",
		"node.transform",
		"node.remote_cluster_client",
	}

	for _, variant := range []string{"elasticsearch-7", "elasticsearch-8", "elasticsearch-9"} {
		t.Run(variant, func(t *testing.T) {
			found, ok := loaded.Definition(variant)
			if !ok {
				t.Fatalf("expected a %s definition", variant)
			}

			content := found.Configs["elasticsearch_yml"].Content
			for _, setting := range legacyRoleSettings {
				if strings.Contains(content, setting) {
					t.Errorf("expected no %s, which pins the heap to a gigabyte", setting)
				}
			}

			if found.Dokku.Hooks.PreCreate == nil {
				t.Fatal("expected a pre create hook")
			}

			for _, part := range found.Dokku.Hooks.PreCreate.Exec {
				if strings.Contains(part, "jvm.options") {
					t.Errorf("expected the pre create hook to leave jvm.options alone, got %q", part)
				}
			}
		})
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

// A flavor is its own line of majors, sorted after the datastore's own. A
// flavor's name does not end in a bare number, and reading it as version zero
// would sort pg18 before pg17 whenever the names happened to.
func TestFlavorsAreOrderedAfterTheDatastoresOwn(t *testing.T) {
	names := []string{
		"postgres-timescaledb-pg18",
		"postgres-pgvector-pg18",
		"postgres-18",
		"postgres-pgvector-pg17",
		"postgres-timescaledb-pg17",
		"postgres-17",
	}
	sortVariants(names)

	expected := []string{
		"postgres-17",
		"postgres-18",
		"postgres-pgvector-pg17",
		"postgres-pgvector-pg18",
		"postgres-timescaledb-pg17",
		"postgres-timescaledb-pg18",
	}
	if strings.Join(names, " ") != strings.Join(expected, " ") {
		t.Errorf("expected %v, got %v", expected, names)
	}
}

func TestParseVariant(t *testing.T) {
	tests := []struct {
		name     string
		expected variant
	}{
		{name: "redis", expected: variant{}},
		{name: "postgres-18", expected: variant{major: 18}},
		{name: "solr-10", expected: variant{major: 10}},
		{name: "postgres-pgvector-pg17", expected: variant{flavor: "pgvector", prefix: "pg", major: 17}},
		{name: "postgres-postgis-pg18", expected: variant{flavor: "postgis", prefix: "pg", major: 18}},
		{name: "postgres-pgvector", expected: variant{flavor: "pgvector"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := parseVariant(test.name); actual != test.expected {
				t.Errorf("expected %+v, got %+v", test.expected, actual)
			}
		})
	}
}

// Each flavor writes its tags its own way, and the major has to come out of
// all of them: pgvector and timescaledb lead with their own version and carry
// postgres's as pg17, and postgis leads with postgres's.
func TestTagMajor(t *testing.T) {
	tests := []struct {
		version  string
		prefix   string
		expected int
		ok       bool
	}{
		{version: "18.4", prefix: "", expected: 18, ok: true},
		{version: "17-3.5", prefix: "pg", expected: 17, ok: true},
		{version: "pg17", prefix: "pg", expected: 17, ok: true},
		{version: "0.8.6-pg18", prefix: "pg", expected: 18, ok: true},
		{version: "0.8.6-pg17-trixie", prefix: "pg", expected: 17, ok: true},
		{version: "2.30.1-pg17", prefix: "pg", expected: 17, ok: true},
		{version: "latest-pg18", prefix: "pg", expected: 18, ok: true},
		// the leading number is the extension's own version, which is what
		// used to be read as the major
		{version: "0.8.6", prefix: "pg", expected: 0, ok: true},
		{version: "latest", prefix: "pg", ok: false},
		{version: "", prefix: "", ok: false},
	}

	for _, test := range tests {
		t.Run(test.prefix+"/"+test.version, func(t *testing.T) {
			actual, ok := tagMajor(test.version, test.prefix)
			if ok != test.ok || actual != test.expected {
				t.Errorf("expected (%d, %t), got (%d, %t)", test.expected, test.ok, actual, ok)
			}
		})
	}
}

// The image picks the flavor, and a flavor is never picked for an image it does
// not ship: the datastore's own image, one no definition ships, and no image at
// all each land on the datastore's own definitions, which is where they ran
// before flavors existed.
func TestForImagePicksTheFlavor(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	tests := []struct {
		image    string
		version  string
		expected string
	}{
		{image: "", version: "", expected: "postgres-18"},
		{image: "", version: "pg17", expected: "postgres-18"},
		{image: "postgres", version: "17.11", expected: "postgres-17"},
		{image: "ghcr.io/example/postgres", version: "17.11", expected: "postgres-17"},
		{image: "pgvector/pgvector", version: "pg17", expected: "postgres-pgvector-pg17"},
		{image: "pgvector/pgvector", version: "", expected: "postgres-pgvector-pg18"},
		{image: "postgis/postgis", version: "17-3.5", expected: "postgres-postgis-pg17"},
		{image: "timescale/timescaledb", version: "2.30.1-pg17", expected: "postgres-timescaledb-pg17"},
		{image: "timescale/timescaledb", version: "2.30.1-pg18", expected: "postgres-timescaledb-pg18"},
	}

	for _, test := range tests {
		t.Run(test.image+":"+test.version, func(t *testing.T) {
			found, err := loaded.ForImage("postgres", test.image, test.version)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if found.Name != test.expected {
				t.Errorf("expected %s, got %s", test.expected, found.Name)
			}
		})
	}
}

// Every flavor is postgres on another image, so it has to stay postgres in
// everything but the image: the same plugin, and its data mounted where the
// datastore's own definition of the same major mounts it, since a service moved
// between the two by an upgrade keeps its data directory.
func TestFlavorsMountTheirDataWhereTheirMajorDoes(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	checked := 0
	for _, name := range loaded.NamesFor("postgres") {
		found := parseVariant(name)
		if found.flavor == "" {
			continue
		}

		checked++
		t.Run(name, func(t *testing.T) {
			flavor, _ := loaded.Definition(name)
			own, ok := loaded.Definition(fmt.Sprintf("postgres-%d", found.major))
			if !ok {
				t.Fatalf("expected a postgres-%d definition beside it", found.major)
			}

			if flavor.Dokku.Plugin != "postgres" {
				t.Errorf("expected plugin postgres, got %q", flavor.Dokku.Plugin)
			}

			if flavor.Service.Volumes[0] != own.Service.Volumes[0] {
				t.Errorf("mounts its data as %+v, but %s mounts it as %+v",
					flavor.Service.Volumes[0], own.Name, own.Service.Volumes[0])
			}
		})
	}

	if checked == 0 {
		t.Error("expected at least one flavor to check")
	}
}

// timescaledb ships no openssl, so its certificate is made in the plain postgres
// image of the same major. It is held to the tag that definition pins, so that a
// host running both pulls one image rather than two; a dependabot bump of the
// postgres Dockerfile fails here until the hook is moved with it.
func TestTimescaledbMakesItsCertificateInThePostgresImage(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	for _, major := range []int{17, 18} {
		name := fmt.Sprintf("postgres-timescaledb-pg%d", major)
		t.Run(name, func(t *testing.T) {
			timescaledb, ok := loaded.Definition(name)
			if !ok {
				t.Fatalf("expected a %s definition", name)
			}

			own, _ := loaded.Definition(fmt.Sprintf("postgres-%d", major))
			expected := own.DefaultImage + ":" + own.DefaultImageVersion
			if actual := timescaledb.Dokku.Hooks.PreCreate.Image; actual != expected {
				t.Errorf("expected the pre_create hook to run in %s, got %q; update %s/docker-compose.yml",
					expected, actual, name)
			}
		})
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

// Postgres moved its data directory up one level in eighteen, which is the
// whole reason the two lines have definitions of their own.
func TestPostgresMountsItsDataWhereTheVersionKeepsIt(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	tests := []struct {
		name     string
		variant  string
		expected string
	}{
		{name: "before eighteen", variant: "postgres-17", expected: "/var/lib/postgresql/data"},
		{name: "eighteen and since", variant: "postgres-18", expected: "/var/lib/postgresql"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found, ok := loaded.Definition(test.variant)
			if !ok {
				t.Fatalf("expected a %s definition", test.variant)
			}

			data := ""
			for _, volume := range found.Service.Volumes {
				if volume.Target != "/certs" {
					data = volume.Target
				}
			}

			if data != test.expected {
				t.Errorf("expected the data at %s, got %s", test.expected, data)
			}
		})
	}
}

// The configuration file postgres writes during its own initialisation does not
// exist until the service has run once, which is why the bash plugin pauses the
// service to edit it and starts it again. Saying the same thing in the server's
// own options needs no service to have run.
func TestPostgresTurnsSslOnWithoutRestarting(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	postgres, ok := loaded.Definition("postgres-18")
	if !ok {
		t.Fatal("expected a postgres-18 definition")
	}

	rendered, err := definition.RenderAll(postgres.Service.Command, postgres.WithTargets(definition.Scope{}))
	if err != nil {
		t.Fatalf("unable to render the command: %s", err)
	}

	command := strings.Join(rendered, " ")
	for _, expected := range []string{"ssl=on", "ssl_cert_file=/certs/server.crt", "ssl_key_file=/certs/server.key"} {
		if !strings.Contains(command, expected) {
			t.Errorf("expected the server to be told %s, got %q", expected, command)
		}
	}

	// the certificates are read from wherever the service mounts them
	rendered, err = definition.RenderAll(postgres.Service.Command, postgres.WithTargets(definition.Scope{Target: map[string]string{"certs": "/tls"}}))
	if err != nil {
		t.Fatalf("unable to render the command: %s", err)
	}

	command = strings.Join(rendered, " ")
	for _, expected := range []string{"ssl_cert_file=/tls/server.crt", "ssl_key_file=/tls/server.key"} {
		if !strings.Contains(command, expected) {
			t.Errorf("expected the server to be told %s, got %q", expected, command)
		}
	}

	// nothing runs after the service is up, because nothing needs to
	if postgres.Dokku.Hooks.PostCreate != nil {
		t.Error("expected nothing to run after the service is up")
	}

	if postgres.Dokku.Hooks.PreCreate == nil {
		t.Error("expected the certificate to be made before the service runs")
	}
}

// Every postgres definition turns ssl on, so every one of them has to hand out
// its certificate and document how a client uses it. The certificate is read
// from wherever the service mounts it, as the server does.
func TestEveryPostgresDefinitionDocumentsItsCertificate(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	names := loaded.NamesFor("postgres")
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			postgres, _ := loaded.Definition(name)
			certificate, ok := postgres.Dokku.CustomCommands["certificate"]
			if !ok {
				t.Fatal("expected a certificate command")
			}

			for target, expected := range map[string]string{"": "/certs/server.crt", "/tls": "/tls/server.crt"} {
				scope := definition.Scope{}
				if target != "" {
					scope.Target = map[string]string{"certs": target}
				}

				rendered, err := definition.RenderAll(certificate.Exec, postgres.WithTargets(scope))
				if err != nil {
					t.Fatalf("unable to render the command: %s", err)
				}

				if strings.Join(rendered, " ") != "cat "+expected {
					t.Errorf("expected the certificate to be read from %s, got %q", expected, rendered)
				}
			}

			documented := map[string]bool{}
			for _, section := range postgres.Dokku.Documentation {
				documented[section.Title] = true
			}

			if !documented["Encrypting connections with TLS"] {
				t.Error("expected the tls section")
			}

			// the flavors' images share the official image's entrypoint, so
			// every one of them reads the initdb arguments the section names
			if !documented["Choosing the database encoding and locale"] {
				t.Error("expected the encoding section")
			}
		})
	}

	if len(names) != 8 {
		t.Errorf("expected eight postgres definitions, got %v", names)
	}
}

// Rethinkdb is the first definition with more ports than a name to spare, and
// the one everything addresses is neither the lowest numbered nor the first the
// bash plugin listed. Naming them is what stops readiness waiting on the
// administrative web page instead of the port a driver connects on.
func TestRethinkdbAddressesTheDriverPort(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	rethinkdb, ok := loaded.Definition("rethinkdb")
	if !ok {
		t.Fatal("expected a rethinkdb definition")
	}

	primary, ok := rethinkdb.PrimaryPort()
	if !ok {
		t.Fatal("expected a primary port")
	}

	if primary.Target != 28015 {
		t.Errorf("expected the driver port, got %d", primary.Target)
	}

	wait, ok := rethinkdb.PortFor(rethinkdb.Dokku.Wait)
	if !ok {
		t.Fatalf("expected the wait port %q to be named", rethinkdb.Dokku.Wait)
	}

	if wait.Target != 28015 {
		t.Errorf("expected readiness on the driver port, got %d", wait.Target)
	}

	for _, name := range []string{"native", "cluster", "http"} {
		if _, ok := rethinkdb.PortFor(name); !ok {
			t.Errorf("expected a port named %s", name)
		}
	}
}

// The bash plugin offers connect and then fails with "Not yet implemented"
// once it has been called. A definition says the same thing by leaving the
// command out, which is a refusal rather than a failure.
func TestRethinkdbOffersNothingItCannotDo(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	rethinkdb, ok := loaded.Definition("rethinkdb")
	if !ok {
		t.Fatal("expected a rethinkdb definition")
	}

	for _, subcommand := range []string{"connect", "export", "import", "clone", "backup"} {
		if rethinkdb.Implements(subcommand) {
			t.Errorf("expected rethinkdb not to implement %s", subcommand)
		}
	}

	// the image starts with an open admin account, so there is no credential
	// to generate and none in the url
	if len(rethinkdb.Dokku.Secrets) != 0 {
		t.Errorf("expected no secrets, got %v", rethinkdb.Dokku.Secrets)
	}

	// the queries a reset needs go through the administrative endpoint, which
	// needs no client
	if !rethinkdb.Implements("reset") {
		t.Error("expected rethinkdb to implement reset")
	}
}

// Every datastore that keeps data can have it deleted. nats and pushpin keep
// none - nats holds no messages without jetstream, and pushpin only proxies - so
// a reset of either would be a restart, and they say so by leaving it out.
func TestEveryDatastoreWithDataCanBeReset(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	stateless := map[string]bool{"nats": true, "pushpin": true}
	for _, name := range loaded.Names() {
		t.Run(name, func(t *testing.T) {
			parsed, _ := loaded.Definition(name)
			if implements := parsed.Implements("reset"); implements == stateless[parsed.Dokku.Plugin] {
				t.Errorf("expected reset to be implemented=%t, got %t", !stateless[parsed.Dokku.Plugin], implements)
			}
		})
	}
}

// Every datastore that exports a server able to hold more than one database
// can export every one of them, which is what its backups hold. redis dumps
// the whole server already, so it has nothing more to offer.
func TestEveryDatastoreWithManyDatabasesExportsThemAll(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	many := map[string]bool{
		"clickhouse": true,
		"couchdb":    true,
		"mariadb":    true,
		"mongo":      true,
		"mysql":      true,
		"postgres":   true,
	}
	for _, name := range loaded.Names() {
		t.Run(name, func(t *testing.T) {
			parsed, _ := loaded.Definition(name)
			if exports := parsed.ExportsAllDatabases(); exports != many[parsed.Dokku.Plugin] {
				t.Errorf("expected every database to be exported=%t, got %t", many[parsed.Dokku.Plugin], exports)
			}
		})
	}
}

// The every-database dumps are written once and copied to every postgres
// definition, as the reset is, so that no major or flavor dumps them otherwise.
func TestEveryDatabaseDumpsAreTheSameForPostgres(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	script := func(name string, verb string) string {
		t.Helper()

		parsed, ok := loaded.Definition(name)
		if !ok {
			t.Fatalf("expected a %s definition", name)
		}

		command := parsed.Dokku.Commands[verb].AllDatabases
		if command == nil || len(command.Exec) < 3 || command.Exec[0] != "sh" {
			t.Fatalf("expected %s to %s every database with an inline script", name, verb)
		}

		return command.Exec[2]
	}

	postgres := loaded.NamesFor("postgres")
	for _, verb := range []string{"export", "import"} {
		for _, name := range postgres[1:] {
			if script(name, verb) != script(postgres[0], verb) {
				t.Errorf("expected %s to %s every database as %s does", name, verb, postgres[0])
			}
		}
	}
}

// A reset is written once and copied to every definition that runs it, since a
// definition embeds what it runs rather than pointing at another's. A copy that
// drifted would leave one major or flavor deleting its data differently.
func TestResetsAreTheSameWhereTheDatastoreIs(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	resetScript := func(name string) string {
		t.Helper()

		parsed, ok := loaded.Definition(name)
		if !ok {
			t.Fatalf("expected a %s definition", name)
		}

		reset, ok := parsed.Dokku.Commands["reset"]
		if !ok {
			t.Fatalf("expected %s to declare a reset", name)
		}

		// the script, without the label and the arguments after it
		if len(reset.Exec) < 3 || reset.Exec[0] != "sh" {
			t.Fatalf("expected %s to reset with an inline script, got %q", name, reset.Exec)
		}

		return reset.Exec[2]
	}

	rootfsScript := func(name string, path string) string {
		t.Helper()

		parsed, ok := loaded.Definition(name)
		if !ok {
			t.Fatalf("expected a %s definition", name)
		}

		contents, ok := parsed.Rootfs[path]
		if !ok {
			t.Fatalf("expected %s to ship %s", name, path)
		}

		return string(contents)
	}

	postgres := loaded.NamesFor("postgres")
	if len(postgres) != 8 {
		t.Errorf("expected eight postgres definitions, got %v", postgres)
	}
	for _, name := range postgres[1:] {
		if resetScript(name) != resetScript(postgres[0]) {
			t.Errorf("expected %s to reset as %s does", name, postgres[0])
		}
	}

	if resetScript("mariadb") != resetScript("mysql") {
		t.Error("expected mariadb to reset as mysql does")
	}

	elasticsearch := loaded.NamesFor("elasticsearch")
	if len(elasticsearch) != 3 {
		t.Errorf("expected three elasticsearch definitions, got %v", elasticsearch)
	}
	for _, name := range elasticsearch[1:] {
		path := "usr/local/bin/dokku-elasticsearch-reset"
		if rootfsScript(name, path) != rootfsScript(elasticsearch[0], path) {
			t.Errorf("expected %s to reset as %s does", name, elasticsearch[0])
		}
	}
}

// Pushpin has five ports and the one a linked app is handed is neither the
// first the bash plugin listed by number nor the one clients are served on.
// Naming them is what keeps the url and the readiness wait on the port an app
// publishes to.
func TestPushpinPublishesWhereTheUrlPoints(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	pushpin, ok := loaded.Definition("pushpin")
	if !ok {
		t.Fatal("expected a pushpin definition")
	}

	expected := map[string]int{
		"publish":   5561,
		"http":      7999,
		"push":      5560,
		"subscribe": 5562,
		"command":   5563,
	}

	for name, target := range expected {
		port, ok := pushpin.PortFor(name)
		if !ok {
			t.Errorf("expected a port named %s", name)
			continue
		}

		if port.Target != target {
			t.Errorf("expected %s on %d, got %d", name, target, port.Target)
		}
	}

	primary, ok := pushpin.PrimaryPort()
	if !ok {
		t.Fatal("expected a primary port")
	}

	if primary.Name != "publish" {
		t.Errorf("expected the publish port to be primary, got %s", primary.Name)
	}

	if pushpin.Dokku.Wait != "publish" {
		t.Errorf("expected readiness on the publish port, got %s", pushpin.Dokku.Wait)
	}

	// WEBSOCKET rather than PUSHPIN, which is not derivable from the name
	if pushpin.Dokku.Alias != "WEBSOCKET" {
		t.Errorf("expected WEBSOCKET, got %s", pushpin.Dokku.Alias)
	}
}

// The bash plugin puts the service's own name in the url while creating an
// account called omnisci, so the url it hands a linked app names a user that
// was never made. The account the post-create step makes is the one the url
// has to name.
func TestOmnisciNamesTheAccountItMakes(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	omnisci, ok := loaded.Definition("omnisci")
	if !ok {
		t.Fatal("expected an omnisci definition")
	}

	if !strings.Contains(omnisci.Dokku.DSN, "://omnisci:") {
		t.Errorf("expected the url to name the omnisci account, got %q", omnisci.Dokku.DSN)
	}

	if strings.Contains(omnisci.Dokku.DSN, "ServiceName") {
		t.Errorf("expected the url not to name the service, got %q", omnisci.Dokku.DSN)
	}

	if omnisci.Dokku.Hooks.PostCreate == nil {
		t.Fatal("expected the account and the database to be made after the service answers")
	}
}

// The image ships one administrative account whose password is the same in
// every installation of it. The bash plugin writes that password to disk and
// then sets the password to what it just read, so it never changes: a
// generated one is what makes the step it already meant to take a real one.
func TestOmnisciGeneratesItsRootPassword(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	omnisci, ok := loaded.Definition("omnisci")
	if !ok {
		t.Fatal("expected an omnisci definition")
	}

	root, ok := omnisci.Dokku.Secrets["root_password"]
	if !ok {
		t.Fatal("expected a root password to be generated")
	}

	if root.File != "ROOTPASSWORD" {
		t.Errorf("expected ROOTPASSWORD, got %s", root.File)
	}

	if root.Length == 0 {
		t.Error("expected a generated length rather than a fixed value")
	}

	// what --root-password is handed down as
	if root.Env != "SERVICE_ROOT_PASSWORD" {
		t.Errorf("expected --root-password to be wired up, got %q", root.Env)
	}

	// the password that ships with the image belongs in the script that has to
	// authenticate with it once, not in anything that is written to disk
	for name, secret := range omnisci.Dokku.Secrets {
		if secret.Length == 0 {
			t.Errorf("expected %s to be generated", name)
		}
	}
}

// Graphite is the definition the port names were introduced for: it has five,
// the one a linked app is handed is not the one readiness waits on, and the
// bash plugin distinguishes them only by position in a list.
func TestGraphiteNamesEveryPortItNeeds(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	graphite, ok := loaded.Definition("graphite")
	if !ok {
		t.Fatal("expected a graphite definition")
	}

	expected := map[string]int{
		"statsd":       8125,
		"statsd_admin": 8126,
		"web":          80,
		"web_alt":      81,
		"carbon":       2003,
	}

	for name, target := range expected {
		port, ok := graphite.PortFor(name)
		if !ok {
			t.Errorf("expected a port named %s", name)
			continue
		}

		if port.Target != target {
			t.Errorf("expected %s on %d, got %d", name, target, port.Target)
		}
	}

	// the url is the metrics port and readiness is grafana, which is the third
	// entry in the list the bash plugin has to count along
	primary, ok := graphite.PrimaryPort()
	if !ok {
		t.Fatal("expected a primary port")
	}

	if primary.Name != "statsd" {
		t.Errorf("expected statsd to be primary, got %s", primary.Name)
	}

	wait, ok := graphite.PortFor(graphite.Dokku.Wait)
	if !ok {
		t.Fatal("expected the wait port to be named")
	}

	if wait.Target != 80 {
		t.Errorf("expected readiness on grafana, got %d", wait.Target)
	}

	// what an exposed service publishes it over, so a client off the host that
	// sends metrics reaches it
	statsd, _ := graphite.PortFor("statsd")
	if statsd.Protocol != "udp" {
		t.Errorf("expected statsd to be recorded as udp, got %q", statsd.Protocol)
	}
}

// Every service the bash plugin creates gets an empty grafana password: it
// passes GRAPHITE_PASSWORD from a variable its create function never assigns.
func TestGraphitePassesThePasswordItGenerates(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	graphite, ok := loaded.Definition("graphite")
	if !ok {
		t.Fatal("expected a graphite definition")
	}

	secret, ok := graphite.Dokku.Secrets["password"]
	if !ok {
		t.Fatal("expected a password to be generated")
	}

	if secret.File != "PASSWORD" {
		t.Errorf("expected PASSWORD, got %s", secret.File)
	}

	password, ok := graphite.Service.Environment["GRAPHITE_PASSWORD"]
	if !ok {
		t.Fatal("expected the password to reach the container")
	}

	if !strings.Contains(password, ".Secret.password") {
		t.Errorf("expected the generated password, got %q", password)
	}

	// STATSD, which is not derivable from the datastore's name
	if graphite.Dokku.Alias != "STATSD" || graphite.Dokku.Variable != "STATSD" {
		t.Errorf("expected STATSD, got alias %s and variable %s", graphite.Dokku.Alias, graphite.Dokku.Variable)
	}
}

// Graphite is the one definition with a udp port, and the one that shows why
// readiness and the url are separate questions: the port an app sends metrics
// to cannot be probed by connecting to it, so a tcp port is named to wait on
// instead. A definition that did not do that is refused at parse.
func TestGraphiteWaitsOnAPortThatCanBeProbed(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	graphite, ok := loaded.Definition("graphite")
	if !ok {
		t.Fatal("expected a graphite definition")
	}

	primary, ok := graphite.PrimaryPort()
	if !ok {
		t.Fatal("expected a primary port")
	}

	if primary.Protocol != definition.ProtocolUDP {
		t.Errorf("expected the primary port to speak udp, got %q", primary.Protocol)
	}

	wait, ok := graphite.WaitPort()
	if !ok {
		t.Fatal("expected a wait port")
	}

	if wait.Protocol == definition.ProtocolUDP {
		t.Errorf("expected readiness on a port it can connect to, got %s", wait.Name)
	}

	if wait.Name == primary.Name {
		t.Error("expected readiness to be somewhere other than the metrics port")
	}
}

// Every other definition leaves the protocol unset, which is tcp. This is the
// check that a definition does not pick up a udp port by accident, since a udp
// port is now refused as a readiness target and would fail to load.
func TestEveryDefinitionCanBeWaitedFor(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	for _, name := range loaded.Names() {
		found, ok := loaded.Definition(name)
		if !ok {
			t.Fatalf("expected a %s definition", name)
		}

		wait, ok := found.WaitPort()
		if !ok {
			continue
		}

		if wait.Protocol == definition.ProtocolUDP {
			t.Errorf("%s would wait on a udp port", name)
		}
	}
}

// Graphite is the only definition that needs root for part of what it does, and
// the split is the point: the script that runs on the host is unprivileged and
// ships in bin/, and only the part that writes under /etc is privileged.
func TestGraphiteSplitsThePrivilegedPart(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	graphite, ok := loaded.Definition("graphite")
	if !ok {
		t.Fatal("expected a graphite definition")
	}

	if _, ok := graphite.Privileged["nginx"]; !ok {
		t.Errorf("expected a privileged nginx helper, got %v", graphite.Privileged)
	}

	for _, name := range []string{"nginx-expose", "nginx-unexpose"} {
		script, ok := graphite.Scripts[name]
		if !ok {
			t.Fatalf("expected graphite to ship %s on the host", name)
		}

		// the unprivileged half asks for root rather than assuming it
		if !strings.Contains(string(script), "sudo ") {
			t.Errorf("expected %s to reach root through the helper", name)
		}

		declared, ok := graphite.CommandFor(name)
		if !ok {
			t.Fatalf("expected %s to be declared", name)
		}

		if declared.Mode != definition.ModeHost {
			t.Errorf("expected %s to run on the host, got %q", name, declared.Mode)
		}

		if declared.Description == "" {
			t.Errorf("expected %s to be documented", name)
		}
	}

	// and they are extra commands rather than base verbs, so they reach users
	// through invoke and no other datastore has to refuse them
	if _, base := graphite.Dokku.Commands["nginx-expose"]; base {
		t.Error("expected nginx-expose to be a custom command rather than a base verb")
	}

	if !graphite.ImplementsCustom("nginx-expose") {
		t.Error("expected graphite to implement nginx-expose")
	}
}

// Nothing else ships one, so the mechanism grants root to exactly one script in
// the whole registry.
func TestOnlyGraphiteShipsAPrivilegedScript(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	for _, name := range loaded.Names() {
		found, ok := loaded.Definition(name)
		if !ok {
			t.Fatalf("expected a %s definition", name)
		}

		if len(found.Privileged) > 0 && name != "graphite" {
			t.Errorf("%s ships a privileged script, which needs saying out loud", name)
		}
	}
}

// Solr is the only datastore that implements a dokku trigger. dokku finds a
// trigger by the name of a file in the plugin directory, so a plugin ships one
// either way; what the definition carries is the work, so that the file can be
// generated rather than written by hand.
func TestSolrImplementsThePostExtractTrigger(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	for _, variant := range []string{"solr-7", "solr-8"} {
		t.Run(variant, func(t *testing.T) {
			solr, ok := loaded.Definition(variant)
			if !ok {
				t.Fatalf("expected a %s definition", variant)
			}

			declared, ok := solr.TriggerFor("post-extract")
			if !ok {
				t.Fatalf("expected %s to implement post-extract, got %v", variant, solr.TriggerNames())
			}

			// on the host: the build it reads is a host directory, and the core
			// it writes to is the host side of a bind mount
			if declared.Mode != definition.ModeHost {
				t.Errorf("expected the trigger to run on the host, got %q", declared.Mode)
			}

			if _, ok := solr.Scripts["post-extract"]; !ok {
				t.Errorf("expected %s to ship the script it runs, got %v", variant, solr.Scripts)
			}

			// dokku passes the app, the directory and the revision, in that order
			names := []string{}
			for _, argument := range declared.Arguments {
				names = append(names, argument.Name)
			}

			if strings.Join(names, ",") != "app,directory,revision" {
				t.Errorf("expected the arguments dokku passes, got %v", names)
			}
		})
	}
}

// Nothing else implements one, so the mechanism is exercised by exactly one
// datastore and every other plugin ships no trigger file at all.
func TestOnlySolrImplementsATrigger(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	for _, name := range loaded.Names() {
		found, ok := loaded.Definition(name)
		if !ok {
			t.Fatalf("expected a %s definition", name)
		}

		if len(found.TriggerNames()) > 0 && !strings.HasPrefix(name, "solr") {
			t.Errorf("%s implements a trigger, which needs saying out loud", name)
		}
	}
}

// writeOverride lays out one definition in a plugin checkout.
func writeOverride(t *testing.T, pluginDir string, name string, plugin string, image string) {
	t.Helper()

	definitionDir := filepath.Join(pluginDir, "datastore", name)
	if err := os.MkdirAll(definitionDir, 0755); err != nil {
		t.Fatalf("unable to create %s: %s", definitionDir, err)
	}

	compose := fmt.Sprintf(`
services:
  %s:
    image: "{{ .Image }}:{{ .ImageVersion }}"
    ports:
      - name: native
        target: 6379
        primary: true
x-dokku:
  plugin: %s
  title: Shipped
  scheme: %s
  alias: SHIPPED
  dsn: "{{ .Scheme }}://{{ .Host }}:{{ .Port.native }}"
  wait: native
`, plugin, plugin, plugin)

	files := map[string]string{
		"docker-compose.yml": compose,
		"Dockerfile":         fmt.Sprintf("ARG IMAGE=%s\nFROM ${IMAGE}\n", image),
	}
	for filename, contents := range files {
		if err := os.WriteFile(filepath.Join(definitionDir, filename), []byte(contents), 0644); err != nil {
			t.Fatalf("unable to write %s: %s", filename, err)
		}
	}
}

// A plugin that ships definitions ships all of them. Replacing only the ones it
// happens to name would leave a service pinned to an embedded variant running a
// definition its plugin never shipped and cannot see.
func TestAnOverrideReplacesTheWholeDatastore(t *testing.T) {
	pluginDir := t.TempDir()
	writeOverride(t, pluginDir, "postgres-18", "postgres", "postgres:18.4")

	loaded, err := Load(LoadInput{PluginDir: pluginDir})
	if err != nil {
		t.Fatalf("unable to load with an override: %s", err)
	}

	if _, ok := loaded.Definition("postgres-17"); ok {
		t.Error("expected the embedded postgres-17 to be dropped for a plugin shipping its own postgres")
	}

	if names := loaded.NamesFor("postgres"); len(names) != 1 || names[0] != "postgres-18" {
		t.Errorf("expected only the shipped postgres-18, got %v", names)
	}

	shipped, ok := loaded.Definition("postgres-18")
	if !ok || shipped.Dokku.Title != "Shipped" {
		t.Error("expected the plugin's own postgres-18")
	}
}

// A plugin may support fewer or differently named versions than the binary does,
// and a definition it ships is reachable whatever it is called. Before the
// replacement rule a checkout shipping an unversioned name loaded it, sorted it
// first, and never selected it.
func TestAnOverrideNamedWithoutAVersionIsStillSelected(t *testing.T) {
	pluginDir := t.TempDir()
	writeOverride(t, pluginDir, "postgres", "postgres", "postgres:18.4")

	loaded, err := Load(LoadInput{PluginDir: pluginDir})
	if err != nil {
		t.Fatalf("unable to load with an override: %s", err)
	}

	for _, imageVersion := range []string{"", "17.8", "18.4"} {
		found, err := loaded.For("postgres", imageVersion)
		if err != nil {
			t.Fatalf("unable to resolve postgres at %q: %s", imageVersion, err)
		}

		if found.Name != "postgres" {
			t.Errorf("expected the shipped postgres at %q, got %s", imageVersion, found.Name)
		}
	}
}

// A plugin ships the definitions for its own datastore and no other. Without
// this a redis checkout could add a postgres definition and the redis plugin
// would answer for a datastore it does not install or document.
func TestOverridesMustAgreeOnThePlugin(t *testing.T) {
	pluginDir := t.TempDir()
	writeOverride(t, pluginDir, "redis", "redis", "redis:8.8.0")
	writeOverride(t, pluginDir, "postgres-18", "postgres", "postgres:18.4")

	_, err := Load(LoadInput{PluginDir: pluginDir})
	if err == nil {
		t.Fatal("expected a checkout mixing datastores to be refused")
	}

	if !strings.Contains(err.Error(), "one datastore") {
		t.Errorf("expected the error to explain the rule, got %q", err)
	}
}

// The directory name is not the rule: a plugin may name its definition anything,
// and for a variant the name is not the plugin's name anyway.
func TestAnOverrideDirectoryNeedNotMatchThePlugin(t *testing.T) {
	pluginDir := t.TempDir()
	writeOverride(t, pluginDir, "valkey", "redis", "valkey/valkey:9.0.0")

	loaded, err := Load(LoadInput{PluginDir: pluginDir})
	if err != nil {
		t.Fatalf("unable to load a differently named definition: %s", err)
	}

	found, err := loaded.For("redis", "")
	if err != nil {
		t.Fatalf("unable to resolve redis: %s", err)
	}

	if found.Name != "valkey" {
		t.Errorf("expected the shipped valkey definition, got %s", found.Name)
	}
}

// An override taking a name another datastore already holds would leave that
// datastore pointing at a definition that is not its own.
func TestAnOverrideCannotTakeAnotherDatastoresName(t *testing.T) {
	pluginDir := t.TempDir()
	writeOverride(t, pluginDir, "mongo", "redis", "redis:8.8.0")

	_, err := Load(LoadInput{PluginDir: pluginDir})
	if err == nil {
		t.Fatal("expected a name another datastore holds to be refused")
	}

	if !strings.Contains(err.Error(), "already named this") {
		t.Errorf("expected the error to name the collision, got %q", err)
	}
}

// A datastore split by major version keeps one definition per major so that a
// service created on an older one goes on running it. That only holds while each
// of those definitions stays inside its own major: postgres-17 pinned to
// postgres 19 would be a service that asked for 17 and got a data directory
// nineteen expects.
//
// The newest of each is deliberately not checked. It is the one a new service
// gets, it is free to move ahead, and solr-8 already runs solr 10 for that
// reason. A flavor is its own line of majors, so postgres-pgvector-pg17 is
// checked for staying on pg17 while postgres-pgvector-pg18 is its newest.
func TestAnOlderVariantStaysInsideItsMajor(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	checked := 0
	for _, plugin := range loaded.Plugins() {
		byFlavor := map[string][]string{}
		for _, name := range loaded.NamesFor(plugin) {
			flavor := parseVariant(name).flavor
			byFlavor[flavor] = append(byFlavor[flavor], name)
		}

		for _, names := range byFlavor {
			if len(names) < 2 {
				continue
			}

			// sorted oldest first, so everything but the last is an older variant
			for _, name := range names[:len(names)-1] {
				checked++
				t.Run(name, func(t *testing.T) {
					found, _ := loaded.Definition(name)
					expected := parseVariant(name)

					actual, ok := tagMajor(found.DefaultImageVersion, expected.prefix)
					if !ok || actual != expected.major {
						t.Errorf("is pinned to %s, which is major %d rather than %d",
							found.DefaultImageVersion, actual, expected.major)
					}
				})
			}
		}
	}

	if checked == 0 {
		t.Error("expected at least one older variant to check")
	}
}

// Extra arguments are only declared where the tool reads its argv. redis and
// couchdb dump and load with scripts that never look at theirs, so declaring it
// there would drop an operator's arguments without a word.
func TestExtraArgsAreDeclaredWhereTheToolReadsThem(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	accepts := map[string]bool{
		"mariadb":     true,
		"mongo":       true,
		"mysql":       true,
		"postgres-17": true,
		"postgres-18": true,
		// the flavors run the same pg_dump and pg_restore, timescaledb's
		// restore wrapped to stop its background workers around it

		"postgres-pgvector-pg17":    true,
		"postgres-pgvector-pg18":    true,
		"postgres-postgis-pg17":     true,
		"postgres-postgis-pg18":     true,
		"postgres-timescaledb-pg17": true,
		"postgres-timescaledb-pg18": true,
	}

	for _, name := range loaded.Names() {
		t.Run(name, func(t *testing.T) {
			parsed, ok := loaded.Definition(name)
			if !ok {
				t.Fatalf("%s is listed but does not resolve", name)
			}

			for _, verb := range []string{"export", "import"} {
				command, declared := parsed.CommandFor(verb)
				if accepts[name] && !declared {
					t.Errorf("expected %s to declare %s", name, verb)
				}

				if actual := declared && command.ExtraArgs; actual != accepts[name] {
					t.Errorf("expected %s %s to take extra arguments to be %t, got %t", name, verb, accepts[name], actual)
				}
			}
		})
	}
}

// The case the issue was opened for: a service named mysql was handed the
// server's own mysql database. Postgres is the other side of the line: its
// postgres database is only the default one, and a service named after it works.
func TestReservedNamesAreTheDatastoresOwnDatabases(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load: %s", err)
	}

	tests := []struct {
		definition string
		name       string
		reserved   bool
	}{
		{definition: "mysql", name: "mysql", reserved: true},
		{definition: "mariadb", name: "mysql", reserved: true},
		{definition: "postgres-17", name: "template1", reserved: true},
		{definition: "postgres-18", name: "template1", reserved: true},
		{definition: "postgres-17", name: "postgres", reserved: false},
		{definition: "postgres-18", name: "postgres", reserved: false},
		{definition: "mongo", name: "admin", reserved: true},
		{definition: "clickhouse", name: "default", reserved: false},
		{definition: "redis", name: "redis", reserved: false},
	}

	for _, test := range tests {
		t.Run(test.definition+"/"+test.name, func(t *testing.T) {
			parsed, ok := loaded.Definition(test.definition)
			if !ok {
				t.Fatalf("expected an embedded %s definition", test.definition)
			}

			reserved := false
			for _, name := range parsed.Dokku.ReservedNames {
				if name == test.name {
					reserved = true
				}
			}

			if reserved != test.reserved {
				t.Errorf("expected %s reserved to be %t, got %t (%v)", test.name, test.reserved, reserved, parsed.Dokku.ReservedNames)
			}
		})
	}
}

// A definition naming one of its volumes' paths reads it from the scope, so
// every template it has must render against the targets every service gets by
// default. Parsing checks the names; this checks they render.
func TestEveryDefinitionRendersWithItsDefaultTargets(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	for _, name := range loaded.Names() {
		t.Run(name, func(t *testing.T) {
			found, ok := loaded.Definition(name)
			if !ok {
				t.Fatalf("expected a %s definition", name)
			}

			scope := definition.Scope{
				ServiceName: "lollipop",
				Database:    "lollipop",
				Image:       found.DefaultImage,
				Scheme:      found.Dokku.Scheme,
				Secret:      map[string]string{},
				Port:        map[string]int{},
				Args:        map[string]string{},
			}
			for secret := range found.Dokku.Secrets {
				scope.Secret[secret] = "hunter2"
			}
			for _, port := range found.Service.Ports {
				scope.Port[port.Name] = port.Target
			}

			bodies := []string{found.Service.Image, found.Dokku.DSN}
			bodies = append(bodies, found.Service.Command...)
			for _, value := range found.Service.Environment {
				bodies = append(bodies, value)
			}

			commands := []definition.Command{}
			for _, hook := range []*definition.Command{found.Dokku.Hooks.PreCreate, found.Dokku.Hooks.PostCreate} {
				if hook != nil {
					commands = append(commands, *hook)
				}
			}
			// an upgrade step is the one thing told where the old data went
			scope.PreviousData = "data.previous.20261001T000000"
			for _, group := range []map[string]definition.Command{found.Dokku.Commands, found.Dokku.CustomCommands, found.Dokku.Triggers, found.Dokku.Upgrade.From} {
				for _, command := range group {
					commands = append(commands, command)
				}
			}
			for _, command := range commands {
				for _, argument := range command.Arguments {
					scope.Args[argument.Name] = "argument"
				}
				bodies = append(bodies, command.Exec...)
				for _, value := range command.Env {
					bodies = append(bodies, value)
				}
			}

			scope = found.WithTargets(scope)
			for _, body := range bodies {
				if _, err := definition.Render(body, scope); err != nil {
					t.Errorf("unable to render %q: %s", body, err)
				}
			}
		})
	}
}

// A volume's key is its source under the service root, and a service moves a
// volume by naming it, so renaming a source strands every service that moved
// that volume. Pinned here so that doing it is a decision rather than a side
// effect.
func TestVolumeKeysArePinned(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	postgres := []string{"data", "certs"}
	expected := map[string][]string{
		"clickhouse":                {"config", "data"},
		"couchdb":                   {"config", "data"},
		"elasticsearch-7":           {"config", "data"},
		"elasticsearch-8":           {"config", "data"},
		"elasticsearch-9":           {"config", "data"},
		"graphite":                  {"data/grafana", "data/whisper", "data/graphite-web/graphite.db"},
		"mariadb":                   {"config", "data"},
		"meilisearch":               {"data"},
		"memcached":                 {},
		"mongo":                     {"config", "data", "configdb", "initdb"},
		"mysql":                     {"config", "data"},
		"nats":                      {},
		"omnisci":                   {"data"},
		"postgres-17":               postgres,
		"postgres-18":               postgres,
		"postgres-pgvector-pg17":    postgres,
		"postgres-pgvector-pg18":    postgres,
		"postgres-postgis-pg17":     postgres,
		"postgres-postgis-pg18":     postgres,
		"postgres-timescaledb-pg17": postgres,
		"postgres-timescaledb-pg18": postgres,
		"pushpin":                   {"config"},
		"rabbitmq":                  {"config/rabbitmq.conf", "data", "certs", "config/tls.conf"},
		"redis":                     {"config", "data"},
		"rethinkdb":                 {"data"},
		"solr-7":                    {"data"},
		"solr-8":                    {"data"},
		"typesense":                 {"config", "data"},
	}

	for _, name := range loaded.Names() {
		found, ok := loaded.Definition(name)
		if !ok {
			t.Fatalf("expected a %s definition", name)
		}

		keys, pinned := expected[name]
		if !pinned {
			t.Errorf("the %s definition's volume keys are not pinned here, got %v", name, found.VolumeKeys())
			continue
		}

		if strings.Join(found.VolumeKeys(), " ") != strings.Join(keys, " ") {
			t.Errorf("expected the %s definition's volumes to be %v, got %v", name, keys, found.VolumeKeys())
		}
	}
}

// A step migrating data in place is keyed by the definition a service is moved
// off, which has to be one of the same plugin at a lower major: pg_upgrade only
// goes forward, and a step keyed by a name the registry does not have would
// never run. The image it runs is the one published for that pair of majors.
func TestUpgradeStepsMoveForwardWithinAPlugin(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	steps := 0
	for _, name := range loaded.Names() {
		found, _ := loaded.Definition(name)
		for from, step := range found.Dokku.Upgrade.From {
			steps++
			t.Run(name+" from "+from, func(t *testing.T) {
				previous, ok := loaded.Definition(from)
				if !ok {
					t.Fatalf("expected %s to be a definition", from)
				}

				if previous.Dokku.Plugin != found.Dokku.Plugin {
					t.Errorf("expected %s to be a %s definition, got %s", from, found.Dokku.Plugin, previous.Dokku.Plugin)
				}

				before, after := parseVariant(from), parseVariant(name)
				if before.major == 0 || before.major >= after.major {
					t.Errorf("expected %s to be an older major than %s", from, name)
				}

				if expected := fmt.Sprintf(":%d-to-%d", before.major, after.major); !strings.HasSuffix(step.Image, expected) {
					t.Errorf("expected the step to run an image tagged %s, got %q", expected, step.Image)
				}
			})
		}
	}

	if steps == 0 {
		t.Error("expected at least one step migrating data in place")
	}
}

// Every postgres definition migrates the data of a service moved onto it, since
// none of them reads another's cluster, and every definition that migrates can
// remove the data a migration kept aside, the same way wherever it is declared.
func TestEveryMigratingDefinitionCleansUpTheSameWay(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	for _, name := range loaded.NamesFor("postgres") {
		found, _ := loaded.Definition(name)
		if !found.Dokku.Upgrade.Migrate {
			t.Errorf("expected %s to migrate", name)
		}
	}

	var expected *definition.Command
	for _, name := range loaded.Names() {
		found, _ := loaded.Definition(name)
		if !found.Dokku.Upgrade.Migrate {
			continue
		}

		cleanup, ok := found.Dokku.CustomCommands["upgrade-cleanup"]
		if !ok {
			t.Errorf("expected %s to declare upgrade-cleanup", name)
			continue
		}

		if expected == nil {
			expected = &cleanup
			continue
		}

		if strings.Join(cleanup.Exec, "\n") != strings.Join(expected.Exec, "\n") || cleanup.User != expected.User || cleanup.Mode != expected.Mode {
			t.Errorf("expected %s to clean up as the others do", name)
		}
	}

	if expected == nil {
		t.Fatal("expected a definition that migrates")
	}

	// the data an upgrade kept aside is named data.<definition>.<timestamp>,
	// which is all the command may match: the data the service runs on is
	// beside it
	script := strings.Join(expected.Exec, " ")
	if !strings.Contains(script, "-name 'data.*'") || !strings.Contains(script, "-mindepth 1 -maxdepth 1") {
		t.Errorf("expected the cleanup to match only the directories an upgrade keeps, got %q", script)
	}
}

// A migration between postgres definitions carries every database and role, so
// every one of them dumps the whole cluster and replays it the same way: the
// export of one is the import of any other.
func TestPostgresMigratesTheWholeCluster(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	names := loaded.NamesFor("postgres")
	first, _ := loaded.Definition(names[0])
	for _, name := range names {
		found, _ := loaded.Definition(name)
		upgrade := found.Dokku.Upgrade
		if upgrade.Export == nil || upgrade.Import == nil {
			t.Fatalf("expected %s to declare an export and an import for a migration", name)
		}

		if upgrade.Export.Exec[0] != "pg_dumpall" {
			t.Errorf("expected %s to dump the whole cluster, got %q", name, upgrade.Export.Exec)
		}

		if strings.Join(upgrade.Export.Exec, " ") != strings.Join(first.Dokku.Upgrade.Export.Exec, " ") {
			t.Errorf("expected %s to export as %s does", name, names[0])
		}

		if strings.Join(upgrade.Import.Exec, "\n") != strings.Join(first.Dokku.Upgrade.Import.Exec, "\n") {
			t.Errorf("expected %s to import as %s does", name, names[0])
		}
	}
}

// A migration between postgres definitions is refused before anything is
// touched when the new image cannot restore the extensions the data runs, so
// every one of them reads and checks those the same way: what one requires is
// what any other checks.
func TestPostgresChecksAMigrationTheSameWay(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	names := loaded.NamesFor("postgres")
	first, _ := loaded.Definition(names[0])
	for _, name := range names {
		found, _ := loaded.Definition(name)
		upgrade := found.Dokku.Upgrade
		if upgrade.Requires == nil || upgrade.Check == nil {
			t.Fatalf("expected %s to declare a requires and a check for a migration", name)
		}

		if strings.Join(upgrade.Requires.Exec, "\n") != strings.Join(first.Dokku.Upgrade.Requires.Exec, "\n") {
			t.Errorf("expected %s to read what its data requires as %s does", name, names[0])
		}

		if strings.Join(upgrade.Check.Exec, "\n") != strings.Join(first.Dokku.Upgrade.Check.Exec, "\n") {
			t.Errorf("expected %s to check a migration as %s does", name, names[0])
		}
	}
}
