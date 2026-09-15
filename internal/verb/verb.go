// Package verb runs the operations a datastore definition declares: connect,
// export, import, and whatever extra subcommands a definition adds. It turns a
// declared command into a running one, and is the reason a definition can
// describe an operation without any Go code knowing what that operation is.
package verb

import (
	"context"
	"fmt"
	"io"

	"github.com/dokku/dokku-datastore/internal/backend"
	"github.com/dokku/dokku-datastore/internal/definition"
)

// RunInput is the input for Run.
type RunInput struct {
	// Definition is the datastore definition declaring the verb
	Definition definition.Definition

	// Scope is the service's resolved state, which the verb's command and
	// environment are rendered against
	Scope definition.Scope

	// Name is the verb to run: connect, export, import, or an extra subcommand
	Name string

	// Names are the service's containers. A service command runs in the service
	// container; an offline command stops both and runs beside them.
	Names backend.Names

	// Image is the service's resolved image, which an offline command runs a
	// throwaway container on so that it sees the same data the service does.
	Image string

	// Volumes are the service's bind mounts as source:target, which an offline
	// command needs for the same reason.
	Volumes []string

	// TTY asks docker for a terminal, which only connect wants and only when
	// the caller has one to give
	TTY bool

	// Stdin, Stdout and Stderr are wired to the command when set. Import reads
	// stdin; export writes stdout.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ErrNotImplemented is returned for a verb the definition does not declare. It
// is how a datastore says it cannot do something, rather than failing part way
// through trying.
type ErrNotImplemented struct {
	// Plugin is the datastore type
	Plugin string

	// Name is the verb that is missing
	Name string
}

func (e ErrNotImplemented) Error() string {
	return fmt.Sprintf("%s does not implement %s", e.Plugin, e.Name)
}

// Resolve renders a verb into something runnable, without running it. It is
// separate from Run so that what a definition turns into can be tested without
// a docker daemon.
func Resolve(input RunInput) (backend.ExecInput, error) {
	command, ok := input.Definition.Dokku.Commands[input.Name]
	if !ok {
		return backend.ExecInput{}, ErrNotImplemented{
			Plugin: input.Definition.Dokku.Plugin,
			Name:   input.Name,
		}
	}

	// an element that renders empty is dropped, the way the shell guards the
	// bash plugins used around an unset value
	argv, err := definition.RenderAll(command.Exec, input.Scope)
	if err != nil {
		return backend.ExecInput{}, err
	}

	if len(argv) == 0 {
		return backend.ExecInput{}, fmt.Errorf("the %s command rendered to nothing", input.Name)
	}

	env := map[string]string{}
	for name, value := range command.Env {
		rendered, err := definition.Render(value, input.Scope)
		if err != nil {
			return backend.ExecInput{}, err
		}

		env[name] = rendered
	}

	return backend.ExecInput{
		Container: input.Names.Container,
		Argv:      argv,
		Env:       env,
		User:      command.User,
		TTY:       input.TTY,
		Stdin:     input.Stdin,
		Stdout:    input.Stdout,
		Stderr:    input.Stderr,
	}, nil
}

// Run executes a verb against a running service.
func Run(ctx context.Context, input RunInput) error {
	command := input.Definition.Dokku.Commands[input.Name]

	switch command.Mode {
	case "", definition.ModeService:
	case definition.ModeOffline:
		return runOffline(ctx, input)
	default:
		// sidecar and host each need machinery that does not exist yet, and
		// failing here is better than running the command somewhere other than
		// where the definition asked for
		return fmt.Errorf("the %s command runs in mode %q, which is not supported yet", input.Name, command.Mode)
	}

	exec, err := Resolve(input)
	if err != nil {
		return err
	}

	return backend.Exec(ctx, exec)
}

// runOffline runs a command against a service's data with the service down.
// Redis's import is why this exists: redis reads its dump at boot and writes it
// again on shutdown, so replacing the file underneath a running server would be
// undone twice over.
func runOffline(ctx context.Context, input RunInput) error {
	exec, err := Resolve(input)
	if err != nil {
		return err
	}

	if input.Image == "" {
		return fmt.Errorf("the %s command runs offline and so needs the service's image", input.Name)
	}

	if err := backend.Pause(ctx, backend.PauseInput{Names: input.Names}); err != nil {
		return err
	}

	// the service is stopped rather than removed, so that bringing it back is a
	// start: the container keeps its id, its mounts and its network attachments,
	// and nothing has to be recreated from a definition that may have moved on
	runErr := backend.Run(ctx, backend.RunInput{
		Image:   input.Image,
		Argv:    exec.Argv,
		Env:     exec.Env,
		Volumes: input.Volumes,
		User:    exec.User,
		Stdin:   exec.Stdin,
		Stdout:  exec.Stdout,
		Stderr:  exec.Stderr,
	})

	// the service comes back up either way: a datastore left down because an
	// import failed is a worse outcome than the failed import
	if err := backend.Resume(ctx, input.Names); err != nil {
		if runErr != nil {
			return runErr
		}

		return err
	}

	return runErr
}
