package backend

import (
	"context"
	"fmt"

	"github.com/dokku/dokku-datastore/internal/execx"

	"github.com/dokku/dokku/plugins/common"
)

// Compose is the backend that drives a service through docker compose, reading
// the file rendered into the service's directory.
const Compose = "compose"

// Docker is the backend that drives a service through the docker cli. It is the
// default, and it is what every service that exists today was made with.
const Docker = "docker"

// ComposeInput addresses one service's compose file.
type ComposeInput struct {
	// File is the rendered compose file in the service's directory
	File string

	// Project is the compose project name. It is set rather than left to be
	// derived from the directory name, which would make the project depend on
	// where the service root happens to sit.
	Project string
}

// ComposeArgs builds the argv for a compose subcommand. It is pure, so what a
// service is driven with can be pinned by a test without a docker daemon.
func ComposeArgs(input ComposeInput, arguments ...string) []string {
	args := []string{
		"compose",
		"--file", input.File,
		"--project-name", input.Project,
	}

	return append(args, arguments...)
}

// ComposeCreate creates a service's container without starting it.
//
// Create and start are separate for the same reason they are on the docker
// path: networks are attached to the container between the two, and a container
// that is already running cannot be given them.
func ComposeCreate(ctx context.Context, input ComposeInput) error {
	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    ComposeArgs(input, "create"),
	}); err != nil {
		return fmt.Errorf("failed to create the service container: %w", err)
	}

	return nil
}

// ComposeStart starts a service's container.
func ComposeStart(ctx context.Context, input ComposeInput) error {
	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    ComposeArgs(input, "start"),
	}); err != nil {
		return fmt.Errorf("failed to start the service container: %w", err)
	}

	return nil
}

// SelectInput is the input for Select.
type SelectInput struct {
	// Recorded is the contents of the service's BACKEND file, empty when it has
	// none
	Recorded string

	// Default is the host wide default, from DOKKU_DATASTORE_BACKEND
	Default string
}

// Select reports which backend a service is driven with.
//
// The service's own record wins over the host default, so that turning the
// default on does not move services that already exist: a service is created
// one way and stays that way until something recreates it. An unknown name
// falls back to docker rather than failing, because a service that cannot be
// addressed at all is worse than one addressed the way every service was
// before.
func Select(input SelectInput) string {
	for _, candidate := range []string{input.Recorded, input.Default} {
		switch candidate {
		case Compose:
			return Compose
		case Docker:
			return Docker
		}
	}

	return Docker
}
