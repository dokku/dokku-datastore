package render

import (
	"os"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/registry"
)

// goldenContainerArgs is the file the previous implementation's emitted command
// was pinned to. Reproducing it is the whole point of the declarative renderer:
// it is the difference between believing the migration is faithful and knowing.
const goldenContainerArgs = "testdata/container_args.golden"

// redisScope is the state of a redis service named lollipop, matching the fixture
// the golden file was generated from.
func redisScope() definition.Scope {
	return definition.Scope{
		ServiceName:   "lollipop",
		ContainerName: "dokku.redis.lollipop",
		Host:          "dokku-redis-lollipop",
		Database:      "lollipop",
		Plugin:        "redis",
		Title:         "Redis",
		Variable:      "REDIS",
		Image:         "redis",
		ImageVersion:  "8.8.0",
		TaggedImage:   "redis:8.8.0",
		ServiceRoot:   "/var/lib/dokku/services/redis/lollipop",
		HostRoot:      "/var/lib/dokku/services/redis/lollipop",
		Scheme:        "redis",
		Secret:        map[string]string{"password": "hunter2"},
		Port:          map[string]int{"native": 6379},
	}
}

func redisInput(t *testing.T) Input {
	t.Helper()

	loaded, err := registry.Load(registry.LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	redis, ok := loaded.Definition("redis")
	if !ok {
		t.Fatal("expected an embedded redis definition")
	}

	return Input{
		Definition: redis,
		Scope:      redisScope(),
		EnvFile:    "/var/lib/dokku/services/redis/lollipop/ENV",
		IDFile:     "/var/lib/dokku/services/redis/lollipop/ID",
	}
}

// withoutPayload drops the vendored scripts, whose mounts are a deliberate
// addition to what the previous implementation emitted. The golden file is the
// proof that the renderer reproduces that implementation, so it is compared
// against a definition carrying only what the old one had.
func withoutPayload(input Input) Input {
	input.Definition.Rootfs = nil
	return input
}

// TestRendererReproducesTheGoldenArgs is the acceptance check for replacing the
// hand-written redis implementation: the definition must produce the same command
// the Go code produced, for every case the golden file covers.
func TestRendererReproducesTheGoldenArgs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Input)
	}{
		{
			name:   "defaults",
			mutate: func(input *Input) {},
		},
		{
			name:   "a memory limit",
			mutate: func(input *Input) { input.Scope.Memory = "512" },
		},
		{
			name:   "a shared memory size",
			mutate: func(input *Input) { input.Scope.ShmSize = "128m" },
		},
		{
			name:   "an initial network",
			mutate: func(input *Input) { input.Scope.InitialNetwork = "custom-network" },
		},
		{
			name:   "config options",
			mutate: func(input *Input) { input.ConfigOptions = []string{"--appendonly", "yes"} },
		},
		{
			name:   "config options with an empty element",
			mutate: func(input *Input) { input.ConfigOptions = []string{"--appendonly", "", "yes"} },
		},
		{
			name: "a custom image",
			mutate: func(input *Input) {
				input.Scope.Image = "valkey/valkey"
				input.Scope.ImageVersion = "9.0.0"
			},
		},
		{
			name: "log options",
			mutate: func(input *Input) {
				input.Scope.LogDriver = "json-file"
				input.Scope.LogOptions = map[string]string{"max-size": "20m", "max-file": "3"}
			},
		},
		{
			name: "log options with no driver",
			mutate: func(input *Input) {
				input.Scope.LogOptions = map[string]string{"max-size": "10m"}
			},
		},
		{
			name:   "a restart policy",
			mutate: func(input *Input) { input.Scope.RestartPolicy = "on-failure:3" },
		},
		{
			name: "everything at once",
			mutate: func(input *Input) {
				input.ConfigOptions = []string{"--appendonly", "yes"}
				input.Scope.Image = "valkey/valkey"
				input.Scope.ImageVersion = "9.0.0"
				input.Scope.InitialNetwork = "custom-network"
				input.Scope.LogDriver = "json-file"
				input.Scope.LogOptions = map[string]string{"max-size": "20m", "max-file": "3"}
				input.Scope.Memory = "512"
				input.Scope.RestartPolicy = "unless-stopped"
				input.Scope.ShmSize = "128m"
			},
		},
	}

	golden, err := os.ReadFile(goldenContainerArgs)
	if err != nil {
		t.Fatalf("unable to read the golden file: %s", err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := withoutPayload(redisInput(t))
			test.mutate(&input)

			args, err := ContainerArgs(input)
			if err != nil {
				t.Fatalf("unable to render: %s", err)
			}

			expected := goldenCase(t, string(golden), test.name)
			actual := strings.Join(DockerCreateArgs(args), "\n")
			if actual != expected {
				t.Errorf("the definition does not reproduce the previous command.\nexpected:\n%s\ngot:\n%s", expected, actual)
			}
		})
	}
}

