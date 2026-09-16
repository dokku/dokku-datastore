package backend

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/dokku/dokku-datastore/internal/execx"

	"github.com/dokku/dokku/plugins/common"
)

// RunInput is the input for Run.
type RunInput struct {
	// Image is the image to run
	Image string

	// Argv is the command and its arguments, already rendered
	Argv []string

	// Env are environment variables for the command. Passing a secret here
	// rather than on Argv keeps it out of the host's process table.
	Env map[string]string

	// Volumes are bind mounts as source:target. An offline command is given the
	// service's own mounts, which is how it reaches the data with the service
	// container down.
	Volumes []string

	// Network joins the container to a network, which a sidecar command needs
	// to reach the service it is talking to.
	Network string

	// User is the in-container user to run as, empty for the image's default
	User string

	// Entrypoint replaces the image's own when set, which is how a plain
	// command runs in an image whose entrypoint starts something else
	Entrypoint *string

	// TTY asks docker for a terminal
	TTY bool

	// Stdin, Stdout and Stderr are wired to the command when set
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// RunArgs builds the argv for `docker container run`. It is pure, so what a
// definition's verb turns into can be pinned by a test without a daemon.
func RunArgs(input RunInput) []string {
	args := []string{"container", "run", "--rm"}

	// sorted, because a map would otherwise emit a different command each run
	names := make([]string, 0, len(input.Env))
	for name := range input.Env {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		args = append(args, "--env="+name+"="+input.Env[name])
	}

	if input.Network != "" {
		args = append(args, "--network="+input.Network)
	}

	if input.Entrypoint != nil {
		args = append(args, "--entrypoint="+*input.Entrypoint)
	}

	if input.User != "" {
		args = append(args, "--user="+input.User)
	}

	for _, volume := range input.Volumes {
		args = append(args, "--volume="+volume)
	}

	args = append(args, "-i")
	if input.TTY {
		args = append(args, "-t")
	}

	args = append(args, input.Image)
	return append(args, input.Argv...)
}

// Run runs a command in a throwaway container. It is what serves a command the
// service container cannot: one that has to touch the data while the service is
// down, or one whose tool the datastore image does not ship.
func Run(ctx context.Context, input RunInput) error {
	if input.Image == "" {
		return fmt.Errorf("no image to run")
	}

	if len(input.Argv) == 0 {
		return fmt.Errorf("nothing to run in %s", input.Image)
	}

	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command:      common.DockerBin(),
		Args:         RunArgs(input),
		Stdin:        input.Stdin,
		StdoutWriter: input.Stdout,
		StderrWriter: input.Stderr,
	}); err != nil {
		return fmt.Errorf("unable to run the command: %w", err)
	}

	return nil
}
