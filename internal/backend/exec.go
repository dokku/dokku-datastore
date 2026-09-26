package backend

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/dokku/dokku-datastore/internal/execx"

	"github.com/dokku/dokku/plugins/common"
	"golang.org/x/term"
)

// ExecInput is the input for Exec.
type ExecInput struct {
	// Container is the container to run in, by name or id
	Container string

	// Argv is the command and its arguments, already rendered
	Argv []string

	// Env are environment variables for the command. Passing a secret here
	// rather than on Argv keeps it out of the container's process table.
	Env map[string]string

	// User is the in-container user to run as, empty for the image's default
	User string

	// TTY asks docker for a terminal. Asking for one where there is none makes
	// the exec fail outright rather than merely be non interactive, so the
	// caller decides rather than this guessing.
	TTY bool

	// Stdin, Stdout and Stderr are wired to the command when set
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ExecArgs builds the argv for `docker container exec`. It is pure, so what a
// definition's verb turns into can be pinned by a test without a daemon.
func ExecArgs(input ExecInput) []string {
	args := []string{"container", "exec"}

	// sorted, because a map would otherwise emit a different command each run
	names := make([]string, 0, len(input.Env))
	for name := range input.Env {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		args = append(args, "--env="+name+"="+input.Env[name])
	}

	if input.User != "" {
		args = append(args, "--user="+input.User)
	}

	args = append(args, "-i")
	if input.TTY {
		args = append(args, "-t")
	}

	args = append(args, input.Container)
	return append(args, input.Argv...)
}

// Exec runs a command inside a container.
func Exec(ctx context.Context, input ExecInput) error {
	if len(input.Argv) == 0 {
		return fmt.Errorf("nothing to run in %s", input.Container)
	}

	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command:      common.DockerBin(),
		Args:         ExecArgs(input),
		Stdin:        input.Stdin,
		StdoutWriter: input.Stdout,
		StderrWriter: input.Stderr,
	}); err != nil {
		return fmt.Errorf("unable to run the command: %w", err)
	}

	return nil
}

// HasTerminal reports whether a file is a terminal, which is what decides
// whether docker can be asked for one. Being a character device is not enough:
// /dev/null is one, and it is what cron hands a command as its stdin.
func HasTerminal(file *os.File) bool {
	return term.IsTerminal(int(file.Fd()))
}
