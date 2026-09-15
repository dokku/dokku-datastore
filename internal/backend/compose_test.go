package backend

import (
	"strings"
	"testing"
)

func TestComposeArgs(t *testing.T) {
	input := ComposeInput{
		File:    "/var/lib/dokku/services/redis/lollipop/docker-compose.yml",
		Project: "dokku-redis-lollipop",
	}

	expected := "compose --file /var/lib/dokku/services/redis/lollipop/docker-compose.yml --project-name dokku-redis-lollipop create"
	if actual := strings.Join(ComposeArgs(input, "create"), " "); actual != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, actual)
	}
}

// The project is named rather than derived, which compose would otherwise do
// from the directory the file sits in.
func TestComposeArgsNamesTheProject(t *testing.T) {
	args := ComposeArgs(ComposeInput{File: "/f", Project: "p"}, "start")

	for i, arg := range args {
		if arg == "--project-name" {
			if args[i+1] != "p" {
				t.Errorf("expected the project name, got %q", args[i+1])
			}

			return
		}
	}

	t.Errorf("expected the project to be named, got %v", args)
}

func TestSelect(t *testing.T) {
	tests := []struct {
		name     string
		input    SelectInput
		expected string
	}{
		{
			name:     "nothing recorded and no default",
			input:    SelectInput{},
			expected: Docker,
		},
		{
			name:     "the host default",
			input:    SelectInput{Default: Compose},
			expected: Compose,
		},
		{
			// turning the default on must not move a service that already
			// exists: it was created one way and stays that way
			name:     "the service's own record wins",
			input:    SelectInput{Recorded: Docker, Default: Compose},
			expected: Docker,
		},
		{
			name:     "a service recorded as compose on a docker host",
			input:    SelectInput{Recorded: Compose, Default: Docker},
			expected: Compose,
		},
		{
			// a service that cannot be addressed at all is worse than one
			// addressed the way every service was before
			name:     "an unknown name falls back",
			input:    SelectInput{Recorded: "nonsense", Default: "rubbish"},
			expected: Docker,
		},
		{
			name:     "an unknown record still honours the default",
			input:    SelectInput{Recorded: "nonsense", Default: Compose},
			expected: Compose,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := Select(test.input); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}
