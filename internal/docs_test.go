package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	flag "github.com/spf13/pflag"
)

// fakeCommand stands in for a real command in the documentation tests
type fakeCommand struct {
	name          string
	description   string
	usage         string
	documentation string
	group         string
	arguments     []command.Argument
	flags         func(*flag.FlagSet)
}

func (c *fakeCommand) Name() string          { return c.name }
func (c *fakeCommand) Description() string   { return c.description }
func (c *fakeCommand) Usage() string         { return c.usage }
func (c *fakeCommand) Documentation() string { return c.documentation }
func (c *fakeCommand) Group() string         { return c.group }

func (c *fakeCommand) Arguments() []command.Argument { return c.arguments }

func (c *fakeCommand) FlagSet() *flag.FlagSet {
	f := flag.NewFlagSet(c.name, flag.ContinueOnError)
	f.Bool("quiet", false, "suppress output")
	f.String("format", "text", "the format to output the data in")
	f.Bool("trace", false, "enable trace output")
	f.Bool("no-color", false, "disables colored command output")
	if c.flags != nil {
		c.flags(f)
	}

	return f
}

func redisDocumentationData(t *testing.T) DocumentationData {
	t.Helper()
	clearImageEnv(t)

	return NewDocumentationData(DocumentationDataInput{Datastore: service.Datastores["redis"]})
}

func TestNewDocumentationData(t *testing.T) {
	data := redisDocumentationData(t)

	tests := []struct {
		name     string
		actual   string
		expected string
	}{
		{name: "command prefix", actual: data.CommandPrefix, expected: "redis"},
		{name: "default alias", actual: data.DefaultAlias, expected: "REDIS"},
		{name: "plugin variable", actual: data.PluginVariable, expected: "REDIS"},
		{name: "port", actual: data.Port, expected: "6379"},
		{name: "port list", actual: data.PortList, expected: "6379"},
		{name: "scheme", actual: data.Scheme, expected: "redis"},
		{name: "title", actual: data.Title, expected: "Redis"},
		{name: "image", actual: data.Image, expected: "redis"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, test.actual)
			}
		})
	}
}

func TestNewDocumentationDataPrefersTheEnvironment(t *testing.T) {
	clearImageEnv(t)
	t.Setenv("REDIS_IMAGE", "valkey/valkey")
	t.Setenv("REDIS_IMAGE_VERSION", "9.9.9")

	data := NewDocumentationData(DocumentationDataInput{Datastore: service.Datastores["redis"]})
	if data.Image != "valkey/valkey" {
		t.Errorf("expected the image from the environment, got %q", data.Image)
	}

	if data.ImageVersion != "9.9.9" {
		t.Errorf("expected the image version from the environment, got %q", data.ImageVersion)
	}
}

// A plugin pins its image in the definition it ships rather than in a Dockerfile
// of its own, so the default the readme documents comes from there. A Dockerfile
// left at a plugin's root is no longer read, and must not be: it would be a
// second pin, free to disagree with the one the services actually run.
func TestNewDocumentationDataIgnoresAPluginDockerfile(t *testing.T) {
	clearImageEnv(t)

	pluginDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pluginDir, "Dockerfile"), []byte("FROM redis:1.2.3\n"), 0644); err != nil {
		t.Fatalf("unable to write the dockerfile: %s", err)
	}

	data := NewDocumentationData(DocumentationDataInput{
		Datastore: service.Datastores["redis"],
		PluginDir: pluginDir,
	})

	redis := service.Datastores["redis"].Definition
	if data.Image != redis.DefaultImage || data.ImageVersion != redis.DefaultImageVersion {
		t.Errorf("expected the definition's %s:%s, got %s:%s",
			redis.DefaultImage, redis.DefaultImageVersion, data.Image, data.ImageVersion)
	}
}

func TestClassifyDocLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected DocLineKind
	}{
		{name: "prose", line: "you can tail logs for a service", expected: DocProse},
		{name: "a dokku command", line: "dokku redis:logs lollipop", expected: DocCommand},
		{name: "an export", line: `export REDIS_IMAGE="redis"`, expected: DocCommand},
		{name: "a note", line: "> NOTE: this will restart your app", expected: DocNote},
		{name: "a literal block", line: "    REDIS_URL=redis://host:6379", expected: DocCode},
		{name: "a blank line", line: "", expected: DocBlank},
		{name: "a line that only mentions dokku", line: "when calling dokku config", expected: DocProse},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := classifyDocLine(test.line); actual != test.expected {
				t.Errorf("expected kind %d, got %d", test.expected, actual)
			}
		})
	}
}

func TestDocumentationBlocks(t *testing.T) {
	documentation := `you can tail logs for a particular service:
dokku redis:logs lollipop
the following will be set:

    REDIS_URL=redis://host:6379

> NOTE: this will restart your app`

	blocks := DocumentationBlocks(documentation)
	expected := []DocBlock{
		{Kind: DocProse, Lines: []string{"you can tail logs for a particular service:"}},
		{Kind: DocCommand, Lines: []string{"dokku redis:logs lollipop"}},
		{Kind: DocProse, Lines: []string{"the following will be set:"}},
		{Kind: DocCode, Lines: []string{"REDIS_URL=redis://host:6379"}},
		{Kind: DocNote, Lines: []string{"> NOTE: this will restart your app"}},
	}

	if len(blocks) != len(expected) {
		t.Fatalf("expected %d blocks, got %d: %v", len(expected), len(blocks), blocks)
	}

	for i, block := range blocks {
		if block.Kind != expected[i].Kind {
			t.Errorf("block %d: expected kind %d, got %d", i, expected[i].Kind, block.Kind)
		}

		if len(block.Lines) != len(expected[i].Lines) || block.Lines[0] != expected[i].Lines[0] {
			t.Errorf("block %d: expected %v, got %v", i, expected[i].Lines, block.Lines)
		}
	}
}

func TestRenderDocumentation(t *testing.T) {
	data := redisDocumentationData(t)

	rendered, err := RenderDocumentation("dokku {{.CommandPrefix}}:link lollipop playground sets {{.DefaultAlias}}_URL on port {{.Port}}", data)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := "dokku redis:link lollipop playground sets REDIS_URL on port 6379"
	if rendered != expected {
		t.Errorf("expected %q, got %q", expected, rendered)
	}
}

func TestRenderDocumentationRejectsUnknownFields(t *testing.T) {
	if _, err := RenderDocumentation("{{.NotAField}}", redisDocumentationData(t)); err == nil {
		t.Error("expected an error for a field the documentation data does not have")
	}
}

func TestDocumentedFlagsLeavesOutTheFlagsDokkuConsumes(t *testing.T) {
	c := &fakeCommand{
		name: "backup",
		flags: func(f *flag.FlagSet) {
			f.BoolP("use-iam", "u", false, "use the IAM profile associated with the current server")
			f.String("bucket", "", "the bucket to back up to for {{.CommandPrefix}}")
		},
	}

	flags, err := DocumentedFlags(c, redisDocumentationData(t))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []DocumentedFlag{
		{Label: "--bucket <string>", Description: "the bucket to back up to for redis"},
		{Label: "-u|--use-iam", Description: "use the IAM profile associated with the current server"},
	}

	if len(flags) != len(expected) {
		t.Fatalf("expected %d flags, got %v", len(expected), flags)
	}

	for i, f := range flags {
		if f != expected[i] {
			t.Errorf("flag %d: expected %v, got %v", i, expected[i], f)
		}
	}
}

func TestDocumentedArgumentsLeavesOutTheDatastoreType(t *testing.T) {
	c := &fakeCommand{
		name: "backup",
		arguments: []command.Argument{
			{Name: "datastore-type", Description: "the type of datastore to back up"},
			{Name: "service-name", Description: "the name of the service to back up"},
			{Name: "bucket-name", Description: "the name of the s3 bucket to upload the backup to"},
		},
	}

	arguments := DocumentedArguments(c)
	if len(arguments) != 2 {
		t.Fatalf("expected 2 arguments, got %v", arguments)
	}

	if arguments[0].Label != "service-name" || arguments[1].Label != "bucket-name" {
		t.Errorf("unexpected arguments: %v", arguments)
	}
}
