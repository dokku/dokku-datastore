package service

import (
	"fmt"
	"strings"

	"github.com/dokku/dokku-datastore/internal/verb"
	"github.com/dokku/dokku/plugins/common"
	"mvdan.cc/sh/v3/shell"
	"mvdan.cc/sh/v3/syntax"
)

// ExportArgsProperty is the arguments every export of a service is run with,
// appended to the datastore's own. It is what makes a scheduled backup and a
// clone dump the service the way an operator asked for, since neither is ever
// run by hand to be given them.
const ExportArgsProperty = "export-args"

// ImportArgsProperty is the arguments every import into a service is run with,
// appended to the datastore's own, a clone's import included.
const ImportArgsProperty = "import-args"

// extraArgsProperties are the property each verb reads its extra arguments from
var extraArgsProperties = map[string]string{
	"export": ExportArgsProperty,
	"import": ImportArgsProperty,
}

// ExtraArgsVerb is the verb an extra arguments property is read by, empty for a
// property that is not one.
func ExtraArgsVerb(property string) string {
	for verbName, candidate := range extraArgsProperties {
		if candidate == property {
			return verbName
		}
	}

	return ""
}

// ValidateExtraArgs reports whether a value is usable as extra arguments. An
// empty value is valid and means unset.
//
// The value is split the way a shell would split it, without expanding
// anything, so that an argument with a space in it can be quoted.
func ValidateExtraArgs(property string, value string) error {
	if _, err := parseExtraArgs(value); err != nil {
		return fmt.Errorf("invalid %s value %q: %w", property, value, err)
	}

	return nil
}

// ServiceExtraArgs is the extra arguments property a service was given, as it
// was written, empty when it was given none.
func ServiceExtraArgs(s *Datastore, serviceName string, property string) string {
	return strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, property))
}

// ExtraArgs is the extra arguments property a service was given, split into
// the arguments it names.
func ExtraArgs(s *Datastore, serviceName string, property string) ([]string, error) {
	args, err := parseExtraArgs(ServiceExtraArgs(s, serviceName, property))
	if err != nil {
		return nil, fmt.Errorf("unable to read the %s property: %w", property, err)
	}

	return args, nil
}

// AcceptsExtraArgs reports whether the datastore's command for a verb passes
// extra arguments on to the tool it runs.
func (s *Datastore) AcceptsExtraArgs(verbName string) bool {
	command, ok := s.Definition.CommandFor(verbName)
	return ok && command.ExtraArgs
}

// extraArgs is what a verb is run with: the arguments given for this run, or
// failing that the ones the service keeps for the verb.
//
// Refused up front for a command that would ignore them, rather than once the
// command is resolved, so that nothing is pulled or paused for a run that can
// only fail.
func (s *Datastore) extraArgs(serviceName string, verbName string, given []string) ([]string, error) {
	args := given
	if property, ok := extraArgsProperties[verbName]; ok && len(args) == 0 {
		var err error
		args, err = ExtraArgs(s, serviceName, property)
		if err != nil {
			return nil, err
		}
	}

	if len(args) > 0 && !s.AcceptsExtraArgs(verbName) {
		return nil, verb.ErrExtraArgsRefused{
			Plugin: s.Definition.Dokku.Plugin,
			Name:   verbName,
		}
	}

	return args, nil
}

// parseExtraArgs splits a value into arguments the way a shell would.
//
// Nothing is expanded, and a variable is refused rather than dropped: it would
// otherwise split to nothing, and a --where naming one would dump rows nobody
// asked for. Single quotes keep a literal $.
func parseExtraArgs(value string) ([]string, error) {
	value = strings.TrimSpace(value)

	// found in the syntax rather than by watching what the split looks up,
	// since the split looks up variables of its own such as IFS
	parsed, err := syntax.NewParser().Parse(strings.NewReader(value), "")
	if err != nil {
		return nil, err
	}

	expansion := ""
	syntax.Walk(parsed, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.ParamExp:
			expansion = "$" + node.Param.Value
		case *syntax.ArithmExp:
			expansion = "$((...))"
		}
		return expansion == ""
	})

	if expansion != "" {
		return nil, fmt.Errorf("%s is not expanded, single quote a literal $", expansion)
	}

	return shell.Fields(value, func(name string) string {
		return ""
	})
}
