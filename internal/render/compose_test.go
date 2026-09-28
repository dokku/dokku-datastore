package render

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
	"gopkg.in/yaml.v3"
)

func TestComposeForRedis(t *testing.T) {
	rendered, err := Compose(redisInput(t))
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	document := string(rendered)

	for _, expected := range []string{
		"container_name: dokku.redis.lollipop",
		"hostname: dokku.redis.lollipop",
		"image: redis:8.8.0",
		"restart: always",
		"dokku: service",
		"dokku.service: redis",
		// compose would otherwise invent a project network, and a service that
		// was on the default bridge would quietly move
		"network_mode: bridge",
	} {
		if !strings.Contains(document, expected) {
			t.Errorf("expected the compose file to contain %q, got:\n%s", expected, document)
		}
	}

	// a container exposes what its image declares and nothing more, which is
	// what the docker path produces. Declaring ports here would exhibit ports
	// the image never had, which mongo showed: it names four and its image
	// declares one.
	for _, absent := range []string{"ports:", "expose:"} {
		if strings.Contains(document, absent) {
			t.Errorf("expected no %s in the rendered file, got:\n%s", absent, document)
		}
	}
}

// The compose file and the docker argv come from one resolution, so a
// difference between them is a bug in the renderer rather than in a backend.
func TestComposeAgreesWithTheArgv(t *testing.T) {
	input := redisInput(t)

	arguments, err := ContainerArgs(input)
	if err != nil {
		t.Fatalf("unable to resolve: %s", err)
	}

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	document := string(rendered)

	if !strings.Contains(document, "image: "+arguments.TaggedImage) {
		t.Errorf("the image differs: argv has %q", arguments.TaggedImage)
	}

	for _, volume := range arguments.Volumes {
		if !strings.Contains(document, "- "+volume) {
			t.Errorf("the compose file is missing the volume %q", volume)
		}
	}

	for _, argument := range arguments.Command {
		if !strings.Contains(document, "- "+argument) {
			t.Errorf("the compose file is missing the command element %q", argument)
		}
	}
}

func TestComposeJoinsANamedNetwork(t *testing.T) {
	input := redisInput(t)
	input.Scope.InitialNetwork = "custom-network"

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	document := string(rendered)

	// external, because dokku's networks are made by dokku: compose must attach
	// to one rather than create it
	for _, expected := range []string{"custom-network:", "external: true", "- dokku-redis-lollipop"} {
		if !strings.Contains(document, expected) {
			t.Errorf("expected %q, got:\n%s", expected, document)
		}
	}

	if strings.Contains(document, "network_mode: bridge") {
		t.Errorf("expected no bridge pin when a network was asked for, got:\n%s", document)
	}
}

func TestComposeEscapesADollarInTheEnvironment(t *testing.T) {
	input := redisInput(t)
	input.Environment = []string{"PRICE=$5", "GREETING=hello"}

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	// compose expands ${...} in a value and docker's env file does not, so a
	// dollar left alone would reach the container as something else
	if !strings.Contains(string(rendered), "PRICE: $$5") {
		t.Errorf("expected the dollar to be escaped, got:\n%s", string(rendered))
	}

	if !strings.Contains(string(rendered), "GREETING: hello") {
		t.Errorf("expected the plain value untouched, got:\n%s", string(rendered))
	}
}

func TestComposeOmitsALimitOfZero(t *testing.T) {
	// the memory file holds "0" for a service that was never given a limit
	input := redisInput(t)
	input.Scope.Memory = "0"

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	if strings.Contains(string(rendered), "memory:") {
		t.Errorf("expected no memory limit, got:\n%s", string(rendered))
	}

	input.Scope.Memory = "512"
	rendered, err = Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	if !strings.Contains(string(rendered), "memory: 512m") {
		t.Errorf("expected a memory limit, got:\n%s", string(rendered))
	}
}

func TestComposeCarriesTheLogConfig(t *testing.T) {
	input := redisInput(t)
	input.Scope.LogDriver = "json-file"
	input.Scope.LogOptions = map[string]string{"max-size": "20m", "max-file": "3"}

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	document := string(rendered)

	for _, expected := range []string{"logging:", "driver: json-file", `max-file: "3"`, "max-size: 20m"} {
		if !strings.Contains(document, expected) {
			t.Errorf("expected the compose file to contain %q, got:\n%s", expected, document)
		}
	}
}

// A service that says nothing about its logging must emit no logging key at
// all, or compose would pin a driver the docker path leaves to the daemon and
// the two backends would produce different containers.
func TestComposeOmitsAnEmptyLogConfig(t *testing.T) {
	rendered, err := Compose(redisInput(t))
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	if document := string(rendered); strings.Contains(document, "logging:") {
		t.Errorf("expected no logging key when nothing is set, got:\n%s", document)
	}
}

// The cap reaches the container whichever backend made it, and compose must not
// name a driver the docker path did not.
func TestComposeCarriesLogOptionsWithoutADriver(t *testing.T) {
	input := redisInput(t)
	input.Scope.LogOptions = map[string]string{"max-size": "10m"}

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	document := string(rendered)

	if !strings.Contains(document, "max-size: 10m") {
		t.Errorf("expected the cap to be carried, got:\n%s", document)
	}

	if strings.Contains(document, "driver:") {
		t.Errorf("expected no driver when the service names none, got:\n%s", document)
	}
}

// Compose expands ${...} in a value and docker's own --log-opt does not, so a
// tag template carrying a dollar sign would otherwise mean two different things
// depending on which backend made the container.
func TestComposeDoublesADollarInALogOption(t *testing.T) {
	input := redisInput(t)
	input.Scope.LogOptions = map[string]string{"tag": "$SERVICE"}

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	if document := string(rendered); !strings.Contains(document, "tag: $$SERVICE") {
		t.Errorf("expected the dollar sign to be doubled, got:\n%s", document)
	}
}

