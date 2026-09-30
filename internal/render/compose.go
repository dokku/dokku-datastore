package render

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeFile is the rendered compose document for one service. It is built
// from the same resolved values the docker argv is built from, which is the
// whole guarantee that the two execution paths agree: a divergence is a bug
// here rather than in a backend.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Networks map[string]composeNetwork `yaml:"networks,omitempty"`
	Volumes  map[string]composeNetwork `yaml:"volumes,omitempty"`
}

// composeService is the service as dokku runs it, rather than as the definition
// declares it.
//
// There is deliberately no expose key. A container exposes what its image
// declares and nothing more, which is what the docker path produces. The port
// names live in the definition, where the dsn, the readiness probe and the
// ambassador read them. Ports are only published for a service exposed
// directly rather than through an ambassador, with the same specs the docker
// path hands --publish.
type composeService struct {
	// ContainerName and Hostname are pinned, which is what lets every read path
	// address the container by name whichever backend created it.
	ContainerName string `yaml:"container_name"`
	Hostname      string `yaml:"hostname"`

	Image   string   `yaml:"image"`
	Command []string `yaml:"command,omitempty"`
	Restart string   `yaml:"restart"`

	// Labels are dokku's, not the definition's: a definition setting one is a
	// load error.
	Labels map[string]string `yaml:"labels"`

	// Environment carries literal values rather than pointing at the env file,
	// because compose interpolates ${...} in an env_file and docker does not,
	// so a custom env containing a dollar sign would mean two different things.
	Environment map[string]string `yaml:"environment,omitempty"`

	// Volumes are the short syntax strings the docker path passes to -v,
	// followed by a composeVolume for each mount only --mount can express.
	// Compose takes the two syntaxes mixed in one list.
	Volumes    []any  `yaml:"volumes,omitempty"`
	ShmSize    string `yaml:"shm_size,omitempty"`
	WorkingDir string `yaml:"working_dir,omitempty"`

	Logging *composeLogging `yaml:"logging,omitempty"`

	// NetworkMode pins the default bridge when no network was asked for.
	// Compose would otherwise invent a project network, and a service that was
	// on the bridge would quietly move.
	NetworkMode string                       `yaml:"network_mode,omitempty"`
	Networks    map[string]composeServiceNet `yaml:"networks,omitempty"`

	Ports []string `yaml:"ports,omitempty"`

	Deploy *composeDeploy `yaml:"deploy,omitempty"`
}

// composeVolume is compose's long volume syntax, for a docker volume mounted
// from a subpath within it.
type composeVolume struct {
	Type     string              `yaml:"type"`
	Source   string              `yaml:"source"`
	Target   string              `yaml:"target"`
	ReadOnly bool                `yaml:"read_only,omitempty"`
	Volume   composeVolumeOption `yaml:"volume"`
}

type composeVolumeOption struct {
	NoCopy  bool   `yaml:"nocopy,omitempty"`
	Subpath string `yaml:"subpath"`
}

// composeVolumes is the volumes list for a service: what the docker path passes
// to -v, then what it passes to --mount.
func composeVolumes(arguments ContainerArgsInput) []any {
	volumes := make([]any, 0, len(arguments.Volumes)+len(arguments.VolumeMounts))
	for _, volume := range arguments.Volumes {
		volumes = append(volumes, volume)
	}

	for _, mount := range arguments.VolumeMounts {
		volumes = append(volumes, composeVolume{
			Type:     "volume",
			Source:   mount.Source,
			Target:   mount.Target,
			ReadOnly: mount.Readonly,
			Volume:   composeVolumeOption{NoCopy: mount.NoCopy, Subpath: mount.Subpath},
		})
	}

	if len(volumes) == 0 {
		return nil
	}

	return volumes
}

// composeServiceNet is a service's attachment to one network.
type composeServiceNet struct {
	Aliases []string `yaml:"aliases,omitempty"`
}

// composeNetwork declares a network the service joins, or a docker volume it
// mounts. It is always external: dokku's networks are made by dokku, and a
// volume is made before the container is, so compose must attach to one rather
// than create it under a name of its own.
type composeNetwork struct {
	External bool   `yaml:"external"`
	Name     string `yaml:"name"`
}

