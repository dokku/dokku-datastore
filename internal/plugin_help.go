package internal

import (
	"os"
	"sort"
	"strings"
)

// maxListedArguments is how many arguments the one line per command listing
// shows before it gives up and elides the rest, so that the aligned table stays
// narrow enough to read
const maxListedArguments = 3

// helpColors are the escape sequences the plugin help is decorated with
type helpColors struct {
	blue      string
	bold      string
	cyan      string
	lightGray string
	lightRed  string
	normal    string
	otherGray string
}

// newHelpColors returns the escape sequences to decorate help output with,
// honouring the same environment the bash plugins did
func newHelpColors() helpColors {
	if os.Getenv("DOKKU_NO_COLOR") != "" || os.Getenv("NO_COLOR") != "" {
		return helpColors{}
	}

	if term := os.Getenv("TERM"); term == "" || term == "unknown" || term == "dumb" {
		return helpColors{}
	}

	return helpColors{
		blue:      "\033[0;34m",
		bold:      "\033[1m",
		cyan:      "\033[1;36m",
		lightGray: "\033[2;37m",
		lightRed:  "\033[1;31m",
		normal:    "\033[m",
		otherGray: "\033[7;37m",
	}
}

// paint wraps text in a color, leaving it alone when colors are off
func (c helpColors) paint(color string, text string) string {
	if color == "" {
		return text
	}

	return color + text + c.normal
}

// PluginHelpInput is the input for the plugin help functions
type PluginHelpInput struct {
	// Commands are the commands the plugin exposes as subcommands
	Commands []PluginCommand

	// Data is the datastore the help is rendered for
	Data DocumentationData
}

// PluginSummaryLine is the single entry the plugin contributes to dokku's
// top level command listing
func PluginSummaryLine(data DocumentationData) string {
	return "    " + data.CommandPrefix + ", Plugin for managing " + data.Title + " services"
}

// PluginCommandLines returns one comma delimited line per command, which is what
// dokku expects back from a plugin's commands script so that it can align the
// entries from every plugin together
func PluginCommandLines(input PluginHelpInput) ([]string, error) {
	colors := newHelpColors()

	lines := []string{}
	for _, c := range input.Commands {
		usage, err := RenderDocumentation(c.Usage(), input.Data)
		if err != nil {
			return nil, err
		}

		description, err := RenderDocumentation(c.Description(), input.Data)
		if err != nil {
			return nil, err
		}

		invocation := strings.TrimSpace(input.Data.CommandPrefix + ":" + c.Name() + " " + elideArguments(usage))
		lines = append(lines, "    "+invocation+","+colors.paint(colors.lightGray, description))
	}

	sort.Strings(lines)
	return lines, nil
}

// PluginHelpOverview is the help shown for the plugin as a whole, listing every
// command it implements
func PluginHelpOverview(input PluginHelpInput) (string, error) {
	colors := newHelpColors()
	data := input.Data

	lines, err := PluginCommandLines(input)
	if err != nil {
		return "", err
	}

	out := []string{
		colors.paint(colors.bold, "usage") + ": dokku " + data.CommandPrefix + "[:COMMAND]",
		"",
		colors.paint(colors.bold, "List your "+data.CommandPrefix+" services."),
		"",
		colors.paint(colors.blue, "Example:"),
		"",
		"    $ dokku " + data.CommandPrefix + ":list",
		"",
		"      " + data.Title + " services",
		"      service-name",
		"",
		"dokku " + colors.paint(colors.bold, data.CommandPrefix) + " commands: (get help with " +
			colors.paint(colors.cyan, "dokku "+data.CommandPrefix+":help SUBCOMMAND") + ")",
		"",
	}
	out = append(out, alignColumns(lines, ",")...)
	out = append(out, "")

	return strings.Join(out, "\n") + "\n", nil
}

