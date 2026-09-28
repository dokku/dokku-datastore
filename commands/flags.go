package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/mitchellh/cli"
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

// Logger applies the global flags once they are parsed, and returns the ui the
// command reports through.
//
// Trace and quiet are handed on the way dokku hands them to a plugin, as
// DOKKU_TRACE and DOKKU_QUIET_OUTPUT, which is what the dokku helpers this binary
// calls read and what every trigger and dokku command it runs inherits. And json
// keeps stdout for the document, so what a child process streams goes to stderr.
func (c *GlobalFlagCommand) Logger(ui cli.Ui) internal.Ui {
	setEnvFlag("DOKKU_TRACE", c.trace)
	setEnvFlag("DOKKU_QUIET_OUTPUT", c.quiet)
	execx.StreamStdoutToStderr(c.format == "json")

	return internal.Ui{
		Ui:     ui,
		Format: c.format,
		Quiet:  c.quiet,
		Trace:  c.trace,
	}
}

// errTextOnly refuses --format json on a command whose output is text that
// something else reads as it is.
func errTextOnly(name string) error {
	return fmt.Errorf("%s only prints text, and does not take --format json", name)
}

// setEnvFlag exports a boolean the way dokku does, as 1, and removes it when the
// flag was turned off, since dokku's helpers read any value as set.
func setEnvFlag(name string, enabled bool) {
	if enabled {
		os.Setenv(name, "1") //nolint:errcheck
		return
	}

	os.Unsetenv(name) //nolint:errcheck
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

// changedMounts is changedSlice for the --volume flag, read into mounts, so
// that --volume "" asks for no mounts at all, which is how a clone drops the
// ones its source has.
func changedMounts(f *flag.FlagSet, name string, value []string) (*[]service.Mount, error) {
	if !f.Changed(name) {
		return nil, nil
	}

	mounts, err := internal.ParseMountSpecs(value)
	if err != nil {
		return nil, err
	}

	return &mounts, nil
}

// changedVolumeTargets is changedSlice for the --volume-target flag, read into
// targets, so that --volume-target "" asks for every volume where the
// definition puts it, which is how a clone or an upgrade drops the ones the
// service moved.
func changedVolumeTargets(f *flag.FlagSet, name string, value []string) (*map[string]string, error) {
	if !f.Changed(name) {
		return nil, nil
	}

	targets, err := service.ParseVolumeTargets(strings.Join(value, " "))
	if err != nil {
		return nil, err
	}

	return &targets, nil
}
