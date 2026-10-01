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

func parseCompose(t *testing.T, compose string) (Definition, error) {
	t.Helper()

	return Parse(ParseInput{
		Name:       "thing",
		Compose:    []byte(compose),
		Dockerfile: []byte("ARG IMAGE=thing:1.0\nFROM ${IMAGE}\n"),
	})
}

func TestParseValidDefinition(t *testing.T) {
	parsed, err := parseCompose(t, validCompose)
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

// A declared alt_alias is the one used. It is checked with a value the derived
// one could never be, since every embedded definition declares the value it
// would have been given anyway, which is how the key going unread went unseen.
func TestParseReadsADeclaredAltAlias(t *testing.T) {
	parsed, err := parseCompose(t, validCompose+"  alt_alias: OTHER_THING\n")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if parsed.Dokku.AltAlias != "OTHER_THING" {
		t.Errorf("expected %q, got %q", "OTHER_THING", parsed.Dokku.AltAlias)
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name     string
		compose  string
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
			// a service's logging is resolved from what the host and the service
			// were told rather than declared per datastore, so a definition
			// setting it would be overruled on the docker path and obeyed on the
			// compose one
			name:     "logging dokku owns",
			compose:  strings.Replace(validCompose, "    command:", "    logging:\n      driver: none\n    command:", 1),
			expected: "logging is set by dokku",
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
			// made a directory before the config could be written into it,
			// which is how rabbitmq's config went unwritten
			name:     "a config file bound without saying it is a file",
			compose:  withConfigEntry(strings.Replace(validCompose, "        target: /data\n", "        target: /data\n      - type: bind\n        source: \"{{ .HostRoot }}/config/thing.conf\"\n        target: /etc/thing.conf\n", 1), "      - source: thing_conf\n        target: /etc/thing.conf\n"),
			expected: `volume "/etc/thing.conf" is a config file, so it needs x-file: true`,
		},
		{
			// docker would make a directory where it belongs
			name:     "a file nothing makes",
			compose:  strings.Replace(validCompose, "        target: /data\n", "        target: /data\n        x-file: true\n", 1),
			expected: `volume "/data" is a file that neither a config nor a pre_create hook makes`,
		},
		{
			name:     "a volume outside the service root",
			compose:  strings.Replace(validCompose, `source: "{{ .HostRoot }}/data"`, `source: "/mnt/elsewhere"`, 1),
			expected: "must be rooted at {{ .HostRoot }}",
		},
		{
			// a volume is moved by naming it, and the service root has no name
			name:     "a volume of the whole service root",
			compose:  strings.Replace(validCompose, `source: "{{ .HostRoot }}/data"`, `source: "{{ .HostRoot }}"`, 1),
			expected: "has no name to be moved by",
		},
		{
			name:     "a volume whose name cannot be written as a volume target",
			compose:  strings.Replace(validCompose, `source: "{{ .HostRoot }}/data"`, `source: "{{ .HostRoot }}/data=1"`, 1),
			expected: `names the volume "data=1", which cannot be written as a volume-targets key`,
		},
		{
			name:     "two volumes from one source",
			compose:  strings.Replace(validCompose, "        target: /data\n", "        target: /data\n      - type: bind\n        source: \"{{ .HostRoot }}/data\"\n        target: /other\n", 1),
			expected: `two volumes are mounted from "data"`,
		},
		{
			// caught when the definition loads rather than when a service is
			// made, where it would fail the create part way through
			name:     "a command naming the target of a volume that does not exist",
			compose:  strings.Replace(validCompose, "command: [thingd]", `command: [thingd, "--dir={{ .Target.logs }}"]`, 1),
			expected: `names the target of the volume "logs", which is not declared`,
		},
		{
			name:     "an env naming the target of a volume that does not exist, through index",
			compose:  validCompose + "\n  commands:\n    export:\n      exec: [thing-dump]\n      env:\n        DUMP: '{{ index .Target \"data/dump\" }}'\n",
			expected: `names the target of the volume "data/dump", which is not declared`,
		},
		{
			// seeded once and never rewritten, so the path would be the one the
			// volume had when the service was made
			name:     "a config naming a volume target",
			compose:  withConfigEntry(validCompose, "      - source: thing_conf\n        target: /data/thing.conf\n") + "      dir = {{ .Target.data }}\n",
			expected: `config "thing_conf" names a volume target`,
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
			// nothing but export and import passes extra arguments on, so it
			// would be a setting that does nothing
			name:     "extra arguments on a command that cannot take them",
			compose:  validCompose + "\n  commands:\n    connect:\n      extra_args: true\n      exec: [thing]\n",
			expected: `command "connect" cannot take extra arguments`,
		},
		{
			// exec'd into the running service, so there is no container
			// started for it whose entrypoint could be replaced
			name:     "an entrypoint on a command run in the service",
			compose:  validCompose + "\n  commands:\n    connect:\n      entrypoint: \"\"\n      exec: [thing]\n",
			expected: `command "connect" cannot replace the entrypoint`,
		},
		{
			name:     "an entrypoint on a command run on the host",
			compose:  validCompose + "\n  custom_commands:\n    thing-expose:\n      description: expose thing\n      mode: host\n      entrypoint: \"\"\n      exec: [thing-expose]\n",
			expected: `command "thing-expose" cannot replace the entrypoint`,
		},
		{
			name:     "a protocol that is neither tcp nor udp",
			compose:  strings.Replace(validCompose, "        target: 1234", "        target: 1234\n        protocol: sctp", 1),
			expected: "neither tcp nor udp",
		},
		{
			name:     "an empty reserved name",
			compose:  validCompose + `  reserved_names: [thing, ""]` + "\n",
			expected: "x-dokku.reserved_names has an empty entry",
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
			_, err := parseCompose(t, test.compose)
			if err == nil {
				t.Fatal("expected an error")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error mentioning %q, got: %s", test.expected, err)
			}
		})
	}
}