// PluginCommandHelp is the help shown for a single command
func PluginCommandHelp(c PluginCommand, data DocumentationData) (string, error) {
	colors := newHelpColors()

	usage, err := RenderDocumentation(c.Usage(), data)
	if err != nil {
		return "", err
	}

	description, err := RenderDocumentation(c.Description(), data)
	if err != nil {
		return "", err
	}

	invocation := strings.TrimSpace("dokku " + data.CommandPrefix + ":" + c.Name() + " " + usage)
	out := []string{
		colors.paint(colors.bold, "usage:") + " " + invocation,
		"",
		colors.paint(colors.bold, description),
		"",
	}

	if arguments := DocumentedArguments(c); len(arguments) > 0 {
		out = append(out, colors.paint(colors.cyan, "arguments:"), "")
		out = append(out, alignColumns(entryLines(arguments, colors), ",")...)
		out = append(out, "")
	}

	flags, err := DocumentedFlags(c, data)
	if err != nil {
		return "", err
	}

	if len(flags) > 0 {
		out = append(out, colors.paint(colors.blue, "flags:"), "")
		out = append(out, alignColumns(entryLines(flags, colors), ",")...)
		out = append(out, "")
	}

	documentation, err := RenderDocumentation(c.Documentation(), data)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(documentation) != "" {
		out = append(out, colors.paint(colors.lightRed, "examples:"), "")
		out = append(out, renderHelpExamples(documentation, colors)...)
		out = append(out, "")
	}

	return strings.Join(out, "\n") + "\n", nil
}

// entryLines renders argument or flag entries as the comma delimited lines that
// alignColumns takes
func entryLines(entries []DocumentedFlag, colors helpColors) []string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, entry.Label+","+colors.paint(colors.lightGray, entry.Description))
	}

	return lines
}

// renderHelpExamples lays the long form documentation out for a terminal.
// Commands and literal blocks are indented, and a blank line separates prose
// from the commands that follow it.
func renderHelpExamples(documentation string, colors helpColors) []string {
	out := []string{}
	previousWasCommand := false
	first := true

	for _, line := range ParseDocumentation(documentation) {
		isCommand := line.Kind == DocCommand
		if !first && isCommand != previousWasCommand {
			out = append(out, "")
		}
		first = false
		previousWasCommand = isCommand

		switch line.Kind {
		case DocCommand:
			out = append(out, "    "+colors.paint(colors.lightGray, line.Text))
		case DocNote:
			out = append(out, "", "    "+colors.paint(colors.bold, line.Text))
		case DocCode:
			out = append(out, "    "+colors.paint(colors.otherGray, strings.TrimSpace(line.Text)))
		default:
			out = append(out, line.Text)
		}
	}

	return out
}

// elideArguments shortens an argument sketch to the first few arguments, so that
// the one line per command listing stays narrow
func elideArguments(usage string) string {
	arguments := splitArguments(usage)
	if len(arguments) <= maxListedArguments {
		return usage
	}

	return strings.Join(arguments[:maxListedArguments], " ") + "..."
}

// splitArguments splits an argument sketch on whitespace, treating a bracketed
// group as a single argument even when it contains a space
func splitArguments(usage string) []string {
	arguments := []string{}
	depth := 0
	current := strings.Builder{}

	for _, char := range usage {
		switch char {
		case '<', '[':
			depth++
		case '>', ']':
			depth--
		case ' ':
			if depth == 0 {
				if current.Len() > 0 {
					arguments = append(arguments, current.String())
					current.Reset()
				}
				continue
			}
		}

		current.WriteRune(char)
	}

	if current.Len() > 0 {
		arguments = append(arguments, current.String())
	}

	return arguments
}

// alignColumns pads the first field of every line so that the second field lines
// up, the way column -t would. Escape sequences are not counted, so the columns
// still line up when the output is colored.
func alignColumns(lines []string, separator string) []string {
	width := 0
	for _, line := range lines {
		head, _, found := strings.Cut(line, separator)
		if !found {
			continue
		}

		if len(head) > width {
			width = len(head)
		}
	}

	aligned := make([]string, 0, len(lines))
	for _, line := range lines {
		head, tail, found := strings.Cut(line, separator)
		if !found {
			aligned = append(aligned, line)
			continue
		}

		aligned = append(aligned, head+strings.Repeat(" ", width-len(head)+2)+tail)
	}

	return aligned
}
