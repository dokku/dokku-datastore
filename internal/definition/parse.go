package definition

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeFile is the subset of a compose document a definition may use. Keys the
// tool owns are named here so that a definition setting one is a load error
// rather than a surprise at create time.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Dokku    Dokku                     `yaml:"x-dokku"`
}

// composeService adds the tool-owned keys to Service purely so they can be
// rejected with a message naming them.
type composeService struct {
	Service       `yaml:",inline"`
	ContainerName string    `yaml:"container_name"`
	Hostname      string    `yaml:"hostname"`
	Restart       string    `yaml:"restart"`
	Labels        yaml.Node `yaml:"labels"`
	Networks      yaml.Node `yaml:"networks"`
}

// ParseInput is the input for Parse.
type ParseInput struct {
	// Name is the definition directory name.
	Name string

	// Compose is the docker-compose.yml.
	Compose []byte

	// Dockerfile is the Dockerfile, whose FROM line supplies the default image.
	Dockerfile []byte

	// Scripts are the bin/ hook scripts, keyed by file name.
	Scripts map[string][]byte

	// Embedded records whether this definition came from the tree compiled into
	// the binary. A host-mode command is accepted only from there, because a
	// plugin checkout may override a definition and host mode runs arbitrary
	// code outside a container.
	Embedded bool
}

// Parse reads and validates a definition. It executes no templates and touches no
// filesystem, so every definition the binary ships can be validated by a unit test.
func Parse(input ParseInput) (Definition, error) {
	file := composeFile{}
	if err := yaml.Unmarshal(input.Compose, &file); err != nil {
		return Definition{}, fmt.Errorf("%s: unable to parse docker-compose.yml: %w", input.Name, err)
	}

	if len(file.Services) != 1 {
		return Definition{}, fmt.Errorf("%s: expected exactly one service, got %d", input.Name, len(file.Services))
	}

	name := ""
	service := composeService{}
	for key, value := range file.Services {
		name = key
		service = value
	}

	definition := Definition{
		Name:       input.Name,
		Service:    service.Service,
		Dokku:      file.Dokku,
		Dockerfile: input.Dockerfile,
		Scripts:    input.Scripts,
	}

	if definition.Dokku.Variable == "" {
		definition.Dokku.Variable = strings.ToUpper(definition.Dokku.Plugin)
	}

	if definition.Dokku.AltAlias == "" {
		// every plugin's alt alias is DOKKU_ plus its variable, graphite's
		// DOKKU_STATSD included, so it is derived rather than restated
		definition.Dokku.AltAlias = "DOKKU_" + definition.Dokku.Variable
	}

	if err := validate(input, name, service, definition); err != nil {
		return Definition{}, err
	}

	return definition, nil
}

