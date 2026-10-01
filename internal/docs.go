package internal

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/dokku/dokku-datastore/internal/definition"
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
	AltAlias       string
	CommandPrefix  string
	DefaultAlias   string
	Image          string
	ImageVersion   string
	PluginVariable string
	Port           string
	PortList       string
	Scheme         string
	Title          string

	// ExportArgs and ImportArgs are whether the datastore passes extra
	// arguments on to its export and import tools, so that a datastore that
	// refuses them is never documented as taking them
	ExportArgs bool
	ImportArgs bool

	// ReservedNames are the service names create and clone refuse, because the
	// database a service is named after would be one the datastore keeps
	ReservedNames []string

	// Sections are the readme sections the datastore's definitions add, which
	// belong to no command
	Sections []definition.DocumentationSection

	// Flavors are the images other than the datastore's own that it ships
	// definitions for, such as pgvector/pgvector for postgres, each at the
	// version its newest definition pins
	Flavors []DocumentedImage

	// Definitions are the names of the datastore's definitions, oldest first,
	// and empty for a datastore with only the one, where there is nothing to
	// choose between
	Definitions []string

	// Volumes are the volumes a service can move, one entry for each volume of
	// each of the datastore's definitions, since a datastore split by major
	// version mounts its data somewhere different in each. Empty for a
	// datastore that mounts nothing.
	Volumes []DocumentedVolume

	// VolumeKey is the volume the examples move: the data volume where there
	// is one, and otherwise the first the datastore mounts
	VolumeKey string

	// VolumeKeys are the volumes a service of the datastore can move, each
	// once, whichever of its definitions mounts it
	VolumeKeys []string

	// Migrates is whether an upgrade onto one of the datastore's definitions
	// from another carries the data across, which is what the upgrade has to
	// be told to stop the linked apps for
	Migrates bool
}

// DocumentedImage is an image and the version a definition pins for it.
type DocumentedImage struct {
	Image        string
	ImageVersion string
}

// DocumentedVolume is a volume a definition mounts and where it mounts it.
type DocumentedVolume struct {
	Definition string
	Key        string
	Target     string
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
	volumes := documentedVolumes(input.Datastore)

	return DocumentationData{
		AltAlias:       properties.AltAlias,
		CommandPrefix:  properties.CommandPrefix,
		DefaultAlias:   properties.DefaultAlias,
		Image:          image,
		ImageVersion:   imageVersion,
		PluginVariable: properties.PluginVariable,
		Port:           port,
		PortList:       strings.Join(ports, " "),
		Scheme:         properties.Scheme,
		Title:          input.Datastore.Title(),
		ExportArgs:     input.Datastore.AcceptsExtraArgs("export"),
		ImportArgs:     input.Datastore.AcceptsExtraArgs("import"),
		ReservedNames:  input.Datastore.Definition.Dokku.ReservedNames,
		Sections:       input.Datastore.Documentation(),
		Flavors:        documentedFlavors(input.Datastore),
		Definitions:    documentedDefinitions(input.Datastore),
		Volumes:        volumes,
		VolumeKey:      documentedVolumeKey(input.Datastore),
		VolumeKeys:     documentedVolumeKeys(volumes),
		Migrates:       documentedMigrates(input.Datastore),
	}
}

// documentedMigrates is whether any of a datastore's definitions migrates the
// data of a service moved onto it.
func documentedMigrates(datastore *service.Datastore) bool {
	for _, found := range datastore.Definitions() {
		if found.Dokku.Upgrade.Migrate {
			return true
		}
	}

	return false
}

// documentedVolumeKeys is each volume named once, in the order they are first
// seen
func documentedVolumeKeys(volumes []DocumentedVolume) []string {
	keys := []string{}
	for _, volume := range volumes {
		if !slices.Contains(keys, volume.Key) {
			keys = append(keys, volume.Key)
		}
	}

	return keys
}

// documentedVolumes is every volume of every definition of a datastore, in the
// order its definitions are kept and each declares its volumes.
func documentedVolumes(datastore *service.Datastore) []DocumentedVolume {
	volumes := []DocumentedVolume{}
	for _, found := range datastore.Definitions() {
		for _, volume := range found.Service.Volumes {
			volumes = append(volumes, DocumentedVolume{
				Definition: found.Name,
				Key:        definition.VolumeKey(volume),
				Target:     volume.Target,
			})
		}
	}

	return volumes
}

// documentedVolumeKey is the volume the examples move, which is the data one
// wherever a datastore has one since that is what moving is usually for.
func documentedVolumeKey(datastore *service.Datastore) string {
	keys := datastore.Definition.VolumeKeys()
	if slices.Contains(keys, "data") {
		return "data"
	}

	if len(keys) > 0 {
		return keys[0]
	}

	return ""
}

// documentedFlavors is every image a datastore ships definitions for besides
// its own, in the order its definitions are kept, at the version the newest of
// each pins. Definitions come oldest first, so the last one seen for an image
// is the one a create naming only that image lands on.
func documentedFlavors(datastore *service.Datastore) []DocumentedImage {
	own := datastore.Definition.DefaultImage
	flavors := []DocumentedImage{}
	seen := map[string]int{}
	for _, found := range datastore.Definitions() {
		if found.DefaultImage == own {
			continue
		}

		documented := DocumentedImage{Image: found.DefaultImage, ImageVersion: found.DefaultImageVersion}
		if index, ok := seen[found.DefaultImage]; ok {
			flavors[index] = documented
			continue
		}

		seen[found.DefaultImage] = len(flavors)
		flavors = append(flavors, documented)
	}

	return flavors
}

// documentedDefinitions is every definition a service of the datastore can be
// placed on by name, or nothing when there is only the one.
func documentedDefinitions(datastore *service.Datastore) []string {
	found := datastore.Definitions()
	if len(found) < 2 {
		return nil
	}

	names := make([]string, 0, len(found))
	for _, one := range found {
		names = append(names, one.Name)
	}

	return names
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
