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

// The create and upgrade templates only document naming a definition for a
// datastore with more than one to choose between.
func TestDefinitionIsDocumentedWhereThereIsAChoice(t *testing.T) {
	commands := []internal.PluginCommand{&CreateCommand{}, &UpgradeCommand{}}

	for name, expected := range map[string]bool{"postgres": true, "redis": false} {
		t.Run(name, func(t *testing.T) {
			data := internal.NewDocumentationData(internal.DocumentationDataInput{Datastore: service.Datastores[name]})

			for _, c := range commands {
				documentation, err := internal.RenderDocumentation(c.Documentation(), data)
				if err != nil {
					t.Fatalf("unable to render the %s documentation: %s", c.Name(), err)
				}

				if actual := strings.Contains(documentation, "--definition"); actual != expected {
					t.Errorf("expected %s to document --definition to be %t, got %t:\n%s", c.Name(), expected, actual, documentation)
				}

				if strings.Contains(documentation, "\n\n") {
					t.Errorf("expected the %s documentation to have no blank lines left by the conditionals", c.Name())
				}
			}
		})
	}
}

// A host whose sudo drops the caller's environment never hands an exported
// variable to the binary, so the create documentation names the flag for each
// setting rather than a variable to export.
func TestCreateDocumentationExportsNothing(t *testing.T) {
	for _, name := range []string{"postgres", "redis", "graphite"} {
		t.Run(name, func(t *testing.T) {
			data := internal.NewDocumentationData(internal.DocumentationDataInput{Datastore: service.Datastores[name]})

			documentation, err := internal.RenderDocumentation((&CreateCommand{}).Documentation(), data)
			if err != nil {
				t.Fatalf("unable to render the create documentation: %s", err)
			}

			for _, unexpected := range []string{"export ", data.PluginVariable + "_"} {
				if strings.Contains(documentation, unexpected) {
					t.Errorf("expected the create documentation not to contain %q:\n%s", unexpected, documentation)
				}
			}

			for _, expected := range []string{"--image", "--image-version", "--custom-env", "set --global wait-timeout"} {
				if !strings.Contains(documentation, expected) {
					t.Errorf("expected the create documentation to contain %q:\n%s", expected, documentation)
				}
			}
		})
	}
}
