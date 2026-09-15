// Package render turns a definition and a service's state into something
// runnable. It is pure: it reads nothing and writes nothing. Both the docker argv
// and the compose file come from one set of resolved values, which is what stops
// the two execution backends drifting — a difference between them is a bug here,
// not in a backend.
package render

import (
	"fmt"

	"github.com/dokku/dokku-datastore/internal/definition"
)

// Input is the input for ContainerArgs.
type Input struct {
	// Definition is the datastore definition being rendered.
	Definition definition.Definition

	// Scope is the service's resolved state.
	Scope definition.Scope

	// ConfigOptions are the user's --config-options, already split the way a
	// shell would split them. They are appended after the definition's command,
	// which is what lets a definition supply a fixed prefix such as mongod or
	// solr-precreate and the user's free text follow it.
	ConfigOptions []string

	// IDFile and EnvFile are the service root paths docker writes the container
	// id to and reads the custom environment from.
	IDFile  string
	EnvFile string
}

// ContainerArgs resolves a definition into the input for the container argv
// builder. The memory limit, shared memory size and initial network are applied
// here rather than declared by a definition, because they are dokku-level knobs
// that every datastore takes identically.
func ContainerArgs(input Input) (ContainerArgsInput, error) {
	service := input.Definition.Service

	image, err := definition.Render(service.Image, input.Scope)
	if err != nil {
		return ContainerArgsInput{}, err
	}

	command, err := definition.RenderAll(service.Command, input.Scope)
	if err != nil {
		return ContainerArgsInput{}, err
	}

	volumes := make([]string, 0, len(service.Volumes))
	for _, volume := range service.Volumes {
		source, err := definition.Render(volume.Source, input.Scope)
		if err != nil {
			return ContainerArgsInput{}, err
		}

		if source == "" {
			return ContainerArgsInput{}, fmt.Errorf("volume for %s rendered an empty source", volume.Target)
		}

		volumes = append(volumes, source+":"+volume.Target)
	}

	// the payload is mounted rather than baked into an image, which is what
	// keeps a definition that vendors a script on the pull path
	for _, file := range RootfsFiles(input) {
		volumes = append(volumes, file.Mount)
	}

	return ContainerArgsInput{
		CommandPrefix:  input.Definition.Dokku.Plugin,
		Command:        command,
		ConfigOptions:  input.ConfigOptions,
		ContainerName:  input.Scope.ContainerName,
		EnvFile:        input.EnvFile,
		IDFile:         input.IDFile,
		InitialNetwork: input.Scope.InitialNetwork,
		Memory:         input.Scope.Memory,
		NetworkAlias:   input.Scope.Host,
		ShmSize:        input.Scope.ShmSize,
		TaggedImage:    image,
		Volumes:        volumes,
	}, nil
}
