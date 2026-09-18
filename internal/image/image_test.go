package image

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/registry"
)

// built is a definition that bakes a vendored script into the image, which is
// the only kind that needs a build.
func built() definition.Definition {
	return definition.Definition{
		Name:                "redis",
		Dockerfile:          []byte("FROM redis:8.8.0\nCOPY rootfs/ /\n"),
		DefaultImage:        "redis",
		DefaultImageVersion: "8.8.0",
		Builds:              true,
		Rootfs: map[string][]byte{
			"usr/local/bin/dokku-redis-export": []byte("#!/usr/bin/env bash\n"),
			"etc/redis/notes.txt":              []byte("not a program\n"),
		},
	}
}

func TestTag(t *testing.T) {
	// the definition name rather than the plugin name, so postgres-17 and
	// postgres-18 cannot produce the same tag
	if expected := "dokku/datastore-postgres-18:18.1"; Tag("postgres-18", "18.1") != expected {
		t.Errorf("expected %q, got %q", expected, Tag("postgres-18", "18.1"))
	}
}

func TestBuildArgs(t *testing.T) {
	tests := []struct {
		name     string
		input    BuildInput
		expected string
	}{
		{
			name:     "the version the Dockerfile pins",
			input:    BuildInput{Definition: built()},
			expected: "image build --build-arg IMAGE=redis:8.8.0 --file /ctx/Dockerfile --tag dokku/datastore-redis:8.8.0 /ctx",
		},
		{
			// --image and --image-version have to keep working for a definition
			// that builds, which is the whole reason for the ARG IMAGE idiom
			name:     "a version the service pinned",
			input:    BuildInput{Definition: built(), ImageVersion: "7.4.1"},
			expected: "image build --build-arg IMAGE=redis:7.4.1 --file /ctx/Dockerfile --tag dokku/datastore-redis:7.4.1 /ctx",
		},
		{
			name:     "an image the service pinned",
			input:    BuildInput{Definition: built(), Image: "valkey/valkey", ImageVersion: "8.1.0"},
			expected: "image build --build-arg IMAGE=valkey/valkey:8.1.0 --file /ctx/Dockerfile --tag dokku/datastore-redis:8.1.0 /ctx",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := strings.Join(BuildArgs(test.input, "/ctx"), " "); actual != test.expected {
				t.Errorf("expected:\n%s\ngot:\n%s", test.expected, actual)
			}
		})
	}
}

func TestWriteContext(t *testing.T) {
	directory := t.TempDir()

	if err := WriteContext(built(), directory); err != nil {
		t.Fatalf("unable to write the build context: %s", err)
	}

	dockerfile, err := os.ReadFile(filepath.Join(directory, "Dockerfile"))
	if err != nil {
		t.Fatalf("unable to read the Dockerfile: %s", err)
	}

	if !strings.Contains(string(dockerfile), "COPY rootfs/ /") {
		t.Errorf("the Dockerfile did not survive: %q", string(dockerfile))
	}

	script := filepath.Join(directory, "rootfs", "usr", "local", "bin", "dokku-redis-export")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("unable to stat the vendored script: %s", err)
	}

	// an embedded file loses its executable bit, and a vendored script arriving
	// in the image unable to run fails at export time rather than at build time
	if info.Mode().Perm() != 0755 {
		t.Errorf("expected the script to be executable, got %v", info.Mode().Perm())
	}

	notes, err := os.Stat(filepath.Join(directory, "rootfs", "etc", "redis", "notes.txt"))
	if err != nil {
		t.Fatalf("unable to stat the data file: %s", err)
	}

	if notes.Mode().Perm() != 0644 {
		t.Errorf("expected the data file to stay 0644, got %v", notes.Mode().Perm())
	}
}

