// Package verb runs the operations a datastore definition declares: connect,
// export, import, and whatever extra subcommands a definition adds. It turns a
// declared command into a running one, and is the reason a definition can
// describe an operation without any Go code knowing what that operation is.
package verb

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

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

	// Name is the verb to run: connect, export, import, or a command the
	// datastore adds
	Name string

	// Command runs this rather than looking Name up, which is how a hook runs:
	// it is a command in every respect except that it is not a subcommand, so
	// there is no name to find it under.
	Command *definition.Command

	// Names are the service's containers. A service command runs in the service
	// container; an offline command stops both and runs beside them.
	Names backend.Names

	// Image is the service's resolved image, which an offline command runs a
	// throwaway container on so that it sees the same data the service does.
	Image string

	// Volumes are the service's bind mounts as source:target, which an offline
	// command needs for the same reason.
	Volumes []string

	// ScriptRoot is where the definition's own bin scripts were written for
	// this service. A host command's argv[0] is resolved against it, so a
	// definition runs something it ships rather than anything on the host's
	// path.
	ScriptRoot string

	// TTY asks docker for a terminal, which only connect wants and only when
	// the caller has one to give
	TTY bool

	// Stdin, Stdout and Stderr are wired to the command when set. Import reads
	// stdin; export writes stdout.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// command is what this input runs: the one it was handed, or the one the
// definition declares under the name it was given.
func (input RunInput) command() (definition.Command, bool) {
	if input.Command != nil {
		return *input.Command, true
	}

	return input.Definition.CommandFor(input.Name)
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
	command, ok := input.command()
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
	command, _ := input.command()

	switch command.Mode {
	case "", definition.ModeService:
	case definition.ModeOffline:
		return runOffline(ctx, input)
	case definition.ModeSidecar:
		return runSidecar(ctx, input)
	case definition.ModeHost:
		return runHost(ctx, input)
	default:
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

// runSidecar runs a command beside a running service rather than inside it.
//
// It shares the service container's network namespace, so a client addressing
// localhost reaches the service exactly as it would from inside, whatever
// network the service is on. That is what makes a verb independent of how the
// service container was built: a service created before its scripts were
// vendored, or by the bash plugin, has nothing to exec, but it still has a
// network namespace to join and data to read.
func runSidecar(ctx context.Context, input RunInput) error {
	exec, err := Resolve(input)
	if err != nil {
		return err
	}

	command, _ := input.command()

	// the command's own image when it names one, since a sidecar exists for a
	// tool the datastore image does not ship; otherwise the service's own
	image := command.Image
	if image == "" {
		image = input.Image
	}

	if image == "" {
		return fmt.Errorf("the %s command runs in a sidecar and so needs an image", input.Name)
	}

	return backend.Run(ctx, backend.RunInput{
		Image:   image,
		Argv:    exec.Argv,
		Env:     exec.Env,
		Volumes: input.Volumes,
		Network: "container:" + input.Names.Container,
		User:    exec.User,
		TTY:     input.TTY,
		Stdin:   exec.Stdin,
		Stdout:  exec.Stdout,
		Stderr:  exec.Stderr,
	})
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

// runHost runs a command on the host rather than in a container.
//
// Two commands need this, and what they do is write an nginx vhost and reload
// nginx, which is not something a container can do to the machine it runs on.
// It runs as whoever runs the binary, which is the dokku user: the part that
// needs root is a separate script the install put somewhere dokku cannot write,
// and the command reaches it through sudo.
//
// argv[0] is resolved against the scripts the definition ships rather than
// against the host's path, and only by base name, so a definition runs what it
// brought with it and cannot reach anything else. Parsing accepts this mode
// only for definitions embedded in the binary, which is what stops a plugin
// checkout from shipping host code at all.
func runHost(ctx context.Context, input RunInput) error {
	resolved, err := Resolve(input)
	if err != nil {
		return err
	}

	if input.ScriptRoot == "" {
		return fmt.Errorf("the %s command runs on the host, which needs the scripts the definition ships", input.Name)
	}

	name := filepath.Base(resolved.Argv[0])
	if name != resolved.Argv[0] {
		return fmt.Errorf("the %s command runs %q, which is a path rather than one of the scripts the definition ships", input.Name, resolved.Argv[0])
	}

	script := filepath.Join(input.ScriptRoot, name)
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("the %s command runs %s, which the definition does not ship: %w", input.Name, name, err)
	}

	command := exec.CommandContext(ctx, script, resolved.Argv[1:]...)

	// the host environment is inherited because the script is a dokku plugin
	// script: it reads DOKKU_ROOT and calls docker. What the definition
	// declares is layered on top of that.
	command.Env = os.Environ()
	for name, value := range resolved.Env {
		command.Env = append(command.Env, name+"="+value)
	}

	command.Stdin = input.Stdin
	command.Stdout = input.Stdout
	command.Stderr = input.Stderr

	return command.Run()
}