// The same policy the docker path is handed, retry count and all, which compose
// takes in docker's own syntax.
func TestComposeCarriesTheRestartPolicy(t *testing.T) {
	input := redisInput(t)
	input.Scope.RestartPolicy = "on-failure:3"

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	if document := string(rendered); !strings.Contains(document, "restart: on-failure:3") {
		t.Errorf("expected the restart policy to be carried, got:\n%s", document)
	}
}

// The compose backend is handed the same mounts the docker path is, after the
// definition's own volumes.
func TestComposeCarriesTheMounts(t *testing.T) {
	input := redisInput(t)
	input.Scope.Mounts = []string{"/srv/extra:/data/extra:ro,z", "some-volume:/opt/extra"}

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	document := string(rendered)
	for _, mount := range input.Scope.Mounts {
		if !strings.Contains(document, "- "+mount) {
			t.Errorf("expected the mount %s to be carried, got:\n%s", mount, document)
		}
	}

	definitionVolume := strings.Index(document, "/lollipop/data:/data\n")
	if definitionVolume == -1 {
		t.Fatalf("expected the definition's data volume, got:\n%s", document)
	}
	if strings.Index(document, "/srv/extra:/data/extra") < definitionVolume {
		t.Errorf("expected the mounts to follow the definition's volumes, got:\n%s", document)
	}
}

// Compose refuses a service that mounts a docker volume its file does not
// declare, and one declared without external would be made under the project's
// name rather than the one the operator gave.
func TestComposeDeclaresTheDockerVolumes(t *testing.T) {
	input := redisInput(t)
	input.Scope.Mounts = []string{"/srv/extra:/data/extra:ro", "some-volume:/opt/extra"}

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	file := map[string]any{}
	if err := yaml.Unmarshal(rendered, &file); err != nil {
		t.Fatalf("unable to parse the rendered file: %s", err)
	}

	expected := map[string]any{"some-volume": map[string]any{"external": true, "name": "some-volume"}}
	if !reflect.DeepEqual(file["volumes"], expected) {
		t.Errorf("expected %v, got %v", expected, file["volumes"])
	}

	// and none at all when nothing mounts one
	rendered, err = Compose(redisInput(t))
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}
	if strings.Contains(string(rendered), "\nvolumes:") {
		t.Errorf("expected no top level volumes, got:\n%s", rendered)
	}
}

// A docker volume mounted from a subpath takes compose's long syntax, which is
// the only one that can say so, after the short entries.
func TestComposeCarriesAVolumeSubpath(t *testing.T) {
	input := redisInput(t)
	input.Scope.VolumeMounts = []definition.VolumeMount{
		{Source: "some-volume", Target: "/opt/sub", Subpath: "one/two", Readonly: true, NoCopy: true},
	}

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	file := struct {
		Services map[string]struct {
			Volumes []any `yaml:"volumes"`
		} `yaml:"services"`
		Volumes map[string]any `yaml:"volumes"`
	}{}
	if err := yaml.Unmarshal(rendered, &file); err != nil {
		t.Fatalf("unable to parse the rendered file: %s", err)
	}

	volumes := file.Services["redis"].Volumes
	last := volumes[len(volumes)-1]
	expected := map[string]any{
		"type":      "volume",
		"source":    "some-volume",
		"target":    "/opt/sub",
		"read_only": true,
		"volume":    map[string]any{"nocopy": true, "subpath": "one/two"},
	}
	if !reflect.DeepEqual(last, expected) {
		t.Errorf("expected %v, got %v", expected, last)
	}

	if _, ok := file.Volumes["some-volume"]; !ok {
		t.Errorf("expected some-volume to be declared, got %v", file.Volumes)
	}
}

// A bare no is a boolean to a YAML 1.1 reader, and compose would be handed
// false rather than a policy, so it has to reach the file as a string.
func TestComposeQuotesARestartPolicyOfNo(t *testing.T) {
	input := redisInput(t)
	input.Scope.RestartPolicy = "no"

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	document := string(rendered)
	if !strings.Contains(document, `restart: "no"`) {
		t.Errorf("expected the policy to be quoted, got:\n%s", document)
	}

	parsed := struct {
		Services map[string]map[string]any `yaml:"services"`
	}{}
	if err := yaml.Unmarshal(rendered, &parsed); err != nil {
		t.Fatalf("unable to parse the rendered file: %s", err)
	}

	for name, service := range parsed.Services {
		if policy, ok := service["restart"].(string); !ok || policy != "no" {
			t.Errorf("expected %s to restart %q, got %#v", name, "no", service["restart"])
		}
	}
}

// The compose backend is rendered from the same resolution as the docker one,
// so it mounts a moved volume at the same path.
func TestComposeMountsAMovedVolume(t *testing.T) {
	input := redisInput(t)
	input.Scope.Target = map[string]string{"data": "/redis-data"}

	rendered, err := Compose(input)
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	document := string(rendered)
	if !strings.Contains(document, "- /var/lib/dokku/services/redis/lollipop/data:/redis-data\n") {
		t.Errorf("expected the data volume at the moved path, got:\n%s", document)
	}

	if strings.Contains(document, "lollipop/data:/data\n") {
		t.Errorf("expected nothing at the path the volume left, got:\n%s", document)
	}

	// and started in it, the way the docker backend starts it
	if !strings.Contains(document, "working_dir: /redis-data\n") {
		t.Errorf("expected the container started in the moved data volume, got:\n%s", document)
	}
}
