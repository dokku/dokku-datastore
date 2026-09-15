package datastores

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

	// EnvFile holds the custom environment the service was created with
	EnvFile string

	// IDFile is where docker writes the container id
	IDFile string

	// InitialNetwork is the network to attach at create time. When it is empty no
	// network is requested at all and the container lands on the default bridge
	// with no alias, which is what tests/link_networks.bats asserts.
	InitialNetwork string

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

// ContainerArgs builds the argv for `docker container create`. It is pure, so the
// exact command a service is created with can be pinned by a test that needs
// neither a filesystem nor a docker daemon.
func ContainerArgs(input ContainerArgsInput) []string {
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

	for _, volume := range input.Volumes {
		args = append(args, "--volume="+volume)
	}

	if input.Memory != "" {
		args = append(args, "--memory="+input.Memory+"m")
	}

	if input.ShmSize != "" {
		args = append(args, "--shm-size="+input.ShmSize)
	}

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
