package commands

import (
	"testing"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/josegonzalez/cli-skeleton/command"
)

// triggerArgumentsAcceptExtras checks that a trigger command tolerates more
// arguments than it reads. Dokku passes a trigger whatever that trigger's
// contract carries, not only the parts a given plugin happens to use, and the
// bash implementations simply ignored the rest.
func triggerArgumentsAcceptExtras(t *testing.T, name string, arguments []command.Argument, args []string) {
	t.Helper()

	if _, err := internal.ParseArguments(args, arguments); err != nil {
		t.Errorf("%s rejected %v: %v", name, args, err)
	}
}

func TestTriggerCommandsAcceptExtraArguments(t *testing.T) {
	tests := []struct {
		name      string
		arguments []command.Argument
		args      []string
	}{
		{
			// dokku sends the app name and the image tag
			name:      "trigger-pre-delete",
			arguments: (&TriggerPreDeleteCommand{}).Arguments(),
			args:      []string{"redis", "my-app", "latest"},
		},
		{
			name:      "trigger-pre-start",
			arguments: (&TriggerPreStartCommand{}).Arguments(),
			args:      []string{"redis", "my-app", "something-else"},
		},
		{
			name:      "trigger-pre-restore",
			arguments: (&TriggerPreRestoreCommand{}).Arguments(),
			args:      []string{"redis", "my-app", "something-else"},
		},
		{
			name:      "trigger-post-app-clone-setup",
			arguments: (&TriggerPostAppCloneSetupCommand{}).Arguments(),
			args:      []string{"redis", "old-app", "new-app", "something-else"},
		},
		{
			name:      "trigger-post-app-rename-setup",
			arguments: (&TriggerPostAppRenameSetupCommand{}).Arguments(),
			args:      []string{"redis", "old-app", "new-app", "something-else"},
		},
		{
			name:      "trigger-service-list",
			arguments: (&TriggerServiceListCommand{}).Arguments(),
			args:      []string{"redis", "redis", "something-else"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			triggerArgumentsAcceptExtras(t, test.name, test.arguments, test.args)
		})
	}
}

func TestTriggerCommandsStillRequireADatastore(t *testing.T) {
	// the catch-all must not make the datastore type optional
	if _, err := internal.ParseArguments([]string{}, (&TriggerPreDeleteCommand{}).Arguments()); err == nil {
		t.Error("expected trigger-pre-delete to reject an empty argument list")
	}
}
