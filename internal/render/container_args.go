package render

import (
	"sort"
	"strings"

	"github.com/dokku/dokku-datastore/internal/definition"
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

	// Publish are the docker --publish specs the container publishes its
	// ports with, each [address:]host:container[/udp]. Empty for a service
	// that is not exposed, or that is exposed through an ambassador
	Publish []string

	// RestartPolicy is the docker restart policy, empty for DefaultRestartPolicy
	RestartPolicy string

	// ShmSize is a shared memory size, with its unit
	ShmSize string

	// TaggedImage is the fully resolved image reference
	TaggedImage string

	// Volumes are host:container bind mounts, in the order they are passed
	Volumes []string

	// VolumeMounts are docker volumes mounted from a subpath, which -v cannot
	// express and so are passed to --mount after the volumes above
	VolumeMounts []definition.VolumeMount

	// WorkingDir is the directory the container starts in, empty for the
	// image's own
	WorkingDir string
}

// NamedVolumes are the docker volumes a container mounts, as opposed to host
// paths, each named once and in the order they are first mounted.
func NamedVolumes(input ContainerArgsInput) []string {
	names := []string{}
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || strings.HasPrefix(name, "/") || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}

	for _, volume := range input.Volumes {
		source, _, _ := strings.Cut(volume, ":")
		add(source)
	}

	for _, mount := range input.VolumeMounts {
		add(mount.Source)
	}

	return names
}

// MountArg is the docker --mount value for a volume mounted from a subpath.
func MountArg(mount definition.VolumeMount) string {
	fields := []string{
		"type=volume",
		"source=" + mount.Source,
		"target=" + mount.Target,
		"volume-subpath=" + mount.Subpath,
	}

	if mount.Readonly {
		fields = append(fields, "readonly")
	}

	if mount.NoCopy {
		fields = append(fields, "volume-nocopy")
	}

	return strings.Join(fields, ",")
}

// PublishedPortsLabel is the label a service container carries naming the
// ports it publishes itself, comma separated. Unset on one that publishes
// none, which is every container made before a service could be exposed
// directly, so what a container publishes can be read without parsing
// docker's own port bindings back into specs.
const PublishedPortsLabel = "dokku.service.published-ports"

// DefaultRestartPolicy is what a container is made with when its service names
// no restart policy. A datastore an app depends on should come back on its own,
// which is why this was the only value there was before there was a choice.
const DefaultRestartPolicy = "always"

// RestartPolicy is the restart policy a container is made with: the one its
// service names, and otherwise the default.
//
// Applied here rather than where the property is read, because the ambassador an
// exposed service runs is made elsewhere and has to land on the same answer, and
// an empty policy means something else to each of the things that make one.
func RestartPolicy(value string) string {
	if value == "" {
		return DefaultRestartPolicy
	}

	return value
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
	}

	if len(input.Publish) > 0 {
		args = append(args, "--label="+PublishedPortsLabel+"="+strings.Join(input.Publish, ","))
	}

	args = append(args,
		"--name="+input.ContainerName,
		"--restart="+RestartPolicy(input.RestartPolicy),
	)

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

	for _, mount := range input.VolumeMounts {
		args = append(args, "--mount="+MountArg(mount))
	}

	if input.Memory != "" {
		args = append(args, "--memory="+input.Memory+"m")
	}

	if input.ShmSize != "" {
		args = append(args, "--shm-size="+input.ShmSize)
	}

	if input.WorkingDir != "" {
		args = append(args, "--workdir="+input.WorkingDir)
	}

	args = append(args, LogArgs(input.LogDriver, input.LogOptions)...)

	if input.InitialNetwork != "" {
		args = append(args, "--network="+input.InitialNetwork)
		args = append(args, "--network-alias="+input.NetworkAlias)
	}

	for _, spec := range input.Publish {
		args = append(args, "--publish="+spec)
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
