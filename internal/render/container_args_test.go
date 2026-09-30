package render

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite the golden files instead of comparing against them")

// redisContainerArgs is what the previous hand written redis service passed
// named lollipop, with everything the user can vary left at its default. The
// working directory naming the data volume is the one thing added since, so that
// a service that moves the volume writes its dump into it.
func redisContainerArgs() ContainerArgsInput {
	return ContainerArgsInput{
		CommandPrefix: "redis",
		Command:       []string{"redis-server", "/usr/local/etc/redis/redis.conf", "--bind", "0.0.0.0"},
		WorkingDir:    "/data",
		ContainerName: "dokku.redis.lollipop",
		EnvFile:       "/var/lib/dokku/services/redis/lollipop/ENV",
		IDFile:        "/var/lib/dokku/services/redis/lollipop/ID",
		NetworkAlias:  "dokku-redis-lollipop",
		TaggedImage:   "redis:8.8.0",
		Volumes: []string{
			"/var/lib/dokku/services/redis/lollipop/config:/usr/local/etc/redis",
			"/var/lib/dokku/services/redis/lollipop/data:/data",
		},
	}
}

func TestContainerArgs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ContainerArgsInput)
	}{
		{
			name:   "defaults",
			mutate: func(input *ContainerArgsInput) {},
		},
		{
			name:   "a memory limit",
			mutate: func(input *ContainerArgsInput) { input.Memory = "512" },
		},
		{
			name:   "a shared memory size",
			mutate: func(input *ContainerArgsInput) { input.ShmSize = "128m" },
		},
		{
			name:   "an initial network",
			mutate: func(input *ContainerArgsInput) { input.InitialNetwork = "custom-network" },
		},
		{
			name:   "config options",
			mutate: func(input *ContainerArgsInput) { input.ConfigOptions = []string{"--appendonly", "yes"} },
		},
		{
			// the empty element is what an empty CONFIG_OPTIONS file produces once
			// it has been split, and it must not reach the command line
			name:   "config options with an empty element",
			mutate: func(input *ContainerArgsInput) { input.ConfigOptions = []string{"--appendonly", "", "yes"} },
		},
		{
			name:   "a custom image",
			mutate: func(input *ContainerArgsInput) { input.TaggedImage = "valkey/valkey:9.0.0" },
		},
		{
			// sorted rather than in the order a map hands them over, or the
			// command would differ between two runs of the same create
			name: "log options",
			mutate: func(input *ContainerArgsInput) {
				input.LogDriver = "json-file"
				input.LogOptions = map[string]string{"max-size": "20m", "max-file": "3"}
			},
		},
		{
			// the driver is the daemon's, which is what a service that names none
			// is logged by, and the cap still has to reach the container
			name: "log options with no driver",
			mutate: func(input *ContainerArgsInput) {
				input.LogOptions = map[string]string{"max-size": "10m"}
			},
		},
		{
			// the retry count rides along in docker's own syntax rather than as a
			// flag of its own
			name:   "a restart policy",
			mutate: func(input *ContainerArgsInput) { input.RestartPolicy = "on-failure:3" },
		},
		{
			// what the operator mounted follows the definition's own volumes,
			// options and all
			name: "mounts",
			mutate: func(input *ContainerArgsInput) {
				input.Volumes = append(input.Volumes, "/srv/extra:/data/extra:ro,z", "some-volume:/opt/extra")
			},
		},
		{
			// a docker volume mounted from a subpath is one -v cannot say, and
			// follows the -v arguments as a --mount
			name: "a volume mounted from a subpath",
			mutate: func(input *ContainerArgsInput) {
				input.Volumes = append(input.Volumes, "some-volume:/opt/extra")
				input.VolumeMounts = []definition.VolumeMount{
					{Source: "some-volume", Target: "/opt/sub", Subpath: "one/two"},
					{Source: "other-volume", Target: "/opt/other", Subpath: "three", Readonly: true, NoCopy: true},
				}
			},
		},
		{
			name: "no volumes at all",
			mutate: func(input *ContainerArgsInput) {
				input.Volumes = nil
				input.Command = nil
			},
		},
		{
			// a service exposed directly publishes its ports itself, and says
			// which on a label so the ports can be compared without docker's own
			// bindings being parsed back into specs
			name: "published ports",
			mutate: func(input *ContainerArgsInput) {
				input.Publish = []string{"127.0.0.1:1234:6379", "[::1]:1235:8125/udp"}
			},
		},
		{
			name: "everything at once",
			mutate: func(input *ContainerArgsInput) {
				input.ConfigOptions = []string{"--appendonly", "yes"}
				input.InitialNetwork = "custom-network"
				input.LogDriver = "json-file"
				input.LogOptions = map[string]string{"max-size": "20m", "max-file": "3"}
				input.Memory = "512"
				input.RestartPolicy = "unless-stopped"
				input.ShmSize = "128m"
				input.TaggedImage = "valkey/valkey:9.0.0"
			},
		},
	}

	rendered := strings.Builder{}
	for _, test := range tests {
		input := redisContainerArgs()
		test.mutate(&input)

		rendered.WriteString("### " + test.name + "\n")
		for _, arg := range DockerCreateArgs(input) {
			rendered.WriteString(arg + "\n")
		}
		rendered.WriteString("\n")
	}

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenContainerArgs), 0755); err != nil {
			t.Fatalf("unable to create the golden directory: %s", err)
		}

		if err := os.WriteFile(goldenContainerArgs, []byte(rendered.String()), 0644); err != nil {
			t.Fatalf("unable to write the golden file: %s", err)
		}

		t.Logf("wrote %s", goldenContainerArgs)
		return
	}

	expected, err := os.ReadFile(goldenContainerArgs)
	if err != nil {
		t.Fatalf("unable to read the golden file, run go test -run TestContainerArgs -update-golden: %s", err)
	}

	if rendered.String() != string(expected) {
		t.Errorf("the emitted command changed.\nexpected:\n%s\ngot:\n%s", expected, rendered.String())
	}
}

func TestContainerArgsOmitsUnsetValues(t *testing.T) {
	args := strings.Join(DockerCreateArgs(redisContainerArgs()), " ")

	for _, absent := range []string{"--memory", "--shm-size", "--network", "--network-alias", "--log-driver", "--log-opt", "--publish", PublishedPortsLabel} {
		if strings.Contains(args, absent) {
			t.Errorf("expected %s to be absent when it is unset, got: %s", absent, args)
		}
	}
}

// Only docker volumes are named, and each once, since a host path is not
// something compose has to be told about.
func TestNamedVolumes(t *testing.T) {
	input := redisContainerArgs()
	input.Volumes = append(input.Volumes, "some-volume:/opt/extra:ro", "/srv/extra:/opt/srv")
	input.VolumeMounts = []definition.VolumeMount{
		{Source: "some-volume", Target: "/opt/sub", Subpath: "one"},
		{Source: "other-volume", Target: "/opt/other", Subpath: "two"},
	}

	expected := []string{"some-volume", "other-volume"}
	if actual := NamedVolumes(input); !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %v, got %v", expected, actual)
	}

	if actual := NamedVolumes(redisContainerArgs()); len(actual) != 0 {
		t.Errorf("expected no named volumes, got %v", actual)
	}
}
