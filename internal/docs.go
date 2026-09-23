package internal

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
	flag "github.com/spf13/pflag"
)

// PluginCommand is implemented by the commands a dokku datastore plugin exposes
// as subcommands. Both the plugin help and the generated plugin readme render
// from it, which is what keeps the two in agreement.
type PluginCommand interface {
	// Name is the subcommand name, without the plugin's command prefix
	Name() string

	// Description is a template for the one line description of the command
	Description() string

	// Usage is a template for the argument sketch rendered after the command name
	Usage() string

	// Documentation is a template for the long form documentation of the command
	Documentation() string

	// Group is the readme usage section the command is documented under
	Group() string

	// Arguments are the positional arguments the command accepts
	Arguments() []command.Argument

	// FlagSet are the flags the command accepts
	FlagSet() *flag.FlagSet
}

// globalFlags are added to every command's flag set but consumed by dokku
// before a plugin ever sees them, so they are left out of the documentation
var globalFlags = map[string]bool{
	"format":   true,
	"no-color": true,
	"quiet":    true,
	"trace":    true,
}

// DocumentationData is what a command's templates are rendered against. It
// flattens the datastore's properties into the names the bash annotations used,
// so the migrated prose reads the same way.
type DocumentationData struct {
	CommandPrefix  string
	DefaultAlias   string
	Image          string
	ImageVersion   string
	PluginVariable string
	Port           string
	PortList       string
	Scheme         string
	Title          string
}

// DocumentationDataInput is the input for the NewDocumentationData function
type DocumentationDataInput struct {
	// Datastore is the datastore the documentation is rendered for
	Datastore *service.Datastore

	// PluginDir is the plugin checkout to read the pinned image out of. When it
	// is empty, only the environment and the datastore's defaults are consulted.
	PluginDir string
}

// NewDocumentationData builds the template data for a datastore
func NewDocumentationData(input DocumentationDataInput) DocumentationData {
	properties := input.Datastore.Properties()

	ports := make([]string, 0, len(properties.Ports))
	for _, port := range properties.Ports {
		ports = append(ports, strconv.Itoa(port))
	}

	port := ""
	if len(ports) > 0 {
		port = ports[0]
	}

	image, imageVersion := documentedImage(properties)

	return DocumentationData{
		CommandPrefix:  properties.CommandPrefix,
		DefaultAlias:   properties.DefaultAlias,
		Image:          image,
		ImageVersion:   imageVersion,
		PluginVariable: properties.PluginVariable,
		Port:           port,
		PortList:       strings.Join(ports, " "),
		Scheme:         properties.Scheme,
		Title:          input.Datastore.Title(),
	}
}

// documentedImage works out which image the readme and the help are written
// about. An operator exports it as an environment variable at runtime, so that
// is preferred over the datastore's own default; the definition supplies the
// rest.
//
// The environment is read through the same helper create reads it with, so the
// image an operator is shown is the image they would get. It used to be read
// here under one pair of names and there under another, and only one of the two
// pairs was ever set.
//
// A plugin no longer pins the image in a Dockerfile of its own. It pins it in the
// definition, which is where the default here comes from, so the two cannot say
// different things.
func documentedImage(properties service.ServiceStruct) (string, string) {
	image := ImageFromEnv(properties)
	imageVersion := ImageVersionFromEnv(properties)

	if image == "" {
		image = properties.DefaultImage
	}
	if imageVersion == "" {
		imageVersion = properties.DefaultImageVersion
	}

	return image, imageVersion
}

// RenderDocumentation fills a template from a command in for a datastore
func RenderDocumentation(body string, data DocumentationData) (string, error) {
	parsed, err := template.New("documentation").Option("missingkey=error").Parse(body)
	if err != nil {
		return "", fmt.Errorf("unable to parse the documentation template: %w", err)
	}

	var rendered bytes.Buffer
	if err := parsed.Execute(&rendered, data); err != nil {
		return "", fmt.Errorf("unable to render the documentation template: %w", err)
	}

	return rendered.String(), nil
}

