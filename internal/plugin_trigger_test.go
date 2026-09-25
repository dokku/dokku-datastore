package internal

import (
	"strings"
	"testing"
)

// The file dokku finds is a dispatcher and nothing else: everything the trigger
// does lives in the definition, so the file never has to be regenerated for a
// change in behaviour, only for a change in which triggers exist.
func TestPluginTriggerDispatchesThroughTheBinary(t *testing.T) {
	contents := PluginTrigger("post-extract")

	for _, expected := range []string{
		`dokku-datastore" trigger post-extract "$PLUGIN_COMMAND_PREFIX" "$@"`,
		"#!/usr/bin/env bash",
		"do not edit",
	} {
		if !strings.Contains(contents, expected) {
			t.Errorf("expected %q in the generated trigger, got:\n%s", expected, contents)
		}
	}

	// dokku runs the file it finds with the arguments it has, so the script
	// must not consume any of them itself
	if strings.Contains(contents, "shift") {
		t.Error("expected the trigger to pass its arguments through untouched")
	}
}

// A trigger the binary implements itself dispatches to its own command rather
// than through the definition.
func TestPluginBuiltinTriggerDispatchesToItsCommand(t *testing.T) {
	for _, name := range BuiltinTriggers {
		contents := PluginBuiltinTrigger(name)

		expected := `dokku-datastore" trigger-` + name + ` "$PLUGIN_COMMAND_PREFIX" "$@"`
		if !strings.Contains(contents, expected) {
			t.Errorf("expected %q in the generated %s trigger, got:\n%s", expected, name, contents)
		}

		if !strings.Contains(contents, "do not edit") {
			t.Errorf("expected the generated %s trigger to say it is generated", name)
		}

		if strings.Contains(contents, "shift") {
			t.Errorf("expected the %s trigger to pass its arguments through untouched", name)
		}
	}
}

func TestCheckTriggerNames(t *testing.T) {
	if err := CheckTriggerNames([]string{"post-extract"}); err != nil {
		t.Errorf("expected post-extract to be allowed: %s", err)
	}

	// both would be written to a file called pre-start
	if err := CheckTriggerNames([]string{"post-extract", "pre-start"}); err == nil {
		t.Error("expected a definition trigger named pre-start to be refused")
	}
}
