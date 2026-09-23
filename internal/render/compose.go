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
}

// composeService is the service as dokku runs it, rather than as the definition
// declares it.
//
// There is deliberately no ports or expose key. A container exposes what its
// image declares and nothing more, which is what the docker path produces, and
// a datastore has never published a port itself. The port names live in the
// definition, where the dsn, the readiness probe and the ambassador read them.
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

	Volumes []string `yaml:"volumes,omitempty"`
	ShmSize string   `yaml:"shm_size,omitempty"`

	Logging *composeLogging `yaml:"logging,omitempty"`

	// NetworkMode pins the default bridge when no network was asked for.
	// Compose would otherwise invent a project network, and a service that was
	// on the bridge would quietly move.
	NetworkMode string                       `yaml:"network_mode,omitempty"`
	Networks    map[string]composeServiceNet `yaml:"networks,omitempty"`

	Deploy *composeDeploy `yaml:"deploy,omitempty"`
}

// composeServiceNet is a service's attachment to one network.
type composeServiceNet struct {
	Aliases []string `yaml:"aliases,omitempty"`
}

// composeNetwork declares a network the service joins. It is always external:
// dokku's networks are made by dokku, and compose must attach to one rather
// than create it.
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
		Restart:       "always",
		Labels: map[string]string{
			"dokku":         "service",
			"dokku.service": arguments.CommandPrefix,
		},
		Environment: environment(input.Environment, arguments.Env),
		Volumes:     arguments.Volumes,
		ShmSize:     arguments.ShmSize,
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
