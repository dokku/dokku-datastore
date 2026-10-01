package internal

import (
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/service"
)

func mongoDatastore(t *testing.T) *service.Datastore {
	t.Helper()

	mongo, ok := service.Datastores["mongo"]
	if !ok {
		t.Fatal("expected a mongo datastore")
	}

	return mongo
}

// A datastore's own commands are still its commands, so the help and the readme
// list them beside the rest rather than behind the verb that dispatches them.
func TestCustomCommandsForMongo(t *testing.T) {
	commands := CustomCommands(mongoDatastore(t))

	if len(commands) != 1 {
		t.Fatalf("expected one custom command, got %d", len(commands))
	}

	if commands[0].Name() != "connect-admin" {
		t.Errorf("expected connect-admin, got %q", commands[0].Name())
	}

	// rendered against the same data the tool's own descriptions are
	if !strings.Contains(commands[0].Description(), "{{.Title}}") {
		t.Errorf("expected a templated description, got %q", commands[0].Description())
	}

	if commands[0].Usage() != "<service>" {
		t.Errorf("expected the service argument, got %q", commands[0].Usage())
	}
}

// A command that declares arguments documents them, which is what lets the
// readme show what to pass.
func TestCustomCommandUsageShowsArguments(t *testing.T) {
	command := CustomCommand{
		CommandName: "nginx-expose",
		Declared: definition.Command{
			Description: "expose the service",
			Arguments: []definition.Argument{
				{Name: "domain", Description: "the domain to serve on", Optional: true},
			},
		},
	}

	if command.Usage() != "<service> [domain]" {
		t.Errorf("expected an optional domain, got %q", command.Usage())
	}

	// the service is always first, since a command runs against one
	if arguments := command.Arguments(); len(arguments) != 2 || arguments[0].Name != "service" {
		t.Errorf("expected the service first, got %v", arguments)
	}
}

// Postgres prints the certificate its server encrypts connections with, so a
// client off the host can verify it, and removes the data an upgrade across a
// major version kept aside.
func TestCustomCommandsForPostgres(t *testing.T) {
	commands := CustomCommands(service.Datastores["postgres"])

	if len(commands) != 2 || commands[0].Name() != "certificate" || commands[1].Name() != "upgrade-cleanup" {
		t.Fatalf("expected the certificate and upgrade-cleanup commands, got %v", commands)
	}

	for _, command := range commands {
		if command.Usage() != "<service>" {
			t.Errorf("expected %s to take the service argument, got %q", command.Name(), command.Usage())
		}
	}
}

// Redis adds nothing of its own, so it has nothing extra to document.
func TestCustomCommandsForADatastoreWithNone(t *testing.T) {
	if commands := CustomCommands(service.Datastores["redis"]); len(commands) != 0 {
		t.Errorf("expected no custom commands, got %v", commands)
	}
}

func TestPluginSubcommandDispatchesThroughInvoke(t *testing.T) {
	mongo := mongoDatastore(t)
	script, err := PluginSubcommand(PluginSubcommandInput{
		Command: CustomCommand{CommandName: "connect-admin", Declared: mongo.CustomCommands()["connect-admin"]},
		Custom:  true,
		Data: DocumentationData{
			CommandPrefix: "mongo",
			Title:         "MongoDB",
		},
	})
	if err != nil {
		t.Fatalf("unable to render the subcommand: %s", err)
	}

	for _, expected := range []string{
		// the one entry point, named with the command it dispatches
		`dokku-datastore" invoke "$PLUGIN_COMMAND_PREFIX" connect-admin "$@"`,
		// the title is the plugin's own, read from its config at runtime
		`declare desc="connect to the $PLUGIN_SERVICE service as the admin user"`,
		`local cmd="$PLUGIN_COMMAND_PREFIX:connect-admin"`,
	} {
		if !strings.Contains(script, expected) {
			t.Errorf("expected the script to contain %q, got:\n%s", expected, script)
		}
	}
}
