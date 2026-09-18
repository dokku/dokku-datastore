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