// DocLineKind is the sort of content a line of documentation holds
type DocLineKind int

const (
	// DocProse is a line of plain text
	DocProse DocLineKind = iota

	// DocCommand is a shell command the reader can paste
	DocCommand

	// DocCode is a literal line, indented by four spaces in the source
	DocCode

	// DocNote is a blockquote, introduced by a leading >
	DocNote

	// DocBlank is an empty line, used to separate paragraphs
	DocBlank
)

// DocLine is one line of rendered documentation, with the sort of content it holds
type DocLine struct {
	Kind DocLineKind
	Text string
}

// DocBlock is a run of consecutive documentation lines of a single kind
type DocBlock struct {
	Kind  DocLineKind
	Lines []string
}

// classifyDocLine reports what sort of content a documentation line holds. The
// rules are the ones the bash annotations were written against: a line is a
// command when it invokes dokku or exports a variable, a literal when it is
// indented further, and a note when it opens with a blockquote marker.
func classifyDocLine(line string) DocLineKind {
	switch {
	case strings.TrimSpace(line) == "":
		return DocBlank
	case strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "dokku "):
		return DocCommand
	case strings.HasPrefix(line, ">"):
		return DocNote
	case strings.HasPrefix(line, "    "):
		return DocCode
	default:
		return DocProse
	}
}

// ParseDocumentation classifies each line of rendered documentation
func ParseDocumentation(body string) []DocLine {
	if body == "" {
		return nil
	}

	lines := []DocLine{}
	for _, line := range strings.Split(body, "\n") {
		lines = append(lines, DocLine{Kind: classifyDocLine(line), Text: line})
	}

	return lines
}

// DocumentationBlocks groups rendered documentation into runs of a single kind.
// Blank lines are dropped, since a change of kind is what separates one block
// from the next.
func DocumentationBlocks(body string) []DocBlock {
	blocks := []DocBlock{}
	for _, line := range ParseDocumentation(body) {
		if line.Kind == DocBlank {
			continue
		}

		if len(blocks) == 0 || blocks[len(blocks)-1].Kind != line.Kind {
			blocks = append(blocks, DocBlock{Kind: line.Kind})
		}

		last := &blocks[len(blocks)-1]
		last.Lines = append(last.Lines, strings.TrimSpace(line.Text))
	}

	return blocks
}

// DocumentedFlag is a flag as the plugin documentation presents it
type DocumentedFlag struct {
	// Label is the flag as it is written on the command line
	Label string

	// Description is what the flag does
	Description string
}

// DocumentedFlags returns the flags a command accepts, leaving out the ones
// dokku consumes on the plugin's behalf. The flag set sorts by name, so the
// order is stable across runs.
func DocumentedFlags(c PluginCommand, data DocumentationData) ([]DocumentedFlag, error) {
	flags := []DocumentedFlag{}
	var err error
	c.FlagSet().VisitAll(func(f *flag.Flag) {
		if err != nil || globalFlags[f.Name] {
			return
		}

		label := "--" + f.Name
		if f.Shorthand != "" {
			label = "-" + f.Shorthand + "|" + label
		}

		valueType, usage := flag.UnquoteUsage(f)
		if valueType != "" {
			label = label + " <" + valueType + ">"
		}

		description, renderErr := RenderDocumentation(usage, data)
		if renderErr != nil {
			err = renderErr
			return
		}

		flags = append(flags, DocumentedFlag{Label: label, Description: description})
	})

	return flags, err
}

// DocumentedArguments returns the positional arguments a command accepts. The
// datastore type is left out, since the plugin supplies it rather than the user.
func DocumentedArguments(c PluginCommand) []DocumentedFlag {
	arguments := []DocumentedFlag{}
	for _, argument := range c.Arguments() {
		if argument.Name == "datastore-type" {
			continue
		}

		arguments = append(arguments, DocumentedFlag{
			Label:       argument.Name,
			Description: argument.Description,
		})
	}

	return arguments
}
