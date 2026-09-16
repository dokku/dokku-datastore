package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The map is what forty command files look a datastore up in, so a datastore
// missing from it is a command that reports an unsupported type.
func TestEveryDefinitionIsRegistered(t *testing.T) {
	redis, ok := Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	if redis.Definition.Dokku.Plugin != "redis" {
		t.Errorf("expected the redis definition, got %q", redis.Definition.Dokku.Plugin)
	}
}

// These are the values the rest of the binary reads a datastore's metadata
// from, and they are what the hand written redis service returned, so the
// forty call sites see no change.
func TestRedisProperties(t *testing.T) {
	properties := Datastores["redis"].Properties()

	tests := []struct {
		name     string
		actual   string
		expected string
	}{
		{name: "command prefix", actual: properties.CommandPrefix, expected: "redis"},
		{name: "config suffix", actual: properties.ConfigSuffix, expected: "config"},
		{name: "alt alias", actual: properties.AltAlias, expected: "DOKKU_REDIS"},
		{name: "config variable", actual: properties.ConfigVariable, expected: "REDIS_CONFIG_OPTIONS"},
		{name: "default alias", actual: properties.DefaultAlias, expected: "REDIS"},
		{name: "default image", actual: properties.DefaultImage, expected: "redis"},
		{name: "env variable", actual: properties.EnvVariable, expected: "REDIS_CUSTOM_ENV"},
		// derived from the plugin name, not the variable: graphite's is
		// GRAPHITE_DISABLE_PULL while its variable is STATSD
		{name: "image pull variable", actual: properties.ImagePullVariable, expected: "REDIS_DISABLE_PULL"},
		{name: "plugin variable", actual: properties.PluginVariable, expected: "REDIS"},
		{name: "scheme", actual: properties.Scheme, expected: "redis"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, test.actual)
			}
		})
	}

	if len(properties.Ports) != 1 || properties.Ports[0] != 6379 {
		t.Errorf("expected one port 6379, got %v", properties.Ports)
	}

	if properties.WaitPort != 6379 {
		t.Errorf("expected to wait on 6379, got %d", properties.WaitPort)
	}

	// the Dockerfile pins the default rather than floating on latest, which is
	// also how the bash plugin derives its own
	if properties.DefaultImageVersion != "8.8.0" {
		t.Errorf("expected the version the definition pins, got %q", properties.DefaultImageVersion)
	}
}

func TestRedisTitleAndType(t *testing.T) {
	datastore := Datastores["redis"]

	if datastore.Title() != "Redis" {
		t.Errorf("expected Redis, got %q", datastore.Title())
	}

	if datastore.ServiceType() != "redis" {
		t.Errorf("expected redis, got %q", datastore.ServiceType())
	}
}

// No shipped definition builds: a vendored script is mounted instead. The
// unmapping still has to be right for a definition that needs a build a mount
// cannot deliver, so it is exercised against one.
func TestPinnedImage(t *testing.T) {
	redis, ok := Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	// redis runs the image it pinned, so there is nothing to unmap
	if actual := redis.runTaggedImage("lollipop"); actual != "redis:8.8.0" {
		t.Errorf("expected redis to run the image it pinned, got %q", actual)
	}

	building := &Datastore{Definition: redis.Definition}
	building.Definition.Builds = true

	built := building.runTaggedImage("lollipop")
	if built != "dokku/datastore-redis:8.8.0" {
		t.Fatalf("expected a built tag, got %q", built)
	}

	// the built tag is an implementation detail, so a version report answers
	// which redis is running rather than which wrapper dokku built
	if actual := building.PinnedImage("lollipop", built); actual != "redis:8.8.0" {
		t.Errorf("expected redis:8.8.0, got %q", actual)
	}

	// anything else is reported verbatim, so this never hides what is running
	if actual := building.PinnedImage("lollipop", "redis:7.0.0"); actual != "redis:7.0.0" {
		t.Errorf("expected the running image verbatim, got %q", actual)
	}
}

// The payload is dokku's file rather than the operator's, so it is overwritten
// every time: a stale script left after an upgrade would be a verb running the
// previous release's code.
func TestWritePayloadOverwrites(t *testing.T) {
	redis, ok := Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	previous := DokkuLibRoot
	DokkuLibRoot = t.TempDir()
	t.Cleanup(func() {
		DokkuLibRoot = previous
	})

	serviceRoot := Folders(redis, "lollipop").Root
	if err := os.MkdirAll(serviceRoot, 0755); err != nil {
		t.Fatalf("unable to create the service root: %s", err)
	}

	script := filepath.Join(serviceRoot, "rootfs", "usr", "local", "bin", "dokku-redis-export")
	if err := os.MkdirAll(filepath.Dir(script), 0755); err != nil {
		t.Fatalf("unable to create the payload directory: %s", err)
	}

	if err := os.WriteFile(script, []byte("stale\n"), 0644); err != nil {
		t.Fatalf("unable to write the stale script: %s", err)
	}

	if err := redis.writePayload("lollipop"); err != nil {
		t.Fatalf("unable to write the payload: %s", err)
	}

	contents, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("unable to read the script: %s", err)
	}

	if string(contents) == "stale\n" {
		t.Error("expected the stale script to be replaced")
	}

	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("unable to stat the script: %s", err)
	}

	// WriteFile leaves the mode of an existing file alone, and a script that is
	// not executable fails when the verb runs rather than when it is written
	if info.Mode().Perm() != 0755 {
		t.Errorf("expected the script to be executable, got %v", info.Mode().Perm())
	}
}

