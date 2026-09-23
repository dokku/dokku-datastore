package render

import (
	"sort"
)

// ContainerArgsInput is the input for ContainerArgs. Every value a container's
// argv depends on appears here, so that the emitted command is a function of its
// input rather than of the filesystem.
type ContainerArgsInput struct {
	// CommandPrefix is the datastore type, which labels the container
	CommandPrefix string

	// Command is the argv after the image, before the user's config options
	Command []string

	// ConfigOptions are the user's --config-options, already split the way a
	// shell would split them
	ConfigOptions []string

	// ContainerName is both the container name and its hostname
	ContainerName string

	// Env is the environment the definition declares, which is how a datastore
	// is told its own database name and credentials. It is passed alongside the
	// env file rather than merged into it: docker lets a named value win over a
	// file, so what the definition needs cannot be unset by a custom env.
	Env map[string]string

	// EnvFile holds the custom environment the service was created with
	EnvFile string

	// IDFile is where docker writes the container id
	IDFile string

	// InitialNetwork is the network to attach at create time. When it is empty no
	// network is requested at all and the container lands on the default bridge
	// with no alias, which is what tests/link_networks.bats asserts.
	InitialNetwork string

	// LogDriver is the docker logging driver, empty for the daemon's default
	LogDriver string

	// LogOptions are the docker log options, already resolved
	LogOptions map[string]string

	// Memory is a container memory limit in megabytes, without the unit
	Memory string

	// NetworkAlias is the dns name the service answers to on its networks
	NetworkAlias string

	// ShmSize is a shared memory size, with its unit
	ShmSize string

	// TaggedImage is the fully resolved image reference
	TaggedImage string

	// Volumes are host:container bind mounts, in the order they are passed
	Volumes []string
}

// DockerCreateArgs builds the argv for `docker container create`. It is pure, so the
// exact command a service is created with can be pinned by a test that needs
// neither a filesystem nor a docker daemon.
func DockerCreateArgs(input ContainerArgsInput) []string {
	args := []string{
		"container",
		"create",
		"--cidfile=" + input.IDFile,
		"--env-file=" + input.EnvFile,
		"--hostname=" + input.ContainerName,
		"--label=dokku.service=" + input.CommandPrefix,
		"--label=dokku=service",
		"--name=" + input.ContainerName,
		"--restart=always",
	}

	// sorted, because a map would otherwise emit a different command each run
	names := make([]string, 0, len(input.Env))
	for name := range input.Env {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		args = append(args, "--env="+name+"="+input.Env[name])
	}

	for _, volume := range input.Volumes {
		args = append(args, "--volume="+volume)
	}

	if input.Memory != "" {
		args = append(args, "--memory="+input.Memory+"m")
	}

	if input.ShmSize != "" {
		args = append(args, "--shm-size="+input.ShmSize)
	}

	args = append(args, LogArgs(input.LogDriver, input.LogOptions)...)

	if input.InitialNetwork != "" {
		args = append(args, "--network="+input.InitialNetwork)
		args = append(args, "--network-alias="+input.NetworkAlias)
	}

	args = append(args, input.TaggedImage)
	args = append(args, input.Command...)

	for _, option := range input.ConfigOptions {
		if option == "" {
			continue
		}

		args = append(args, option)
	}

	return args
}

// LogArgs builds the docker flags for a container's logging.
//
// Separate from DockerCreateArgs because the service container is not the only
// long lived container a service has: the ambassador an exposed service runs is
// built by hand elsewhere, and it is capped by the same values rather than by
// its own.
func LogArgs(driver string, options map[string]string) []string {
	args := []string{}

	if driver != "" {
		args = append(args, "--log-driver="+driver)
	}

	// sorted, because a map would otherwise emit a different command each run
	names := make([]string, 0, len(options))
	for name := range options {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		args = append(args, "--log-opt="+name+"="+options[name])
	}

	return args
}
