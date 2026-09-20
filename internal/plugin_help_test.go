package internal

import (
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"

	"github.com/josegonzalez/cli-skeleton/command"
	flag "github.com/spf13/pflag"
)

func TestSplitArguments(t *testing.T) {
	tests := []struct {
		name     string
		usage    string
		expected []string
	}{
		{name: "empty", usage: "", expected: []string{}},
		{name: "one argument", usage: "<service>", expected: []string{"<service>"}},
		{
			name:     "a flag that takes a value counts once",
			usage:    "<service> [-t|--tail [<tail-num>]]",
			expected: []string{"<service>", "[-t|--tail [<tail-num>]]"},
		},
		{
			name:     "a list argument",
			usage:    "<service> <ports...>",
			expected: []string{"<service>", "<ports...>"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := splitArguments(test.usage)
			if strings.Join(actual, "|") != strings.Join(test.expected, "|") {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

func TestElideArguments(t *testing.T) {
	tests := []struct {
		name     string
		usage    string
		expected string
	}{
		{
			name:     "short enough to leave alone",
			usage:    "<service> <bucket-name> [-u|--use-iam]",
			expected: "<service> <bucket-name> [-u|--use-iam]",
		},
		{
			name:     "too long to list in full",
			usage:    "<service> <aws-access-key-id> <aws-secret-access-key> <aws-default-region> <endpoint-url>",
			expected: "<service> <aws-access-key-id> <aws-secret-access-key>...",
		},
		{
			name:     "a flag with a value is one argument",
			usage:    "<service> [-t|--tail [<tail-num>]]",
			expected: "<service> [-t|--tail [<tail-num>]]",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := elideArguments(test.usage); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

func TestAlignColumns(t *testing.T) {
	aligned := alignColumns([]string{"short,one", "much-longer-entry,two"}, ",")
	expected := []string{
		"short              one",
		"much-longer-entry  two",
	}

	for i, line := range aligned {
		if line != expected[i] {
			t.Errorf("line %d: expected %q, got %q", i, expected[i], line)
		}
	}
}

func helpTestCommands() []PluginCommand {
	return []PluginCommand{
		&fakeCommand{
			name:        "list",
			description: "list all {{.Title}} services",
			usage:       "",
			group:       definition.GroupBasicUsage,
			arguments:   []command.Argument{{Name: "datastore-type", Description: "the type of datastore to list"}},
		},
		&fakeCommand{
			name:        "backup",
			description: "create a backup of the {{.Title}} service to an existing s3 bucket",
			usage:       "<service> <bucket-name> [-u|--use-iam]",
			group:       definition.GroupBackups,
			arguments: []command.Argument{
				{Name: "datastore-type", Description: "the type of datastore to back up"},
				{Name: "service-name", Description: "the name of the service to back up"},
			},
			flags: func(f *flag.FlagSet) {
				f.BoolP("use-iam", "u", false, "use the IAM profile associated with the current server")
			},
			documentation: `backup the 'lollipop' service to the 'my-s3-bucket' bucket on AWS
dokku {{.CommandPrefix}}:backup lollipop my-s3-bucket --use-iam`,
		},
	}
}

func TestPluginSummaryLine(t *testing.T) {
	expected := "    redis, Plugin for managing Redis services"
	if actual := PluginSummaryLine(redisDocumentationData(t)); actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}

func TestPluginCommandLines(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")

	lines, err := PluginCommandLines(PluginHelpInput{
		Commands: helpTestCommands(),
		Data:     redisDocumentationData(t),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []string{
		"    redis:backup <service> <bucket-name> [-u|--use-iam],create a backup of the Redis service to an existing s3 bucket",
		"    redis:list,list all Redis services",
	}

	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %v", len(expected), lines)
	}

	for i, line := range lines {
		if line != expected[i] {
			t.Errorf("line %d: expected %q, got %q", i, expected[i], line)
		}
	}
}

func TestPluginHelpOverview(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")

	overview, err := PluginHelpOverview(PluginHelpInput{
		Commands: helpTestCommands(),
		Data:     redisDocumentationData(t),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := `usage: dokku redis[:COMMAND]

List your redis services.

Example:

    $ dokku redis:list

      Redis services
      service-name

dokku redis commands: (get help with dokku redis:help SUBCOMMAND)

    redis:backup <service> <bucket-name> [-u|--use-iam]  create a backup of the Redis service to an existing s3 bucket
    redis:list                                           list all Redis services

`

	if overview != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, overview)
	}
}

func TestPluginCommandHelp(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")

	help, err := PluginCommandHelp(helpTestCommands()[1], redisDocumentationData(t))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := `usage: dokku redis:backup <service> <bucket-name> [-u|--use-iam]

create a backup of the Redis service to an existing s3 bucket

arguments:

service-name  the name of the service to back up

flags:

-u|--use-iam  use the IAM profile associated with the current server

examples:

backup the 'lollipop' service to the 'my-s3-bucket' bucket on AWS

    dokku redis:backup lollipop my-s3-bucket --use-iam

`

	if help != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, help)
	}
}

func TestRenderHelpExamplesSeparatesProseFromCommands(t *testing.T) {
	documentation := `a redis service can be linked to a container.
> NOTE: this will restart your app
dokku redis:link lollipop playground
the following will be set:

    REDIS_URL=redis://host:6379`

	expected := []string{
		"a redis service can be linked to a container.",
		"",
		"    > NOTE: this will restart your app",
		"",
		"    dokku redis:link lollipop playground",
		"",
		"the following will be set:",
		"",
		"    REDIS_URL=redis://host:6379",
	}

	actual := renderHelpExamples(documentation, helpColors{})
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		t.Errorf("expected:\n%s\ngot:\n%s", strings.Join(expected, "\n"), strings.Join(actual, "\n"))
	}
}

func TestHelpColorsAreDisabledForDumbTerminals(t *testing.T) {
	tests := []struct {
		name          string
		noColor       string
		term          string
		expectedColor bool
	}{
		{name: "a normal terminal", term: "xterm-256color", expectedColor: true},
		{name: "dokku asked for no color", noColor: "1", term: "xterm-256color"},
		{name: "a dumb terminal", term: "dumb"},
		{name: "no terminal at all"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DOKKU_NO_COLOR", test.noColor)
			t.Setenv("NO_COLOR", "")
			t.Setenv("TERM", test.term)

			if colored := newHelpColors().bold != ""; colored != test.expectedColor {
				t.Errorf("expected colored to be %t", test.expectedColor)
			}
		})
	}
}