func TestModeFor(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected fs.FileMode
	}{
		{name: "usr local bin", path: "usr/local/bin/dokku-redis-export", expected: 0755},
		{name: "usr bin", path: "usr/bin/probe", expected: 0755},
		{name: "sbin", path: "usr/sbin/entrypoint", expected: 0755},
		{name: "a config file", path: "etc/redis/notes.txt", expected: 0644},
		{name: "a file named bin", path: "etc/bin", expected: 0644},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := ModeFor(test.path); actual != test.expected {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

// Which definitions build is worth stating rather than discovering: a build is
// slower than a pull and fails differently on a host with a constrained builder,
// and each one needs its own matrix leg. Building the definitions that only
// declare a base would turn every trigger-install into a build for no gain.
func TestWhichEmbeddedDefinitionsBuild(t *testing.T) {
	// none: a definition that vendors a script mounts it into the container
	// instead, which keeps every datastore on the pull path. Building stays for
	// a payload a mount cannot deliver, such as a compiled tool or a package.
	builds := map[string]bool{}

	loaded, err := registry.Load(registry.LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	for _, name := range loaded.Names() {
		found, ok := loaded.Definition(name)
		if !ok {
			t.Fatalf("expected a definition named %s", name)
		}

		if found.Builds != builds[name] {
			t.Errorf("%s builds=%v, which this test does not know about", name, found.Builds)
		}
	}
}

// A vendored script that arrives in the image unable to run fails at export
// time, long after anyone would connect it to the Dockerfile.
func TestEmbeddedRootfsScriptsAreExecutable(t *testing.T) {
	loaded, err := registry.Load(registry.LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	for _, name := range loaded.Names() {
		found, _ := loaded.Definition(name)
		for file := range found.Rootfs {
			if !strings.Contains(file, "/bin/") {
				continue
			}

			if ModeFor(file) != 0755 {
				t.Errorf("%s: %s is not executable", name, file)
			}
		}
	}
}

func TestBuildSkipsATrivialDefinition(t *testing.T) {
	subject := built()
	subject.Builds = false

	tag, err := Build(t.Context(), BuildInput{Definition: subject})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if tag != "" {
		t.Errorf("expected no tag for a definition that is pulled, got %q", tag)
	}
}

// A definition pins its image on a plain FROM line, because that is the only
// shape dependabot reads. A build needs the base to be replaceable, so the build
// argument is introduced into the context rather than committed.
func TestWriteContextIntroducesTheBuildArgument(t *testing.T) {
	directory := t.TempDir()
	if err := WriteContext(built(), directory); err != nil {
		t.Fatalf("unable to write the context: %s", err)
	}

	contents, err := os.ReadFile(filepath.Join(directory, "Dockerfile"))
	if err != nil {
		t.Fatalf("unable to read the written Dockerfile: %s", err)
	}

	expected := "ARG IMAGE=redis:8.8.0\nFROM ${IMAGE}\nCOPY rootfs/ /\n"
	if string(contents) != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, contents)
	}
}

// The pair that matters: the context declares the argument and BuildArgs supplies
// it, so a service created at another version is built on the version it asked
// for. A Dockerfile without the ARG would ignore the --build-arg silently, which
// is a service running the wrong base with nothing to show for it.
func TestABuiltServiceFollowsTheVersionItPinned(t *testing.T) {
	directory := t.TempDir()
	if err := WriteContext(built(), directory); err != nil {
		t.Fatalf("unable to write the context: %s", err)
	}

	contents, err := os.ReadFile(filepath.Join(directory, "Dockerfile"))
	if err != nil {
		t.Fatalf("unable to read the written Dockerfile: %s", err)
	}

	if !strings.Contains(string(contents), "FROM ${IMAGE}") {
		t.Fatal("expected the context to take its base from the build argument")
	}

	arguments := strings.Join(BuildArgs(BuildInput{Definition: built(), ImageVersion: "7.4.1"}, directory), " ")
	if !strings.Contains(arguments, "--build-arg IMAGE=redis:7.4.1") {
		t.Errorf("expected the pinned version to be passed, got %s", arguments)
	}
}

// Every definition has to stay in the shape dependabot can read: a concrete image
// on the FROM line and no ARG carrying a second copy of the version. Dependabot
// only ever inspects FROM lines, so a definition written the other way is watched
// by nothing and says so nowhere.
func TestEveryDefinitionPinsItsImageWhereDependabotLooks(t *testing.T) {
	loaded, err := registry.Load(registry.LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	for _, name := range loaded.Names() {
		t.Run(name, func(t *testing.T) {
			found, _ := loaded.Definition(name)

			from := ""
			for _, line := range strings.Split(string(found.Dockerfile), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(strings.ToUpper(line), "ARG IMAGE=") {
					t.Errorf("declares the image in an ARG, which dependabot does not read: %s", line)
				}

				if strings.HasPrefix(strings.ToUpper(line), "FROM ") && from == "" {
					from = strings.TrimSpace(line[len("FROM "):])
				}
			}

			if from == "" {
				t.Fatal("has no FROM instruction")
			}

			if strings.ContainsAny(from, "${}") {
				t.Errorf("takes its base from a variable, which dependabot cannot resolve: FROM %s", from)
			}

			if expected := found.DefaultImage + ":" + found.DefaultImageVersion; from != expected {
				t.Errorf("expected FROM %s, got FROM %s", expected, from)
			}
		})
	}
}
