package commands

import (
	"github.com/posener/complete"
	flag "github.com/spf13/pflag"
)

type GlobalFlagCommand struct {
	quiet  bool
	format string
}

func (c *GlobalFlagCommand) GlobalFlags(f *flag.FlagSet) {
	f.BoolVar(&c.quiet, "quiet", false, "suppress output")
	// one of json, table
	f.StringVar(&c.format, "format", "text", "the format to output the data in")
}

func (c *GlobalFlagCommand) AutocompleteGlobalFlags() complete.Flags {
	return complete.Flags{
		"--quiet":  complete.PredictNothing,
		"--format": complete.PredictSet("json", "text"),
	}
}
