package definition

import (
	"strings"
	"testing"
)

// validCompose is a definition with nothing wrong with it, which each test then
// breaks in exactly one way.
const validCompose = `
services:
  thing:
    image: "{{ .Image }}:{{ .ImageVersion }}"
    command: [thingd]
    volumes:
      - type: bind
        source: "{{ .HostRoot }}/data"
        target: /data
    ports:
      - name: native
        target: 1234
        primary: true
x-dokku:
  plugin: thing
  title: Thing
  scheme: thing
  alias: THING
  dsn: "{{ .Scheme }}://{{ .Host }}:{{ .Port.native }}"
  wait: native
`

// withConfigEntry gives the service the supplied config entries and a top level
// thing_conf to point at, so that each case breaks one thing and nothing else.
func withConfigEntry(compose string, entries string) string {
	compose = strings.Replace(compose, "    ports:", "    configs:\n"+entries+"    ports:", 1)
	return compose + "\nconfigs:\n  thing_conf:\n    content: |\n      thing = {{ .ServiceName }}\n"
}

func parseCompose(t *testing.T, compose string, embedded bool) (Definition, error) {
	t.Helper()

	return Parse(ParseInput{
		Name:       "thing",
		Compose:    []byte(compose),
		Dockerfile: []byte("ARG IMAGE=thing:1.0\nFROM ${IMAGE}\n"),
		Embedded:   embedded,
	})
}

func TestParseValidDefinition(t *testing.T) {
	parsed, err := parseCompose(t, validCompose, true)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	tests := []struct {
		name     string
		actual   string
		expected string
	}{
		{name: "plugin", actual: parsed.Dokku.Plugin, expected: "thing"},
		{name: "title", actual: parsed.Dokku.Title, expected: "Thing"},
		{name: "derived variable", actual: parsed.Dokku.Variable, expected: "THING"},
		{name: "derived alt alias", actual: parsed.Dokku.AltAlias, expected: "DOKKU_THING"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, test.actual)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name     string
		compose  string
		embedded bool
		expected string
	}{
		{
			name:     "two services",
			compose:  strings.Replace(validCompose, "x-dokku:", "  other:\n    image: other\nx-dokku:", 1),
			expected: "exactly one service",
		},
		{
			name:     "a container name dokku owns",
			compose:  strings.Replace(validCompose, "    command:", "    container_name: mine\n    command:", 1),
			expected: "container_name is set by dokku",
		},
		{
			name:     "a restart policy dokku owns",
			compose:  strings.Replace(validCompose, "    command:", "    restart: no\n    command:", 1),
			expected: "restart is set by dokku",
		},
		{
			name:     "a missing dsn",
			compose:  strings.Replace(validCompose, `  dsn: "{{ .Scheme }}://{{ .Host }}:{{ .Port.native }}"`, "", 1),
			expected: "x-dokku.dsn is required",
		},
		{
			name:     "a nameless port",
			compose:  strings.Replace(validCompose, "      - name: native", "      - name: \"\"", 1),
			expected: "every port needs a name",
		},
		{
			name:     "a named volume",
			compose:  strings.Replace(validCompose, "      - type: bind", "      - type: volume", 1),
			expected: "must be a bind mount",
		},
		{
			name:     "a volume outside the service root",
			compose:  strings.Replace(validCompose, `source: "{{ .HostRoot }}/data"`, `source: "/mnt/elsewhere"`, 1),
			expected: "must be rooted at {{ .HostRoot }}",
		},
		{
			name:     "a wait naming a port that does not exist",
			compose:  strings.Replace(validCompose, "  wait: native", "  wait: nonsense", 1),
			expected: "which is not declared",
		},
		{
			name:     "no readiness signal at all",
			compose:  strings.Replace(validCompose, "  wait: native", "", 1),
			expected: "either a healthcheck or x-dokku.wait",
		},
		{
			name:     "a config with no content",
			compose:  validCompose + "\nconfigs:\n  thing_conf:\n    file: ./thing.conf\n",
			expected: "needs an inline content",
		},
		{
			name:     "a config entry naming nothing",
			compose:  withConfigEntry(validCompose, "      - source: missing\n        target: /data/thing.conf\n"),
			expected: "which is not a top level config",
		},
		{
			name:     "a config entry with no target",
			compose:  withConfigEntry(validCompose, "      - source: thing_conf\n"),
			expected: `config "thing_conf" needs a target`,
		},
		{
			// a file written where nothing mounts it is a file the container
			// never sees, and the container starting anyway is the worse outcome
			name:     "a config outside every mount",
			compose:  withConfigEntry(validCompose, "      - source: thing_conf\n        target: /etc/thing.conf\n"),
			expected: "not inside any bind mount",
		},
		{
			name:     "two configs seeded to the same path",
			compose:  withConfigEntry(validCompose, "      - source: thing_conf\n        target: /data/thing.conf\n      - source: thing_conf\n        target: /data/thing.conf\n"),
			expected: "two configs are seeded to",
		},
		{
			name:     "a mode that is not octal",
			compose:  withConfigEntry(validCompose, "      - source: thing_conf\n        target: /data/thing.conf\n        mode: \"0o644\"\n"),
			expected: "not an octal file mode",
		},
		{
			// the base spec is fixed: a datastore cannot extend what the tool
			// implements by choosing a name the tool has never heard of
			name:     "a command the tool does not implement, under commands",
			compose:  validCompose + "\n  commands:\n    thing-expose:\n      exec: [thing-expose]\n",
			expected: "declare it under custom_commands",
		},
		{
			name:     "a command the tool implements, under custom_commands",
			compose:  validCompose + "\n  custom_commands:\n    connect:\n      description: connect\n      exec: [thing]\n",
			expected: "declare it under commands",
		},
		{
			// the help and the readme have nothing else to describe it with
			name:     "a custom command with no description",
			compose:  validCompose + "\n  custom_commands:\n    thing-expose:\n      exec: [thing-expose]\n",
			expected: `custom command "thing-expose" needs a description`,
		},
		{
			// host mode runs arbitrary code outside a container, so a plugin
			// checkout must not be able to introduce one
			name:     "a host command from a plugin override",
			compose:  validCompose + "\n  custom_commands:\n    thing-expose:\n      description: expose the thing\n      mode: host\n      exec: [thing-expose]\n",
			embedded: false,
			expected: "only allowed for definitions shipped in the binary",
		},
		{
			name:     "a protocol that is neither tcp nor udp",
			compose:  strings.Replace(validCompose, "        target: 1234", "        target: 1234\n        protocol: sctp", 1),
			expected: "neither tcp nor udp",
		},
		{
			name: "readiness on a port that speaks udp",
			// the shape a definition falls into by declaring a udp port primary
			// and naming nothing to wait on
			compose:  strings.Replace(validCompose, "        target: 1234", "        target: 1234\n        protocol: udp", 1),
			expected: "readiness cannot probe it",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseCompose(t, test.compose, test.embedded)
			if err == nil {
				t.Fatal("expected an error")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error mentioning %q, got: %s", test.expected, err)
			}
		})
	}
}

