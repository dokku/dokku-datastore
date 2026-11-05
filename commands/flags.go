package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"
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

// AmbassadorImageFlagCommand is the flag a command that may make a service's
// ambassador takes to name the image it is made from
type AmbassadorImageFlagCommand struct {
	// ambassadorImage is the image the ambassador is made from, empty to leave
	// it to the variable, the global property and then the pinned default
	ambassadorImage string
}

// AmbassadorImageFlags adds the ambassador image flag to the flag set
func (c *AmbassadorImageFlagCommand) AmbassadorImageFlags(f *flag.FlagSet) {
	f.StringVar(&c.ambassadorImage, "ambassador-image", "", fmt.Sprintf("the image to make the ambassador from, with a tag or digest, overriding the %s global property", service.AmbassadorImageProperty))
}

// AutocompleteAmbassadorImageFlags returns the autocomplete ambassador image
// flags
func (c *AmbassadorImageFlagCommand) AutocompleteAmbassadorImageFlags() complete.Flags {
	return complete.Flags{
		"--ambassador-image": complete.PredictAnything,
	}
}

// ApplyAmbassadorImage hands a given ambassador image on as
// PLUGIN_AMBASSADOR_IMAGE, which is checked before the global property. That
// is how the flag wins over both without being passed down to wherever an
// ambassador is made, and how a trigger the command fires sees it too. It is
// refused before the command changes anything when it names no tag or digest.
func (c *AmbassadorImageFlagCommand) ApplyAmbassadorImage() error {
	image := strings.TrimSpace(c.ambassadorImage)
	if image == "" {
		return nil
	}

	if err := service.ValidateAmbassadorImage(image); err != nil {
		return fmt.Errorf("invalid --ambassador-image flag: %w", err)
	}

	return os.Setenv(hostenv.AmbassadorImageVariable, image)
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

// changedLabels is changedSlice for the --label flag, read into labels, so that
// --label "" asks for no labels at all, which is how a clone drops the ones its
// source has.
func changedLabels(f *flag.FlagSet, name string, value []string) (*map[string]string, error) {
	if !f.Changed(name) {
		return nil, nil
	}

	labels, err := service.ParseLabels(value)
	if err != nil {
		return nil, err
	}

	return &labels, nil
}
