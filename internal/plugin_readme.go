package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// DokkuVersion is the dokku version the generated readme tells people to install on
const DokkuVersion = "0.19.x+"

// readmeSections are the readme usage sections, in the order they are written
// out, along with the prose that introduces each one
var readmeSections = []struct {
	Group string
	Intro []string
}{
	{Group: GroupBasicUsage},
	{
		Group: GroupServiceLifecycle,
		Intro: []string{"The lifecycle of each service can be managed through the following commands:"},
	},
	{
		Group: GroupServiceAutomation,
		Intro: []string{"Service scripting can be executed using the following commands:"},
	},
	{
		Group: GroupDataManagement,
		Intro: []string{"The underlying service data can be imported and exported with the following commands:"},
	},
	{
		Group: GroupBackups,
		Intro: []string{
			"Datastore backups are supported via AWS S3 and S3 compatible services like [minio](https://github.com/minio/minio).",
			"You may skip the `backup-auth` step if your dokku install is running within EC2 and has access to the bucket via an IAM profile. In that case, use the `--use-iam` option with the `backup` command.",
			"If both passphrase and public key forms of encryption are set, the public key encryption will take precedence.",
			"The underlying core backup script is present [here](https://github.com/dokku/docker-s3backup/blob/main/backup.sh).",
			"Backups can be performed using the backup commands:",
		},
	},
}

// maxCommandColumn is how wide the command column in the readme command list
// grows before the descriptions are left to run on
const maxCommandColumn = 50

// ReadmeInput is the input for the Readme function
type ReadmeInput struct {
	// Commands are the commands the plugin exposes as subcommands
	Commands []PluginCommand

	// Data is the datastore the readme is rendered for
	Data DocumentationData

	// PluginDir is the plugin checkout the readme is generated for
	PluginDir string

	// Sponsors are the github accounts that sponsored the plugin
	Sponsors []string
}

// Readme renders the plugin's readme
func Readme(input ReadmeInput) (string, error) {
	sections := []string{
		readmeHeader(input.Data),
		readmeDescription(input.Data),
	}

	if len(input.Sponsors) > 0 {
		sections = append(sections, readmeSponsors(input.Data, input.Sponsors))
	}

	sections = append(sections,
		readmeRequirements(),
		readmeInstallation(input.Data),
	)

	commandList, err := readmeCommandList(input)
	if err != nil {
		return "", err
	}
	sections = append(sections, commandList)

	usage, err := readmeUsage(input)
	if err != nil {
		return "", err
	}
	sections = append(sections, usage...)

	return strings.Join(sections, "\n\n") + "\n", nil
}

// readmeHeader is the title line, with the badges that follow it
func readmeHeader(data DocumentationData) string {
	prefix := data.CommandPrefix
	return strings.Join([]string{
		fmt.Sprintf("# dokku %s", prefix),
		fmt.Sprintf(`[![Build Status](https://img.shields.io/github/actions/workflow/status/dokku/dokku-%s/ci.yml?branch=master&style=flat-square "Build Status")](https://github.com/dokku/dokku-%s/actions/workflows/ci.yml?query=branch%%3Amaster)`, prefix, prefix),
		`[![IRC Network](https://img.shields.io/badge/irc-libera-blue.svg?style=flat-square "IRC Libera")](https://webchat.libera.chat/?channels=dokku)`,
	}, " ")
}

// readmeDescription says which image the plugin installs by default
func readmeDescription(data DocumentationData) string {
	base := "_"
	image := data.Image
	if namespace, name, found := strings.Cut(data.Image, "/"); found {
		base = "r/" + namespace
		image = name
	}

	return fmt.Sprintf("Official %s plugin for dokku. Currently defaults to installing [%s %s](https://hub.docker.com/%s/%s/).",
		data.CommandPrefix, data.Image, data.ImageVersion, base, image)
}

// readmeSponsors credits the people who paid for the plugin to be written
func readmeSponsors(data DocumentationData, sponsors []string) string {
	lines := []string{
		"## Sponsors",
		"",
		fmt.Sprintf("The %s plugin was generously sponsored by the following:", data.CommandPrefix),
		"",
	}

	for _, sponsor := range sponsors {
		lines = append(lines, fmt.Sprintf("- [%s](https://github.com/%s)", sponsor, sponsor))
	}

	return strings.Join(lines, "\n")
}