func TestParseAllowsAHostCommandFromTheEmbeddedTree(t *testing.T) {
	compose := validCompose + "\n  custom_commands:\n    thing-expose:\n      description: expose the thing\n      mode: host\n      exec: [thing-expose]\n"
	if _, err := parseCompose(t, compose, true); err != nil {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestImplements(t *testing.T) {
	withCommands := func(keys ...string) Definition {
		commands := map[string]Command{}
		for _, key := range keys {
			commands[key] = Command{Exec: []string{"true"}}
		}

		return Definition{Dokku: Dokku{Commands: commands}}
	}

	tests := []struct {
		name       string
		definition Definition
		subcommand string
		expected   bool
	}{
		{name: "connect present", definition: withCommands("connect"), subcommand: "connect", expected: true},
		{name: "connect absent", definition: withCommands(), subcommand: "connect", expected: false},
		{name: "clone needs both halves", definition: withCommands("export"), subcommand: "clone", expected: false},
		{name: "clone with both halves", definition: withCommands("export", "import"), subcommand: "clone", expected: true},
		{name: "backup follows export", definition: withCommands("export"), subcommand: "backup", expected: true},
		{name: "backup-schedule-cat follows export", definition: withCommands(), subcommand: "backup-schedule-cat", expected: false},
		{name: "anything else is always available", definition: withCommands(), subcommand: "info", expected: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := test.definition.Implements(test.subcommand); actual != test.expected {
				t.Errorf("expected %t, got %t", test.expected, actual)
			}
		})
	}
}

func TestImageFromDockerfile(t *testing.T) {
	tests := []struct {
		name            string
		contents        string
		expectedImage   string
		expectedVersion string
		expectedTrivial bool
	}{
		{
			name:            "the arg and from idiom",
			contents:        "ARG IMAGE=redis:8.8.0\nFROM ${IMAGE}\n",
			expectedImage:   "redis",
			expectedVersion: "8.8.0",
			expectedTrivial: true,
		},
		{
			name:            "a bare from",
			contents:        "FROM postgres:18.4\n",
			expectedImage:   "postgres",
			expectedVersion: "18.4",
			expectedTrivial: true,
		},
		{
			name:            "a namespaced image",
			contents:        "ARG IMAGE=clickhouse/clickhouse-server:26.5.1.882\nFROM ${IMAGE}\n",
			expectedImage:   "clickhouse/clickhouse-server",
			expectedVersion: "26.5.1.882",
			expectedTrivial: true,
		},
		{
			name:            "no tag",
			contents:        "FROM redis\n",
			expectedImage:   "redis",
			expectedVersion: "latest",
			expectedTrivial: true,
		},
		{
			name:            "a copy makes it a real build",
			contents:        "ARG IMAGE=couchdb:3.5.1\nFROM ${IMAGE}\nCOPY rootfs/ /\n",
			expectedImage:   "couchdb",
			expectedVersion: "3.5.1",
			expectedTrivial: false,
		},
		{
			name:            "comments do not make it a build",
			contents:        "# a comment\nARG IMAGE=redis:8.8.0\nFROM ${IMAGE}\n",
			expectedImage:   "redis",
			expectedVersion: "8.8.0",
			expectedTrivial: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			image, version, trivial, err := ImageFromDockerfile([]byte(test.contents))
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if image != test.expectedImage {
				t.Errorf("expected image %q, got %q", test.expectedImage, image)
			}

			if version != test.expectedVersion {
				t.Errorf("expected version %q, got %q", test.expectedVersion, version)
			}

			if trivial != test.expectedTrivial {
				t.Errorf("expected trivial %t, got %t", test.expectedTrivial, trivial)
			}
		})
	}
}