func TestParseReadsReservedNames(t *testing.T) {
	parsed, err := parseCompose(t, validCompose+"  reserved_names: [thing, thing_schema]\n")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []string{"thing", "thing_schema"}
	if strings.Join(parsed.Dokku.ReservedNames, ",") != strings.Join(expected, ",") {
		t.Errorf("expected %v, got %v", expected, parsed.Dokku.ReservedNames)
	}
}

// A command started in a container of its own may clear the image's
// entrypoint, which is kept as declared rather than defaulted.
func TestParseKeepsTheEntrypointOfAContainerCommand(t *testing.T) {
	for _, mode := range []string{ModeSidecar, ModeOffline} {
		t.Run(mode, func(t *testing.T) {
			compose := validCompose + "\n  commands:\n    export:\n      mode: " + mode + "\n      entrypoint: \"\"\n      exec: [dump]\n"
			parsed, err := parseCompose(t, compose)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			entrypoint := parsed.Dokku.Commands["export"].Entrypoint
			if entrypoint == nil || *entrypoint != "" {
				t.Errorf("expected an empty entrypoint, got %v", entrypoint)
			}
		})
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
		{name: "backup-logs follows export", definition: withCommands(), subcommand: "backup-logs", expected: false},
		{name: "backup-logs with export", definition: withCommands("export"), subcommand: "backup-logs", expected: true},
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
		{
			name:            "a private registry carries a port",
			contents:        "FROM registry.example.com:5000/redis:8.8.0\n",
			expectedImage:   "registry.example.com:5000/redis",
			expectedVersion: "8.8.0",
			expectedTrivial: true,
		},
		{
			name:            "a private registry with no tag",
			contents:        "FROM registry.example.com:5000/redis\n",
			expectedImage:   "registry.example.com:5000/redis",
			expectedVersion: "latest",
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

// Where a reference is cut in two. The last colon rather than the first,
// because a private registry carries its port in the reference and splitting on
// the first recorded the host name as the image and the rest as the version.
func TestCutImage(t *testing.T) {
	tests := []struct {
		name          string
		reference     string
		expectedName  string
		expectedTag   string
		expectedFound bool
	}{
		{
			name:          "an image and a tag",
			reference:     "redis:8.8.0",
			expectedName:  "redis",
			expectedTag:   "8.8.0",
			expectedFound: true,
		},
		{
			name:         "an image with no tag",
			reference:    "redis",
			expectedName: "redis",
		},
		{
			name:          "a namespaced image",
			reference:     "redis/redis-stack-server:7.2.0-v10",
			expectedName:  "redis/redis-stack-server",
			expectedTag:   "7.2.0-v10",
			expectedFound: true,
		},
		{
			name:          "a private registry and a tag",
			reference:     "registry.example.com:5000/redis:8.8.0",
			expectedName:  "registry.example.com:5000/redis",
			expectedTag:   "8.8.0",
			expectedFound: true,
		},
		{
			// the only colon is the port, and a port is not a tag
			name:         "a private registry with no tag",
			reference:    "registry.example.com:5000/redis",
			expectedName: "registry.example.com:5000/redis",
		},
		{
			// the colon in sha256: belongs to the digest
			name:         "a digest",
			reference:    "redis@sha256:0000000000000000000000000000000000000000000000000000000000000000",
			expectedName: "redis@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		},
		{
			name:         "nothing at all",
			reference:    "",
			expectedName: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, tag, found := CutImage(test.reference)
			if name != test.expectedName {
				t.Errorf("expected name %q, got %q", test.expectedName, name)
			}

			if tag != test.expectedTag {
				t.Errorf("expected tag %q, got %q", test.expectedTag, tag)
			}

			if found != test.expectedFound {
				t.Errorf("expected found %t, got %t", test.expectedFound, found)
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

	parsed, err := parseCompose(t, compose)
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

// A plugin may ship a definition that installs a privileged script.
//
// Installing a dokku plugin is a root action, and afterwards the whole plugin
// tree is owned by the dokku user while its install script is run by root. A
// definition in that tree is therefore trusted exactly as far as the scripts
// beside it already were, and refusing it here protected nothing: the same code
// could go straight into the plugin's own install file.
func TestParseAllowsAPrivilegedScriptFromACheckout(t *testing.T) {
	input := ParseInput{
		Name:       "thing",
		Compose:    []byte(validCompose),
		Dockerfile: []byte("ARG IMAGE=thing:1.0\nFROM ${IMAGE}\n"),
		Privileged: map[string][]byte{"nginx": []byte("#!/usr/bin/env bash\ntrue\n")},
	}

	if _, err := Parse(input); err != nil {
		t.Errorf("unexpected error: %s", err)
	}
}

// And a trigger that runs on the host, for the same reason. It runs as the dokku
// user, who already owns every script in the plugin that runs as the dokku user.
func TestParseAllowsAHostTriggerFromACheckout(t *testing.T) {
	compose := strings.Replace(
		validCompose,
		"x-dokku:",
		"x-dokku:\n  triggers:\n    post-extract:\n      mode: host\n      exec: [post-extract]\n",
		1,
	)

	if _, err := parseCompose(t, compose); err != nil {
		t.Errorf("unexpected error: %s", err)
	}
}

// A custom command may name the readme section it is documented under. A section
// nothing matches would leave it documented nowhere, with the readme rendering as
// though it had never been declared, so it is refused at parse.
func TestParseChecksACustomCommandsSection(t *testing.T) {
	withGroup := func(group string) string {
		return validCompose + "\n  custom_commands:\n    thing-expose:\n      description: expose the thing\n" +
			"      group: " + group + "\n      exec: [thing-expose]\n"
	}

	for group := range Groups {
		t.Run(group, func(t *testing.T) {
			parsed, err := parseCompose(t, withGroup(group))
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if actual := parsed.Dokku.CustomCommands["thing-expose"].Group; actual != group {
				t.Errorf("expected %q, got %q", group, actual)
			}
		})
	}

	// the heading rather than the identifier is the mistake worth catching, since
	// it is what somebody reading the readme would copy
	// a trailing space is not among them: yaml strips it from a plain scalar
	// before the validator ever sees it
	for _, group := range []string{"Backups", "Data Management", "nonsense"} {
		t.Run("refuses "+group, func(t *testing.T) {
			_, err := parseCompose(t, withGroup(group))
			if err == nil {
				t.Fatal("expected an unknown section to be refused")
			}

			if !strings.Contains(err.Error(), "unknown section") {
				t.Errorf("expected the error to say why, got %q", err)
			}
		})
	}
}

// A command that names no section is not refused: it is collected into the custom
// command section at the end of the readme.
func TestParseAllowsACustomCommandWithNoSection(t *testing.T) {
	compose := validCompose + "\n  custom_commands:\n    thing-expose:\n      description: expose the thing\n      exec: [thing-expose]\n"
	parsed, err := parseCompose(t, compose)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if group := parsed.Dokku.CustomCommands["thing-expose"].Group; group != "" {
		t.Errorf("expected no section, got %q", group)
	}
}

// A definition may add readme sections of its own, for what a datastore does
// that none of its commands explain.
func TestParseReadsDocumentationSections(t *testing.T) {
	compose := validCompose + "\n  documentation:\n    - title: Using the thing\n      body: |\n        the thing does things.\n        dokku thing:info lollipop\n"
	parsed, err := parseCompose(t, compose)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if len(parsed.Dokku.Documentation) != 1 {
		t.Fatalf("expected one section, got %d", len(parsed.Dokku.Documentation))
	}

	section := parsed.Dokku.Documentation[0]
	if section.Title != "Using the thing" {
		t.Errorf("expected the title to be read, got %q", section.Title)
	}

	if section.Body != "the thing does things.\ndokku thing:info lollipop\n" {
		t.Errorf("expected the body to be read, got %q", section.Body)
	}
}

// A section with nothing to head it or nothing in it would render as a stray
// heading or as prose under the section before it, and two sections with one
// title could not be told apart when a newer definition replaces one.
func TestParseRefusesABrokenDocumentationSection(t *testing.T) {
	tests := []struct {
		name     string
		sections string
		expected string
	}{
		{
			name:     "no title",
			sections: "    - body: the thing does things.\n",
			expected: "needs a title",
		},
		{
			name:     "no body",
			sections: "    - title: Using the thing\n",
			expected: "needs a body",
		},
		{
			name:     "a title used twice",
			sections: "    - title: Using the thing\n      body: one.\n    - title: Using the thing\n      body: two.\n",
			expected: "declared twice",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseCompose(t, validCompose+"\n  documentation:\n"+test.sections)
			if err == nil {
				t.Fatal("expected the section to be refused")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected the error to contain %q, got %q", test.expected, err)
			}
		})
	}
}

// The directory is optional: a definition that names none stores its services
// under its plugin name, which is every definition but graphite's.
func TestServicesDirectoryDefaultsToThePlugin(t *testing.T) {
	parsed, err := parseCompose(t, validCompose)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if parsed.Dokku.DataDirectory != "" {
		t.Errorf("expected no directory to be declared, got %q", parsed.Dokku.DataDirectory)
	}

	if actual := parsed.ServicesDirectory(); actual != parsed.Dokku.Plugin {
		t.Errorf("expected %s, got %s", parsed.Dokku.Plugin, actual)
	}
}

// And a definition that names one is stored there instead.
func TestServicesDirectoryIsDeclarable(t *testing.T) {
	compose := strings.Replace(validCompose, "x-dokku:", "x-dokku:\n  data_directory: somewhere-else", 1)
	parsed, err := parseCompose(t, compose)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if actual := parsed.ServicesDirectory(); actual != "somewhere-else" {
		t.Errorf("expected somewhere-else, got %s", actual)
	}
}

// A volume is named by where it lives under the service root, and a template
// may name it either way a template can.
func TestParseAcceptsTargetReferences(t *testing.T) {
	compose := strings.Replace(validCompose, "command: [thingd]", `command: [thingd, "--dir={{ .Target.data }}", "--also={{ index .Target \"data\" }}"]`, 1)
	parsed, err := parseCompose(t, compose)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if keys := parsed.VolumeKeys(); strings.Join(keys, ",") != "data" {
		t.Errorf("expected the one volume to be named data, got %v", keys)
	}

	rendered, err := RenderAll(parsed.Service.Command, parsed.WithTargets(Scope{}))
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	if strings.Join(rendered, " ") != "thingd --dir=/data --also=/data" {
		t.Errorf("expected the definition's own target, got %v", rendered)
	}

	rendered, err = RenderAll(parsed.Service.Command, parsed.WithTargets(Scope{Target: map[string]string{"data": "/srv/thing"}}))
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	if strings.Join(rendered, " ") != "thingd --dir=/srv/thing --also=/srv/thing" {
		t.Errorf("expected the moved target, got %v", rendered)
	}
}

func TestTargetReferences(t *testing.T) {
	tests := []struct {
		body     string
		expected []string
	}{
		{body: "thingd", expected: []string{}},
		{body: "{{ .Target.data }}/dump.rdb", expected: []string{"data"}},
		{body: `{{ index .Target "data/grafana" }} {{ .Target.config }}`, expected: []string{"data/grafana", "config"}},
		{body: "{{ .HostRoot }}/data", expected: []string{}},
	}

	for _, test := range tests {
		if actual := TargetReferences(test.body); strings.Join(actual, ",") != strings.Join(test.expected, ",") {
			t.Errorf("expected %q to name %v, got %v", test.body, test.expected, actual)
		}
	}
}

// Overrides are laid over the definition's own targets, and one for a volume
// the definition does not have is left out rather than made up
func TestVolumeTargets(t *testing.T) {
	parsed, err := parseCompose(t, strings.Replace(validCompose, "        target: /data\n", "        target: /data\n      - type: bind\n        source: \"{{ .HostRoot }}/config\"\n        target: /etc/thing\n", 1))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if keys := parsed.VolumeKeys(); strings.Join(keys, ",") != "data,config" {
		t.Errorf("expected the volumes in the order they are declared, got %v", keys)
	}

	targets := parsed.VolumeTargets(map[string]string{"data": "/srv/thing", "logs": "/logs"})
	if targets["data"] != "/srv/thing" || targets["config"] != "/etc/thing" || len(targets) != 2 {
		t.Errorf("expected data moved and config where it was, got %v", targets)
	}

	scope := parsed.WithTargets(Scope{Target: map[string]string{"config": "/opt/thing"}})
	if scope.Target["data"] != "/data" || scope.Target["config"] != "/opt/thing" {
		t.Errorf("expected the scope's own target kept and the rest filled in, got %v", scope.Target)
	}

	if target := scope.TargetOf(parsed.Service.Volumes[1]); target != "/opt/thing" {
		t.Errorf("expected the moved target, got %q", target)
	}

	if target := (Scope{}).TargetOf(parsed.Service.Volumes[1]); target != "/etc/thing" {
		t.Errorf("expected the definition's own target for a scope without one, got %q", target)
	}
}
