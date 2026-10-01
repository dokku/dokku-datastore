package commands

import (
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

// resetArguments parses args the way the reset command does, returning the
// datastore type and the service name
func resetArguments(t *testing.T, c *ResetCommand, args []string) (string, string, error) {
	t.Helper()

	flags := c.FlagSet()
	if err := flags.Parse(args); err != nil {
		return "", "", err
	}

	arguments, err := c.ParsedArguments(flags.Args())
	if err != nil {
		return "", "", err
	}

	return arguments["datastore-type"].StringValue(), arguments["service-name"].StringValue(), nil
}

func TestResetCommandArguments(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		expectedForce bool
	}{
		{
			name: "asks before resetting by default",
			args: []string{"redis", "lollipop"},
		},
		{
			name:          "--force skips the question",
			args:          []string{"redis", "lollipop", "--force"},
			expectedForce: true,
		},
		{
			name:          "-f skips the question",
			args:          []string{"-f", "redis", "lollipop"},
			expectedForce: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := &ResetCommand{}
			datastoreType, serviceName, err := resetArguments(t, c, test.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if datastoreType != "redis" {
				t.Errorf("expected datastore type %q, got %q", "redis", datastoreType)
			}

			if serviceName != "lollipop" {
				t.Errorf("expected service %q, got %q", "lollipop", serviceName)
			}

			if c.force != test.expectedForce {
				t.Errorf("expected force=%v, got %v", test.expectedForce, c.force)
			}
		})
	}
}

func TestResetCommandRequiresAServiceName(t *testing.T) {
	if _, _, err := resetArguments(t, &ResetCommand{}, []string{"redis"}); err != service.ErrMissingServiceName {
		t.Errorf("expected %v, got %v", service.ErrMissingServiceName, err)
	}
}