// readmeRequirements lists what the plugin needs to run
func readmeRequirements() string {
	return strings.Join([]string{
		"## Requirements",
		"",
		"- dokku " + DokkuVersion,
		"- docker 1.8.x",
	}, "\n")
}

// readmeInstallation is the command that installs the plugin
func readmeInstallation(data DocumentationData) string {
	return strings.Join([]string{
		"## Installation",
		"",
		"```shell",
		"# on " + DokkuVersion,
		fmt.Sprintf("sudo dokku plugin:install https://github.com/dokku/dokku-%s.git --name %s", data.CommandPrefix, data.CommandPrefix),
		"```",
	}, "\n")
}

// readmeCommandList is the block listing every command the plugin implements
func readmeCommandList(input ReadmeInput) (string, error) {
	commands := sortedCommands(input.Commands)

	invocations := make([]string, 0, len(commands))
	descriptions := make([]string, 0, len(commands))
	width := 0
	for _, c := range commands {
		usage, err := RenderDocumentation(c.Usage(), input.Data)
		if err != nil {
			return "", err
		}

		description, err := RenderDocumentation(c.Description(), input.Data)
		if err != nil {
			return "", err
		}

		invocation := input.Data.CommandPrefix + ":" + c.Name() + " " + usage
		if len(invocation) > width {
			width = len(invocation)
		}

		invocations = append(invocations, invocation)
		descriptions = append(descriptions, description)
	}

	if width > maxCommandColumn {
		width = maxCommandColumn
	}

	lines := []string{"## Commands", "", "```"}
	for i, invocation := range invocations {
		padding := ""
		if width > len(invocation) {
			padding = strings.Repeat(" ", width-len(invocation))
		}

		lines = append(lines, invocation+padding+" # "+descriptions[i])
	}
	lines = append(lines, "```")

	return strings.Join(lines, "\n"), nil
}

// readmeUsage is the per command documentation, grouped into sections
func readmeUsage(input ReadmeInput) ([]string, error) {
	sections := []string{
		"## Usage",
		fmt.Sprintf("Help for any commands can be displayed by specifying the command as an argument to %s:help. "+
			"Plugin help output in conjunction with any files in the `docs/` folder is used to generate the plugin documentation. "+
			"Please consult the `%s:help` command for any undocumented commands.", input.Data.CommandPrefix, input.Data.CommandPrefix),
	}

	for _, section := range readmeSections {
		commands := commandsInGroup(input.Commands, section.Group)
		if len(commands) == 0 {
			continue
		}

		sections = append(sections, "### "+section.Group)
		sections = append(sections, section.Intro...)

		for _, c := range commands {
			documentation, err := readmeCommand(c, input)
			if err != nil {
				return nil, err
			}

			sections = append(sections, documentation...)
		}
	}

	sections = append(sections, readmeDockerPull(input.Data)...)
	return sections, nil
}

