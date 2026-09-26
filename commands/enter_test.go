package commands

import (
	"slices"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

// enterArguments parses args the way the enter command does, returning the
// service name and the command it would run in the container
func enterArguments(t *testing.T, c *EnterCommand, args []string) (string, []string, error) {
	t.Helper()

	flags := c.FlagSet()
	if err := flags.Parse(args); err != nil {
		return "", nil, err
	}

	arguments, err := c.ParsedArguments(flags.Args())
	if err != nil {
		return "", nil, err
	}

	return arguments["service-name"].StringValue(), containerCommand(arguments), nil
}

func TestEnterCommandArguments(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		expectedService string
		expectedCommand []string
	}{
		{
			name:            "no command opens a prompt",
			args:            []string{"redis", "lollipop"},
			expectedService: "lollipop",
			expectedCommand: []string{},
		},
		{
			// the command from issue 241, whose flags were taken as enter's own
			name:            "a command keeps its flags",
			args:            []string{"mariadb", "lollipop", "mariabackup", "--backup", "--target-dir=/tmp/backup", "-u", "root", "-psecret"},
			expectedService: "lollipop",
			expectedCommand: []string{"mariabackup", "--backup", "--target-dir=/tmp/backup", "-u", "root", "-psecret"},
		},
		{
			name:            "a command fenced off with -- does not run --",
			args:            []string{"mysql", "lollipop", "--", "mysql", "-h", "127.0.0.1"},
			expectedService: "lollipop",
			expectedCommand: []string{"mysql", "-h", "127.0.0.1"},
		},
		{
			name:            "only the first -- is a fence",
			args:            []string{"redis", "lollipop", "--", "--", "ls"},
			expectedService: "lollipop",
			expectedCommand: []string{"--", "ls"},
		},
		{
			name:            "global flags before the datastore type are still read",
			args:            []string{"--quiet", "--trace", "redis", "lollipop", "ls", "--quiet"},
			expectedService: "lollipop",
			expectedCommand: []string{"ls", "--quiet"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceName, containerCommand, err := enterArguments(t, &EnterCommand{}, test.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if serviceName != test.expectedService {
				t.Errorf("expected service %q, got %q", test.expectedService, serviceName)
			}

			if !slices.Equal(containerCommand, test.expectedCommand) {
				t.Errorf("expected command %q, got %q", test.expectedCommand, containerCommand)
			}
		})
	}
}

func TestEnterCommandGlobalFlags(t *testing.T) {
	c := &EnterCommand{}
	if _, _, err := enterArguments(t, c, []string{"--quiet", "--trace", "redis", "lollipop", "ls", "--format", "json"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !c.quiet || !c.trace {
		t.Errorf("expected the flags before the datastore type to be read, got quiet=%v trace=%v", c.quiet, c.trace)
	}

	if c.format != "text" {
		t.Errorf("expected a flag after the service name to be left to the command, got format=%q", c.format)
	}
}

func TestEnterCommandRequiresAServiceName(t *testing.T) {
	if _, _, err := enterArguments(t, &EnterCommand{}, []string{"redis"}); err != service.ErrMissingServiceName {
		t.Errorf("expected %v, got %v", service.ErrMissingServiceName, err)
	}
}