func TestImageFromDockerfileNeedsAFrom(t *testing.T) {
	if _, _, _, err := ImageFromDockerfile([]byte("# nothing here\n")); err == nil {
		t.Error("expected an error for a dockerfile with no FROM")
	}
}

func TestRenderAllDropsEmptyElements(t *testing.T) {
	// an element that renders to nothing is an element the bash plugins guarded
	// with [[ -n "$X" ]], so it must vanish rather than become an empty argument
	rendered, err := RenderAll([]string{"memcached", "-m", "{{ .Memory }}"}, Scope{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if strings.Join(rendered, " ") != "memcached -m" {
		t.Errorf("expected the empty element to be dropped, got %v", rendered)
	}
}

func TestParseReadsConfigs(t *testing.T) {
	compose := withConfigEntry(validCompose, "      - source: thing_conf\n        target: /data/thing.conf\n        mode: \"0640\"\n        uid: \"1001\"\n        gid: \"1001\"\n")

	parsed, err := parseCompose(t, compose, true)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if expected := "thing = {{ .ServiceName }}\n"; parsed.Configs["thing_conf"].Content != expected {
		t.Errorf("expected content %q, got %q", expected, parsed.Configs["thing_conf"].Content)
	}

	if len(parsed.Service.Configs) != 1 {
		t.Fatalf("expected one config entry, got %d", len(parsed.Service.Configs))
	}

	entry := parsed.Service.Configs[0]
	if entry.Source != "thing_conf" || entry.Target != "/data/thing.conf" {
		t.Errorf("expected thing_conf at /data/thing.conf, got %s at %s", entry.Source, entry.Target)
	}

	if entry.Mode != "0640" || entry.UID != "1001" || entry.GID != "1001" {
		t.Errorf("expected 0640 1001:1001, got %s %s:%s", entry.Mode, entry.UID, entry.GID)
	}
}

// A privileged script is granted sudo by the install, so a definition a plugin
// checkout can write must not be able to introduce one. This is the same rule
// host mode has, one step earlier: host mode stops a checkout running code on
// the host, and this stops it choosing what the dokku group runs as root.
func TestParseRefusesAPrivilegedScriptFromACheckout(t *testing.T) {
	input := ParseInput{
		Name:       "thing",
		Compose:    []byte(validCompose),
		Dockerfile: []byte("ARG IMAGE=thing:1.0\nFROM ${IMAGE}\n"),
		Privileged: map[string][]byte{"nginx": []byte("#!/usr/bin/env bash\ntrue\n")},
		Embedded:   false,
	}

	if _, err := Parse(input); err == nil {
		t.Fatal("expected a privileged script from a checkout to be refused")
	} else if !strings.Contains(err.Error(), "only allowed for definitions shipped in the binary") {
		t.Errorf("expected the error to say why, got %q", err)
	}

	// and the same tree is fine from the embedded layer
	input.Embedded = true
	if _, err := Parse(input); err != nil {
		t.Errorf("expected the embedded tree to be allowed, got %s", err)
	}
}

// A trigger runs on the host, so it falls under the same rule host mode
// already has: a definition a plugin checkout can write must not be able to
// introduce one, or overriding a definition would be a way to run code outside
// a container.
func TestParseRefusesAHostTriggerFromACheckout(t *testing.T) {
	compose := strings.Replace(
		validCompose,
		"x-dokku:",
		"x-dokku:\n  triggers:\n    post-extract:\n      mode: host\n      exec: [post-extract]\n",
		1,
	)

	if _, err := parseCompose(t, compose, false); err == nil {
		t.Fatal("expected a host trigger from a checkout to be refused")
	} else if !strings.Contains(err.Error(), "only allowed for definitions shipped in the binary") {
		t.Errorf("expected the error to say why, got %q", err)
	}

	if _, err := parseCompose(t, compose, true); err != nil {
		t.Errorf("expected the embedded tree to be allowed, got %s", err)
	}
}
