package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite the golden files instead of comparing against them")

// redisContainerArgs is what internal/datastores/redis.go passes for a service
// named lollipop, with everything the user can vary left at its default.
func redisContainerArgs() ContainerArgsInput {
	return ContainerArgsInput{
		CommandPrefix: "redis",
		Command:       []string{"redis-server", "/usr/local/etc/redis/redis.conf", "--bind", "0.0.0.0"},
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
			name: "no volumes at all",
			mutate: func(input *ContainerArgsInput) {
				input.Volumes = nil
				input.Command = nil
			},
		},
		{
			name: "everything at once",
			mutate: func(input *ContainerArgsInput) {
				input.ConfigOptions = []string{"--appendonly", "yes"}
				input.InitialNetwork = "custom-network"
				input.Memory = "512"
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

	for _, absent := range []string{"--memory", "--shm-size", "--network", "--network-alias"} {
		if strings.Contains(args, absent) {
			t.Errorf("expected %s to be absent when it is unset, got: %s", absent, args)
		}
	}
}