// composeLogging carries the docker logging a container is made with. It is a
// pointer for the same reason composeDeploy is: a service with nothing to say
// about its logging must emit no logging key at all, or compose would pin a
// driver the docker path leaves to the daemon.
type composeLogging struct {
	Driver  string            `yaml:"driver,omitempty"`
	Options map[string]string `yaml:"options,omitempty"`
}

// composeDeploy carries the memory limit, which is the one resource knob a
// datastore takes.
type composeDeploy struct {
	Resources composeResources `yaml:"resources"`
}

type composeResources struct {
	Limits composeLimits `yaml:"limits"`
}

type composeLimits struct {
	Memory string `yaml:"memory,omitempty"`
}

// Compose renders the compose file for a service.
func Compose(input Input) ([]byte, error) {
	arguments, err := ContainerArgs(input)
	if err != nil {
		return nil, err
	}

	service := composeService{
		ContainerName: arguments.ContainerName,
		Hostname:      arguments.ContainerName,
		Image:         arguments.TaggedImage,
		Command:       append(append([]string{}, arguments.Command...), arguments.ConfigOptions...),
		Restart:       RestartPolicy(arguments.RestartPolicy),
		Labels: map[string]string{
			"dokku":         "service",
			"dokku.service": arguments.CommandPrefix,
		},
		Environment: environment(input.Environment, arguments.Env),
		Volumes:     composeVolumes(arguments),
		ShmSize:     arguments.ShmSize,
		WorkingDir:  arguments.WorkingDir,
		Ports:       arguments.Publish,
	}

	if len(arguments.Publish) > 0 {
		service.Labels[PublishedPortsLabel] = strings.Join(arguments.Publish, ",")
	}

	if arguments.Memory != "" {
		service.Deploy = &composeDeploy{
			Resources: composeResources{Limits: composeLimits{Memory: arguments.Memory + "m"}},
		}
	}

	if arguments.LogDriver != "" || len(arguments.LogOptions) > 0 {
		service.Logging = &composeLogging{
			Driver:  arguments.LogDriver,
			Options: logOptions(arguments.LogOptions),
		}
	}

	file := composeFile{}
	if arguments.InitialNetwork == "" {
		service.NetworkMode = "bridge"
	} else {
		service.Networks = map[string]composeServiceNet{
			arguments.InitialNetwork: {Aliases: []string{arguments.NetworkAlias}},
		}
		file.Networks = map[string]composeNetwork{
			arguments.InitialNetwork: {External: true, Name: arguments.InitialNetwork},
		}
	}

	// compose refuses a service that mounts a docker volume the file does not
	// declare, where docker's -v creates one of that name
	for _, name := range NamedVolumes(arguments) {
		if file.Volumes == nil {
			file.Volumes = map[string]composeNetwork{}
		}
		file.Volumes[name] = composeNetwork{External: true, Name: name}
	}

	file.Services = map[string]composeService{input.Definition.Dokku.Plugin: service}

	rendered, err := yaml.Marshal(file)
	if err != nil {
		return nil, fmt.Errorf("unable to render the compose file: %w", err)
	}

	return append([]byte("---\n"), rendered...), nil
}

// environment turns the service's env lines and the definition's declared
// environment into compose's mapping form.
//
// The declared environment is applied last, which is the same order docker
// applies it in: a named value wins over the file, so what the definition needs
// cannot be unset by a custom env.
//
// A dollar sign is doubled, because compose expands ${...} in a value and
// docker's env file does not: left alone, a password containing one would reach
// the container as something else, or as nothing.
func environment(lines []string, declared map[string]string) map[string]string {
	values := map[string]string{}

	for _, line := range lines {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || name == "" {
			continue
		}

		values[name] = strings.ReplaceAll(value, "$", "$$")
	}

	for name, value := range declared {
		values[name] = strings.ReplaceAll(value, "$", "$$")
	}

	if len(values) == 0 {
		return nil
	}

	return values
}

// logOptions renders the log options compose is handed.
//
// A dollar sign is doubled for the same reason it is in an environment value:
// compose expands ${...} in one and docker's own --log-opt does not, so a tag
// template containing one would mean two different things depending on which
// backend made the container.
func logOptions(options map[string]string) map[string]string {
	if len(options) == 0 {
		return nil
	}

	values := make(map[string]string, len(options))
	for name, value := range options {
		values[name] = strings.ReplaceAll(value, "$", "$$")
	}

	return values
}
