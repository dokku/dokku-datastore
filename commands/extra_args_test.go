package commands

import (
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"

	flag "github.com/spf13/pflag"
)

// Everything after a -- is handed to the datastore's tool as it is, a flag the
// command has of its own included, while flags before it are still the
// command's wherever they sit.
func TestSplitExtraArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		positional []string
		extraArgs  []string
		file       string
	}{
		{name: "no dash", args: []string{"mysql", "lollipop"}, positional: []string{"mysql", "lollipop"}},
		{name: "a dash with arguments", args: []string{"mysql", "lollipop", "--", "--hex-blob", "--where=id > 1"}, positional: []string{"mysql", "lollipop"}, extraArgs: []string{"--hex-blob", "--where=id > 1"}},
		{name: "a dash with nothing after it", args: []string{"mysql", "lollipop", "--"}, positional: []string{"mysql", "lollipop"}},
		{name: "a flag before the dash", args: []string{"mysql", "lollipop", "--file", "data.dump", "--", "--hex-blob"}, positional: []string{"mysql", "lollipop"}, extraArgs: []string{"--hex-blob"}, file: "data.dump"},
		{name: "the command's own flag after the dash", args: []string{"mysql", "lollipop", "--", "--file", "data.dump"}, positional: []string{"mysql", "lollipop"}, extraArgs: []string{"--file", "data.dump"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			flags := flag.NewFlagSet("export", flag.ContinueOnError)
			file := flags.StringP("file", "f", "", "")
			if err := flags.Parse(test.args); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			positional, extraArgs := splitExtraArgs(flags.Args(), flags.ArgsLenAtDash())
			if !slices.Equal(positional, test.positional) {
				t.Errorf("expected the arguments %q, got %q", test.positional, positional)
			}

			if !slices.Equal(extraArgs, test.extraArgs) {
				t.Errorf("expected the extra arguments %q, got %q", test.extraArgs, extraArgs)
			}

			if *file != test.file {
				t.Errorf("expected --file to be %q, got %q", test.file, *file)
			}
		})
	}
}

// The shared templates only document extra arguments for a datastore whose
// tools take them.
func TestExtraArgsAreDocumentedWhereTheyAreTaken(t *testing.T) {
	commands := []internal.PluginCommand{&ExportCommand{}, &ImportCommand{}, &SetCommand{}}

	for name, expected := range map[string]bool{"mysql": true, "redis": false} {
		t.Run(name, func(t *testing.T) {
			data := internal.NewDocumentationData(internal.DocumentationDataInput{Datastore: service.Datastores[name]})

			for _, c := range commands {
				usage, err := internal.RenderDocumentation(c.Usage(), data)
				if err != nil {
					t.Fatalf("unable to render the %s usage: %s", c.Name(), err)
				}

				documentation, err := internal.RenderDocumentation(c.Documentation(), data)
				if err != nil {
					t.Fatalf("unable to render the %s documentation: %s", c.Name(), err)
				}

				rendered := usage + "\n" + documentation
				if actual := strings.Contains(rendered, "-args"); actual != expected {
					t.Errorf("expected %s to document extra arguments to be %t, got %t:\n%s", c.Name(), expected, actual, rendered)
				}

				if strings.Contains(documentation, "\n\n") {
					t.Errorf("expected the %s documentation to have no blank lines left by the conditionals", c.Name())
				}
			}
		})
	}
}
