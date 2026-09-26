package backend

import (
	"os"
	"strings"
	"testing"
)

func TestExecArgs(t *testing.T) {
	tests := []struct {
		name     string
		input    ExecInput
		expected string
	}{
		{
			name:     "a bare command",
			input:    ExecInput{Container: "dokku.redis.l", Argv: []string{"redis-cli"}},
			expected: "container exec -i dokku.redis.l redis-cli",
		},
		{
			name: "a terminal is asked for only when there is one",
			input: ExecInput{
				Container: "dokku.redis.l",
				Argv:      []string{"redis-cli"},
				TTY:       true,
			},
			expected: "container exec -i -t dokku.redis.l redis-cli",
		},
		{
			// sorted, or the same definition would emit a different command on
			// each run and nothing could be pinned
			name: "environment variables come out sorted",
			input: ExecInput{
				Container: "dokku.redis.l",
				Argv:      []string{"redis-cli"},
				Env:       map[string]string{"LC_ALL": "C.UTF-8", "REDISCLI_AUTH": "hunter2", "LANG": "C.UTF-8"},
			},
			expected: "container exec --env=LANG=C.UTF-8 --env=LC_ALL=C.UTF-8 --env=REDISCLI_AUTH=hunter2 -i dokku.redis.l redis-cli",
		},
		{
			name: "a user to run as",
			input: ExecInput{
				Container: "dokku.postgres.l",
				Argv:      []string{"createdb", "l"},
				User:      "postgres",
			},
			expected: "container exec --user=postgres -i dokku.postgres.l createdb l",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := strings.Join(ExecArgs(test.input), " "); actual != test.expected {
				t.Errorf("expected:\n%s\ngot:\n%s", test.expected, actual)
			}
		})
	}
}

func TestExecArgsPutsTheCommandLast(t *testing.T) {
	// anything after the container name is the command, so a flag appearing
	// there would be passed to the command rather than to docker
	args := ExecArgs(ExecInput{
		Container: "dokku.redis.l",
		Argv:      []string{"sh", "-c", "echo hello"},
		Env:       map[string]string{"A": "1"},
		TTY:       true,
		User:      "redis",
	})

	container := -1
	for i, arg := range args {
		if arg == "dokku.redis.l" {
			container = i
		}
	}

	if container == -1 {
		t.Fatalf("the container is not in the arguments: %v", args)
	}

	if strings.Join(args[container+1:], " ") != "sh -c echo hello" {
		t.Errorf("expected the command to follow the container, got %v", args[container+1:])
	}
}

func TestExecRejectsAnEmptyCommand(t *testing.T) {
	if err := Exec(t.Context(), ExecInput{Container: "dokku.redis.l"}); err == nil {
		t.Error("expected running nothing to be an error rather than a docker invocation")
	}
}

func TestHasTerminalIsNotFooledByACharacterDevice(t *testing.T) {
	// /dev/null is a character device, and it is the stdin cron runs a command
	// with. Asking docker for a terminal there makes the exec fail outright
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("unable to open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	if HasTerminal(devNull) {
		t.Errorf("expected %s not to be a terminal", os.DevNull)
	}
}

func TestHasTerminalIsFalseForAPipe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("unable to create a pipe: %v", err)
	}
	defer reader.Close()
	defer writer.Close()

	if HasTerminal(reader) {
		t.Error("expected a pipe not to be a terminal")
	}
}
