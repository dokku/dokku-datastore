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
			// host mode runs arbitrary code outside a container, so a plugin
			// checkout must not be able to introduce one
			name:     "a host command from a plugin override",
			compose:  validCompose + "\n  commands:\n    thing-expose:\n      mode: host\n      exec: [thing-expose]\n",
			embedded: false,
			expected: "only allowed for definitions shipped in the binary",
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
	compose := validCompose + "\n  commands:\n    thing-expose:\n      mode: host\n      exec: [thing-expose]\n"
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