// readmeCommand is the documentation for a single command
func readmeCommand(c PluginCommand, input ReadmeInput) ([]string, error) {
	usage, err := RenderDocumentation(c.Usage(), input.Data)
	if err != nil {
		return nil, err
	}

	description, err := RenderDocumentation(c.Description(), input.Data)
	if err != nil {
		return nil, err
	}

	invocation := strings.TrimSpace(fmt.Sprintf("dokku %s:%s %s", input.Data.CommandPrefix, c.Name(), usage))
	blocks := []string{
		"### " + description,
		strings.Join([]string{"```shell", "# usage", invocation, "```"}, "\n"),
	}

	flags, err := DocumentedFlags(c, input.Data)
	if err != nil {
		return nil, err
	}

	if len(flags) > 0 {
		lines := []string{"flags:", ""}
		for _, f := range flags {
			lines = append(lines, fmt.Sprintf("- `%s`: %s", f.Label, f.Description))
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}

	documentation, err := RenderDocumentation(c.Documentation(), input.Data)
	if err != nil {
		return nil, err
	}

	blocks = append(blocks, readmeDocumentationBlocks(documentation)...)

	extra, err := readmeExtraDocumentation(input.PluginDir, c.Name())
	if err != nil {
		return nil, err
	}

	if extra != "" {
		blocks = append(blocks, extra)
	}

	return blocks, nil
}

// readmeDocumentationBlocks renders the long form documentation as markdown
func readmeDocumentationBlocks(documentation string) []string {
	blocks := []string{}
	for _, block := range DocumentationBlocks(documentation) {
		switch block.Kind {
		case DocCommand:
			blocks = append(blocks, "```shell\n"+strings.Join(block.Lines, "\n")+"\n```")
		case DocCode:
			blocks = append(blocks, "```\n"+strings.Join(block.Lines, "\n")+"\n```")
		case DocNote:
			blocks = append(blocks, strings.Join(block.Lines, "\n"))
		default:
			blocks = append(blocks, processSentence(block.Lines))
		}
	}

	return blocks
}

// readmeExtraDocumentation is the plugin's escape hatch for prose that only
// makes sense for one datastore, kept in a file named after the command
func readmeExtraDocumentation(pluginDir string, name string) (string, error) {
	if pluginDir == "" {
		return "", nil
	}

	contents, err := os.ReadFile(filepath.Join(pluginDir, "docs", name+".md"))
	if os.IsNotExist(err) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	return strings.TrimRight(string(contents), "\n"), nil
}

// readmeDockerPull explains how to stop the plugin pulling images
func readmeDockerPull(data DocumentationData) []string {
	return []string{
		"### Disabling `docker image pull` calls",
		fmt.Sprintf("If you wish to disable the `docker image pull` calls that the plugin triggers, you may set the `%s_DISABLE_PULL` "+
			"environment variable to `true`. Once disabled, you will need to pull the service image you wish to deploy as shown in the "+
			"`stderr` output.", strings.ToUpper(data.CommandPrefix)),
		"Please ensure the proper images are in place when `docker image pull` is disabled.",
	}
}

// sortedCommands returns the commands in the order the readme lists them
func sortedCommands(commands []PluginCommand) []PluginCommand {
	sorted := make([]PluginCommand, len(commands))
	copy(sorted, commands)
	sort.Slice(sorted, func(i int, j int) bool {
		return sorted[i].Name() < sorted[j].Name()
	})

	return sorted
}

// commandsInGroup returns the commands a readme usage section documents
func commandsInGroup(commands []PluginCommand, group string) []PluginCommand {
	matching := []PluginCommand{}
	for _, c := range commands {
		if c.Group() == group {
			matching = append(matching, c)
		}
	}

	return sortedCommands(matching)
}

// processSentence turns a paragraph of the terminal oriented documentation into
// markdown prose: sentences are capitalized, acronyms and variable names are
// quoted, and the single quotes the annotations used become backticks.
func processSentence(lines []string) string {
	text := strings.Join(lines, " ")

	pieces := strings.Split(text, ". ")
	for i, piece := range pieces {
		pieces[i] = upperFirst(strings.TrimSpace(piece))
	}

	text = strings.TrimSpace(strings.Join(pieces, ". "))
	if !strings.HasSuffix(text, ".") && !strings.HasSuffix(text, ":") {
		text += ":"
	}

	pieces = strings.Split(text, ". ")
	for i, piece := range pieces {
		words := strings.Split(strings.TrimSpace(piece), " ")
		for j, word := range words {
			words[j] = quoteAcronym(word)
		}
		pieces[i] = strings.Join(words, " ")
	}

	text = strings.Join(pieces, ". ")
	text = strings.ReplaceAll(text, "(0.0.0.0)", "(`0.0.0.0`)")
	text = strings.ReplaceAll(text, "'", "`")
	text = strings.ReplaceAll(text, "`s", "'s")
	text = strings.ReplaceAll(text, "``", "`")

	return strings.TrimSpace(text)
}

// upperFirst capitalizes the first letter of a sentence
func upperFirst(text string) string {
	for i, char := range text {
		return string(unicode.ToUpper(char)) + text[i+len(string(char)):]
	}

	return text
}

// quoteAcronym wraps an all caps word in backticks, so that the acronyms and
// environment variable names the documentation mentions read as code
func quoteAcronym(word string) string {
	if len(word) <= 1 || !isUpper(word) {
		return word
	}

	for _, ending := range []string{":", "."} {
		if strings.HasSuffix(word, ending) {
			return "`" + strings.TrimSuffix(word, ending) + "`" + ending
		}
	}

	return "`" + word + "`"
}

// isUpper reports whether a word has at least one letter and no lowercase ones
func isUpper(word string) bool {
	cased := false
	for _, char := range word {
		if unicode.IsLower(char) {
			return false
		}

		if unicode.IsUpper(char) {
			cased = true
		}
	}

	return cased
}