// validate rejects a definition the renderer could not honour, so that a mistake
// surfaces when the binary is built rather than when a service is created.
func validate(input ParseInput, serviceKey string, service composeService, definition Definition) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%s: %s", input.Name, fmt.Sprintf(format, args...))
	}

	for key, value := range map[string]bool{
		"container_name": service.ContainerName != "",
		"hostname":       service.Hostname != "",
		"labels":         !service.Labels.IsZero(),
		"networks":       !service.Networks.IsZero(),
		"restart":        service.Restart != "",
	} {
		if value {
			return fail("services.%s.%s is set by dokku and may not be set by a definition", serviceKey, key)
		}
	}

	for _, required := range []struct {
		field string
		value string
	}{
		{"plugin", definition.Dokku.Plugin},
		{"title", definition.Dokku.Title},
		{"scheme", definition.Dokku.Scheme},
		{"alias", definition.Dokku.Alias},
		{"dsn", definition.Dokku.DSN},
	} {
		if required.value == "" {
			return fail("x-dokku.%s is required", required.field)
		}
	}

	if definition.Service.Image == "" {
		return fail("services.%s.image is required", serviceKey)
	}

	seen := map[string]bool{}
	primaries := 0
	for _, port := range definition.Service.Ports {
		if port.Name == "" {
			return fail("every port needs a name, so that the dsn and the readiness probe can address one by meaning")
		}

		if seen[port.Name] {
			return fail("port name %q is used twice", port.Name)
		}
		seen[port.Name] = true

		if port.Target == 0 {
			return fail("port %q needs a target", port.Name)
		}

		if port.Primary {
			primaries++
		}
	}

	if primaries > 1 {
		return fail("only one port may be primary, got %d", primaries)
	}

	for _, volume := range definition.Service.Volumes {
		if volume.Type != "bind" {
			return fail("volume %q must be a bind mount: dokku owns the service root and every read path expects the data on the host", volume.Target)
		}

		if !strings.HasPrefix(volume.Source, "{{ .HostRoot }}") {
			return fail("volume source %q must be rooted at {{ .HostRoot }}, which is the path dockerd sees", volume.Source)
		}

		if volume.Target == "" {
			return fail("volume with source %q needs a target", volume.Source)
		}
	}

	if definition.Dokku.Wait != "" {
		if _, ok := definition.PortFor(definition.Dokku.Wait); !ok {
			return fail("x-dokku.wait names port %q, which is not declared", definition.Dokku.Wait)
		}
	}

	if definition.Service.Healthcheck == nil && definition.Dokku.Wait == "" && len(definition.Service.Ports) > 0 {
		return fail("a definition needs either a healthcheck or x-dokku.wait, or nothing decides when the service is ready")
	}

	for name, secret := range definition.Dokku.Secrets {
		if secret.File == "" {
			return fail("secret %q needs a file", name)
		}

		if secret.Length <= 0 {
			return fail("secret %q needs a positive length", name)
		}
	}

	for name, command := range definition.Dokku.Commands {
		if len(command.Exec) == 0 {
			return fail("command %q needs an exec", name)
		}

		switch command.Mode {
		case "", ModeService, ModeOffline:
		case ModeSidecar:
			if command.Image == "" {
				return fail("command %q runs in a sidecar and so needs an image", name)
			}
		case ModeHost:
			if !input.Embedded {
				return fail("command %q runs on the host, which is only allowed for definitions shipped in the binary", name)
			}
		default:
			return fail("command %q has an unknown mode %q", name, command.Mode)
		}
	}

	return nil
}

// ImageFromDockerfile reads the image a definition pins, and reports whether the
// Dockerfile does anything beyond pinning it. A Dockerfile that only declares its
// base is a no-op the loader can skip building, which is what keeps eighteen of
// the twenty-two definitions on the existing pull path.
func ImageFromDockerfile(contents []byte) (image string, version string, trivial bool, err error) {
	trivial = true
	found := false

	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		switch strings.ToUpper(fields[0]) {
		case "ARG":
			continue
		case "FROM":
			if len(fields) < 2 {
				return "", "", false, fmt.Errorf("FROM needs an image")
			}

			reference := fields[1]
			if strings.HasPrefix(reference, "${") {
				// the ARG IMAGE / FROM ${IMAGE} idiom, so the real default is on
				// the ARG line
				reference = argDefault(contents)
			}

			image, version, found = cutImage(reference)
		default:
			// anything else is a real build: a COPY of vendored tooling, a RUN
			trivial = false
		}
	}

	if image == "" {
		return "", "", false, fmt.Errorf("no FROM instruction")
	}

	if !found {
		version = "latest"
	}

	return image, version, trivial, nil
}

// argDefault returns the default of the IMAGE build argument.
func argDefault(contents []byte) string {
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || strings.ToUpper(fields[0]) != "ARG" {
			continue
		}

		name, value, found := strings.Cut(fields[1], "=")
		if found && name == "IMAGE" {
			return strings.Trim(value, `"`)
		}
	}

	return ""
}

// cutImage splits an image reference into its name and tag.
func cutImage(reference string) (string, string, bool) {
	image, version, found := strings.Cut(reference, ":")
	return image, version, found
}
