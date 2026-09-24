package commands

import (
	"os"

	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

// GlobalFlagCommand is the global flag command
type GlobalFlagCommand struct {
	// quiet is whether to suppress output
	quiet bool
	// format is the format to output the data in
	format string
	// trace is whether to enable trace output
	trace bool
}

// GlobalFlags adds the global flags to the flag set. Dokku consumes its own
// global flags before dispatching to a plugin and forwards them on as
// environment variables, so those are the defaults here.
func (c *GlobalFlagCommand) GlobalFlags(f *flag.FlagSet) {
	f.BoolVar(&c.quiet, "quiet", os.Getenv("DOKKU_QUIET_OUTPUT") != "", "suppress output")
	// one of json, table
	f.StringVar(&c.format, "format", "text", "the format to output the data in")
	f.BoolVar(&c.trace, "trace", os.Getenv("DOKKU_TRACE") != "", "enable trace output")
}

// ReportFormat maps the format flag onto what the report helper accepts. The
// flag's own vocabulary is text or json, the helper's is stdout or json.
func (c *GlobalFlagCommand) ReportFormat() string {
	if c.format == "json" {
		return "json"
	}

	return "stdout"
}

// AutocompleteGlobalFlags returns the autocomplete global flags
func (c *GlobalFlagCommand) AutocompleteGlobalFlags() complete.Flags {
	return complete.Flags{
		"--quiet":  complete.PredictNothing,
		"--format": complete.PredictSet("json", "text"),
		"--trace":  complete.PredictNothing,
	}
}

// changedString is a flag's value when it was given and nil when it was not.
//
// A command that changes only some of a service's settings needs to tell "leave
// this alone" apart from "set this to nothing", and an empty string says both.
func changedString(f *flag.FlagSet, name string, value string) *string {
	if !f.Changed(name) {
		return nil
	}

	return &value
}

// changedSlice is changedString for a list flag.
func changedSlice(f *flag.FlagSet, name string, value []string) *[]string {
	if !f.Changed(name) {
		return nil
	}

	return &value
}

// changedInt is changedString for a number flag, where zero is a value a flag
// can be given rather than a sign it was not.
func changedInt(f *flag.FlagSet, name string, value int) *int {
	if !f.Changed(name) {
		return nil
	}

	return &value
}
