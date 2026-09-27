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
	// also how the bash plugin derives its own. Which version it pins is the
	// definition's business and dependabot changes it, so what is checked is
	// that a version is pinned and that it is the one the definition carries
	if properties.DefaultImageVersion == "" || properties.DefaultImageVersion == "latest" {
		t.Errorf("expected a pinned version, got %q", properties.DefaultImageVersion)
	}

	if properties.DefaultImageVersion != Datastores["redis"].Definition.DefaultImageVersion {
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

	// taken from the definition rather than written out, because the version it
	// pins is changed by every image bump and a copy of it here would fail them
	pinned := redis.Definition.DefaultImage + ":" + redis.Definition.DefaultImageVersion

	// redis runs the image it pinned, so there is nothing to unmap
	if actual := redis.runTaggedImage("lollipop"); actual != pinned {
		t.Errorf("expected redis to run the image it pinned, got %q", actual)
	}

	building := &Datastore{Definition: redis.Definition}
	building.Definition.Builds = true

	built := building.runTaggedImage("lollipop")
	if expected := "dokku/datastore-redis:" + redis.Definition.DefaultImageVersion; built != expected {
		t.Fatalf("expected a built tag %q, got %q", expected, built)
	}

	// the built tag is an implementation detail, so a version report answers
	// which redis is running rather than which wrapper dokku built
	if actual := building.PinnedImage("lollipop", built); actual != pinned {
		t.Errorf("expected %s, got %q", pinned, actual)
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

// The word a disabled pull leaves behind reads "<service> service <action>
// failed", so it has to name something an operator recognises. A subcommand's
// own name does; a hook and a trigger are named for where they sit in the plugin
// rather than for anything anybody typed.
func TestVerbAction(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{name: "export", expected: "export"},
		{name: "connect", expected: "connect"},
		{name: "hooks.pre_create", expected: "creation"},
		{name: "hooks.post_create", expected: "creation"},
		{name: "triggers.post-app-clone-setup", expected: "post-app-clone-setup"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := verbAction(test.name); actual != test.expected {
				t.Errorf("expected %q to be reported as %q, got %q", test.name, test.expected, actual)
			}
		})
	}
}

// The secrets are what --password and --root-password override, so a value
// given to create has to be the one written, the environment is only a fallback,
// and a secret already on disk is never replaced.
func TestWriteSecrets(t *testing.T) {
	mysql, ok := Datastores["mysql"]
	if !ok {
		t.Fatal("expected mysql to be registered")
	}

	tests := []struct {
		name         string
		overrides    map[string]string
		env          map[string]string
		existing     map[string]string
		expected     map[string]string
		generatedLen map[string]int
	}{
		{
			name:      "overrides are written",
			overrides: map[string]string{PasswordEnv: "given", RootPasswordEnv: "given-root"},
			expected:  map[string]string{"PASSWORD": "given", "ROOTPASSWORD": "given-root"},
		},
		{
			name:      "an override wins over the environment",
			overrides: map[string]string{PasswordEnv: "given"},
			env:       map[string]string{PasswordEnv: "from-env", RootPasswordEnv: "root-from-env"},
			expected:  map[string]string{"PASSWORD": "given", "ROOTPASSWORD": "root-from-env"},
		},
		{
			name:         "a secret with neither is generated",
			overrides:    map[string]string{RootPasswordEnv: "given-root"},
			expected:     map[string]string{"ROOTPASSWORD": "given-root"},
			generatedLen: map[string]int{"PASSWORD": 16},
		},
		{
			name:         "an empty override is generated",
			overrides:    map[string]string{PasswordEnv: ""},
			generatedLen: map[string]int{"PASSWORD": 16, "ROOTPASSWORD": 16},
		},
		{
			name:      "a secret already on disk is kept",
			overrides: map[string]string{PasswordEnv: "given", RootPasswordEnv: "given-root"},
			existing:  map[string]string{"PASSWORD": "kept"},
			expected:  map[string]string{"PASSWORD": "kept", "ROOTPASSWORD": "given-root"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(PasswordEnv, "")
			t.Setenv(RootPasswordEnv, "")
			for key, value := range test.env {
				t.Setenv(key, value)
			}

			serviceRoot := withServiceRoot(t, mysql, "lollipop")
			for file, value := range test.existing {
				if err := os.WriteFile(filepath.Join(serviceRoot, file), []byte(value), 0600); err != nil {
					t.Fatalf("unable to write %s: %s", file, err)
				}
			}

			if err := mysql.writeSecrets("lollipop", test.overrides); err != nil {
				t.Fatalf("unable to write the secrets: %s", err)
			}

			for file, expected := range test.expected {
				contents, err := os.ReadFile(filepath.Join(serviceRoot, file))
				if err != nil {
					t.Fatalf("unable to read %s: %s", file, err)
				}

				if strings.TrimSpace(string(contents)) != expected {
					t.Errorf("expected %s to hold %q, got %q", file, expected, contents)
				}
			}

			for file, length := range test.generatedLen {
				contents, err := os.ReadFile(filepath.Join(serviceRoot, file))
				if err != nil {
					t.Fatalf("unable to read %s: %s", file, err)
				}

				if len(strings.TrimSpace(string(contents))) != length {
					t.Errorf("expected a generated %s of %d characters, got %q", file, length, contents)
				}
			}
		})
	}
}

// A password given for a secret the definition does not have would be dropped,
// so it is refused instead, naming the flag it came from.
func TestCheckSecretOverrides(t *testing.T) {
	tests := []struct {
		name      string
		datastore string
		overrides map[string]string
		expected  string
	}{
		{name: "mysql takes both", datastore: "mysql", overrides: map[string]string{PasswordEnv: "a", RootPasswordEnv: "b"}},
		{name: "redis takes a password", datastore: "redis", overrides: map[string]string{PasswordEnv: "a"}},
		{name: "nothing given", datastore: "memcached", overrides: map[string]string{}},
		{name: "empty values are not given", datastore: "memcached", overrides: map[string]string{PasswordEnv: "", RootPasswordEnv: ""}},
		{name: "redis has no root password", datastore: "redis", overrides: map[string]string{PasswordEnv: "a", RootPasswordEnv: "b"}, expected: "--root-password"},
		{name: "memcached has no password", datastore: "memcached", overrides: map[string]string{PasswordEnv: "a"}, expected: "--password"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			datastore, ok := Datastores[test.datastore]
			if !ok {
				t.Fatalf("expected %s to be registered", test.datastore)
			}

			err := CheckSecretOverrides(datastore.Definition, test.overrides)
			if test.expected == "" {
				if err != nil {
					t.Errorf("expected no error, got %s", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected an error naming %s", test.expected)
			}

			if !strings.Contains(err.Error(), test.expected) || !strings.Contains(err.Error(), test.datastore) {
				t.Errorf("expected an error naming %s and %s, got %s", test.expected, test.datastore, err)
			}
		})
	}
}
