package internal

import (
	"errors"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
)

// serviceArguments returns the argument definition shared by most commands
func serviceArguments() []command.Argument {
	return []command.Argument{
		{
			Name:        "datastore-type",
			Description: "the type of datastore to act on",
			Optional:    false,
			Type:        command.ArgumentString,
		},
		{
			Name:        "service-name",
			Description: "the name of the service to act on",
			Optional:    false,
			Type:        command.ArgumentString,
		},
	}
}

// exposeArguments returns the argument definition used by the expose command
func exposeArguments() []command.Argument {
	return append(serviceArguments(), command.Argument{
		Name:        "ports",
		Description: "the ports to expose on the host",
		Optional:    true,
		Type:        command.ArgumentList,
	})
}

func TestParseArguments(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		arguments   []command.Argument
		expectedErr string
	}{
		{
			name:        "missing service name",
			args:        []string{"redis"},
			arguments:   serviceArguments(),
			expectedErr: service.MissingServiceNameMessage,
		},
		{
			name:        "missing service name for a command with optional arguments",
			args:        []string{"redis"},
			arguments:   exposeArguments(),
			expectedErr: service.MissingServiceNameMessage,
		},
		{
			name:        "missing every argument",
			args:        []string{},
			arguments:   serviceArguments(),
			expectedErr: "This command requires 2 arguments: <datastore-type> <service-name>",
		},
		{
			name:        "too many arguments",
			args:        []string{"redis", "lollipop", "extra"},
			arguments:   serviceArguments(),
			expectedErr: "This command requires 2 arguments, 3 arguments given: <datastore-type> <service-name>",
		},
		{
			name:      "all arguments present",
			args:      []string{"redis", "lollipop"},
			arguments: serviceArguments(),
		},
		{
			name:      "optional list argument omitted",
			args:      []string{"redis", "lollipop"},
			arguments: exposeArguments(),
		},
		{
			name:      "optional list argument with multiple values",
			args:      []string{"redis", "lollipop", "6379", "6380"},
			arguments: exposeArguments(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseArguments(test.args, test.arguments)
			if test.expectedErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %q", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error %q, got none", test.expectedErr)
			}
			if err.Error() != test.expectedErr {
				t.Errorf("expected error %q, got %q", test.expectedErr, err)
			}
			if test.expectedErr == service.MissingServiceNameMessage && !errors.Is(err, service.ErrMissingServiceName) {
				t.Errorf("expected the error to wrap ErrMissingServiceName")
			}
		})
	}
}

func TestMissingArgument(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		arguments []command.Argument
		expected  string
	}{
		{
			name:      "no arguments given",
			args:      []string{},
			arguments: serviceArguments(),
			expected:  "datastore-type",
		},
		{
			name:      "service name missing",
			args:      []string{"redis"},
			arguments: serviceArguments(),
			expected:  "service-name",
		},
		{
			name:      "all required arguments given",
			args:      []string{"redis", "lollipop"},
			arguments: exposeArguments(),
			expected:  "",
		},
		{
			name:      "optional arguments are never reported",
			args:      []string{"redis", "lollipop"},
			arguments: exposeArguments(),
			expected:  "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := missingArgument(test.args, test.arguments); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}