// goldenCase pulls one named block out of the golden file.
func goldenCase(t *testing.T, golden string, name string) string {
	t.Helper()

	for _, block := range strings.Split(golden, "### ") {
		header, body, found := strings.Cut(block, "\n")
		if !found || header != name {
			continue
		}

		return strings.TrimRight(body, "\n")
	}

	t.Fatalf("the golden file has no case named %q", name)
	return ""
}

func TestRenderRejectsAnUnknownScopeField(t *testing.T) {
	if _, err := definition.Render("{{ .NotAField }}", redisScope()); err == nil {
		t.Error("expected a template naming a field the scope does not have to fail")
	}
}

func TestRenderDoesNotEscapeLikeHTML(t *testing.T) {
	scope := redisScope()
	scope.Secret["password"] = `p&ss<w>'d"`

	rendered, err := definition.Render("{{ .Secret.password }}", scope)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	if rendered != `p&ss<w>'d"` {
		t.Errorf("expected the password to survive verbatim, got %q", rendered)
	}
}

// A declared variable that renders to nothing is left out rather than passed
// empty. The declared environment is applied after the custom one, so an empty
// value would clear whatever the operator set under the same name.
func TestRenderLeavesOutAnEnvironmentVariableThatRendersEmpty(t *testing.T) {
	input := redisInput(t)
	input.Definition.Service.Environment = map[string]string{
		"HEAP": "{{ if not .Memory }}small{{ end }}",
	}
	input.Scope.Memory = "512"
	input.Environment = []string{"HEAP=custom"}

	args, err := ContainerArgs(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	if value, ok := args.Env["HEAP"]; ok {
		t.Errorf("expected HEAP to be left out, got %q", value)
	}

	for _, arg := range DockerCreateArgs(args) {
		if strings.HasPrefix(arg, "--env=HEAP=") {
			t.Errorf("expected no --env for HEAP, got %s", arg)
		}
	}

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	if !strings.Contains(string(rendered), "HEAP: custom") {
		t.Errorf("expected the custom value to survive, got:\n%s", string(rendered))
	}
}

// Elasticsearch sizes its heap from the container's memory limit, so a service
// with one is left to do that. One with no limit would take half the host, so
// its heap is held to 512m.
func TestElasticsearchHeapFollowsTheMemoryLimit(t *testing.T) {
	loaded, err := registry.Load(registry.LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	tests := []struct {
		name     string
		memory   string
		expected string
	}{
		{name: "no limit", memory: "", expected: "-Xms512m -Xmx512m"},
		{name: "a limit", memory: "512", expected: ""},
	}

	for _, variant := range []string{"elasticsearch-7", "elasticsearch-8", "elasticsearch-9"} {
		found, ok := loaded.Definition(variant)
		if !ok {
			t.Fatalf("expected a %s definition", variant)
		}

		for _, test := range tests {
			t.Run(variant+" "+test.name, func(t *testing.T) {
				args, err := ContainerArgs(Input{
					Definition: found,
					Scope: definition.Scope{
						ServiceName:   "lollipop",
						ContainerName: "dokku.elasticsearch.lollipop",
						Image:         "elasticsearch",
						ImageVersion:  "7.17.28",
						HostRoot:      "/var/lib/dokku/services/elasticsearch/lollipop",
						ServiceRoot:   "/var/lib/dokku/services/elasticsearch/lollipop",
						Memory:        test.memory,
					},
				})
				if err != nil {
					t.Fatalf("unable to render: %s", err)
				}

				if actual := args.Env["ES_JAVA_OPTS"]; actual != test.expected {
					t.Errorf("expected ES_JAVA_OPTS %q, got %q", test.expected, actual)
				}
			})
		}
	}
}