// An offline verb runs in a throwaway container standing in for the service
// container, so it needs both the data and the payload. Leaving the payload out
// fails only at runtime, with "dokku-redis-import: not found".
func TestVolumesCarryTheDataAndThePayload(t *testing.T) {
	redis, ok := Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	volumes := strings.Join(redis.volumes("lollipop"), " ")

	for _, expected := range []string{
		"/services/redis/lollipop/data:/data",
		"/services/redis/lollipop/config:/usr/local/etc/redis",
		"/rootfs/usr/local/bin/dokku-redis-import:/usr/local/bin/dokku-redis-import:ro",
		"/rootfs/usr/local/bin/dokku-redis-export:/usr/local/bin/dokku-redis-export:ro",
	} {
		if !strings.Contains(volumes, expected) {
			t.Errorf("expected a mount for %q, got %v", expected, volumes)
		}
	}
}

// A definition's hook scripts run on the host rather than in the container,
// which is why they are written beside the service rather than mounted into it.
// Redis ships none, so this uses one that does.
func TestWriteScripts(t *testing.T) {
	redis, ok := Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	previous := DokkuLibRoot
	DokkuLibRoot = t.TempDir()
	t.Cleanup(func() {
		DokkuLibRoot = previous
	})

	hooked := &Datastore{Definition: redis.Definition}
	hooked.Definition.Scripts = map[string][]byte{"pre-create": []byte("#!/usr/bin/env bash\n")}

	serviceRoot := Folders(hooked, "lollipop").Root
	if err := os.MkdirAll(serviceRoot, 0755); err != nil {
		t.Fatalf("unable to create the service root: %s", err)
	}

	if err := hooked.writeScripts("lollipop"); err != nil {
		t.Fatalf("unable to write the scripts: %s", err)
	}

	script := filepath.Join(serviceRoot, "bin", "pre-create")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("unable to stat the script: %s", err)
	}

	// a hook that cannot run is a create that fails, and an embedded file has
	// no mode of its own to carry
	if info.Mode().Perm() != 0755 {
		t.Errorf("expected the hook to be executable, got %v", info.Mode().Perm())
	}
}

// Redis ships no hooks, so nothing should appear for it.
func TestWriteScriptsWritesNothingWithoutHooks(t *testing.T) {
	redis, ok := Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	previous := DokkuLibRoot
	DokkuLibRoot = t.TempDir()
	t.Cleanup(func() {
		DokkuLibRoot = previous
	})

	if err := redis.writeScripts("lollipop"); err != nil {
		t.Fatalf("unable to write the scripts: %s", err)
	}

	if _, err := os.Stat(filepath.Join(Folders(redis, "lollipop").Root, "bin")); err == nil {
		t.Error("expected no bin directory for a definition with no hooks")
	}
}

// A read path that cannot answer is worse than one that answers unmapped, and
// info is a read path.
func TestPinnedImageSurvivesAMissingDatastore(t *testing.T) {
	var missing *Datastore

	if actual := missing.PinnedImage("lollipop", "redis:8.8.0"); actual != "redis:8.8.0" {
		t.Errorf("expected the running image, got %q", actual)
	}
}

// Docker creates a missing bind source owned by root, which is how a directory
// under the service root ends up belonging to somebody the dokku user cannot
// take it away from. Postgres is the case that found this: its certificates are
// mounted by the pre-create hook, which runs before the service container
// exists at all.
func TestBindDirectoriesCoversWhatTheHooksMountToo(t *testing.T) {
	postgres, ok := Datastores["postgres"]
	if !ok {
		t.Fatal("expected postgres to be registered")
	}

	directories := postgres.BindDirectories("lollipop")
	root := Folders(postgres, "lollipop").Root

	for _, expected := range []string{root + "/data", root + "/certs"} {
		found := false
		for _, directory := range directories {
			if directory == expected {
				found = true
			}
		}

		if !found {
			t.Errorf("expected %s to be created, got %v", expected, directories)
		}
	}

	// the service root is where the whole tree has to stay, or destroy is
	// removing something it does not own
	for _, directory := range directories {
		if !strings.HasPrefix(directory, root+"/") {
			t.Errorf("expected %s to be under the service root", directory)
		}
	}
}
