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

// AutocompleteGlobalFlags returns the autocomplete global flags
func (c *GlobalFlagCommand) AutocompleteGlobalFlags() complete.Flags {
	return complete.Flags{
		"--quiet":  complete.PredictNothing,
		"--format": complete.PredictSet("json", "text"),
		"--trace":  complete.PredictNothing,
	}
}
