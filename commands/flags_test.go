package commands

import (
	"os"
	"reflect"
	"testing"

	"github.com/mitchellh/cli"
	flag "github.com/spf13/pflag"
)

func TestGlobalFlagsDefaults(t *testing.T) {
	tests := []struct {
		name          string
		quietEnv      string
		traceEnv      string
		args          []string
		expectedQuiet bool
		expectedTrace bool
	}{
		{
			name: "no environment and no flags",
		},
		{
			name:          "dokku forwarded quiet",
			quietEnv:      "1",
			expectedQuiet: true,
		},
		{
			name:          "dokku forwarded trace",
			traceEnv:      "1",
			expectedTrace: true,
		},
		{
			name:          "both forwarded",
			quietEnv:      "1",
			traceEnv:      "1",
			expectedQuiet: true,
			expectedTrace: true,
		},
		{
			name:          "flags still work without the environment",
			args:          []string{"--quiet", "--trace"},
			expectedQuiet: true,
			expectedTrace: true,
		},
		{
			name:          "an explicit false overrides the environment",
			quietEnv:      "1",
			args:          []string{"--quiet=false"},
			expectedQuiet: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DOKKU_QUIET_OUTPUT", test.quietEnv)
			t.Setenv("DOKKU_TRACE", test.traceEnv)

			c := &GlobalFlagCommand{}
			f := flag.NewFlagSet("test", flag.ContinueOnError)
			c.GlobalFlags(f)
			if err := f.Parse(test.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			if c.quiet != test.expectedQuiet {
				t.Errorf("expected quiet %t, got %t", test.expectedQuiet, c.quiet)
			}
			if c.trace != test.expectedTrace {
				t.Errorf("expected trace %t, got %t", test.expectedTrace, c.trace)
			}
		})
	}
}

// The flags are handed on as the environment dokku would have set, so the dokku
// helpers this binary calls and every trigger it runs see them too.
func TestLoggerAppliesTheGlobalFlags(t *testing.T) {
	tests := []struct {
		name     string
		quietEnv string
		traceEnv string
		args     []string
		quiet    string
		trace    string
	}{
		{name: "nothing", quiet: "", trace: ""},
		{name: "the flags", args: []string{"--quiet", "--trace"}, quiet: "1", trace: "1"},
		// dokku's own checks only count 1
		{name: "any value dokku forwarded", quietEnv: "true", traceEnv: "true", quiet: "1", trace: "1"},
		{name: "turned off", quietEnv: "1", traceEnv: "1", args: []string{"--quiet=false", "--trace=false"}, quiet: "", trace: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DOKKU_QUIET_OUTPUT", test.quietEnv)
			t.Setenv("DOKKU_TRACE", test.traceEnv)

			c := &GlobalFlagCommand{}
			f := flag.NewFlagSet("test", flag.ContinueOnError)
			c.GlobalFlags(f)
			if err := f.Parse(test.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			logger := c.Logger(cli.NewMockUi())
			if logger.Quiet != c.quiet || logger.Format != c.format {
				t.Errorf("expected the ui to carry the flags, got %+v", logger)
			}

			if actual := os.Getenv("DOKKU_QUIET_OUTPUT"); actual != test.quiet {
				t.Errorf("expected DOKKU_QUIET_OUTPUT %q, got %q", test.quiet, actual)
			}
			if actual := os.Getenv("DOKKU_TRACE"); actual != test.trace {
				t.Errorf("expected DOKKU_TRACE %q, got %q", test.trace, actual)
			}
		})
	}
}

func TestAskedForJSON(t *testing.T) {
	tests := []struct {
		args     []string
		expected bool
	}{
		{args: []string{"redis", "help"}},
		{args: []string{"redis", "redis:help", "--format", "json"}, expected: true},
		{args: []string{"redis", "--format=json"}, expected: true},
		{args: []string{"redis", "--format", "text"}},
		{args: []string{"redis", "--format"}},
	}

	for _, test := range tests {
		if actual := askedForJSON(test.args); actual != test.expected {
			t.Errorf("expected %v for %v, got %v", test.expected, test.args, actual)
		}
	}
}

func TestReportFormat(t *testing.T) {
	// the report helper rejects anything but these two, and the flag's default
	// is text, so the mapping has to happen for every caller
	tests := []struct {
		name     string
		format   string
		expected string
	}{
		{name: "the flag default", format: "text", expected: "stdout"},
		{name: "json is passed through", format: "json", expected: "json"},
		{name: "an unset format", format: "", expected: "stdout"},
		{name: "anything unrecognised falls back to stdout", format: "table", expected: "stdout"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := &GlobalFlagCommand{format: test.format}
			if actual := c.ReportFormat(); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// A number flag given as zero is still a flag that was given, which is how a
// clone is told to drop the memory limit its source has.
func TestChangedInt(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected *int
	}{
		{name: "not given", args: []string{}},
		{name: "given", args: []string{"--memory", "512"}, expected: new(512)},
		{name: "given as zero", args: []string{"--memory", "0"}, expected: new(0)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var memory int
			f := flag.NewFlagSet("test", flag.ContinueOnError)
			f.IntVar(&memory, "memory", 0, "")
			if err := f.Parse(test.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			actual := changedInt(f, "memory", memory)
			if test.expected == nil {
				if actual != nil {
					t.Errorf("expected nil, got %d", *actual)
				}
				return
			}

			if actual == nil || *actual != *test.expected {
				t.Errorf("expected %d, got %v", *test.expected, actual)
			}
		})
	}
}

// A volume target flag given empty is still a flag that was given, which is how
// a clone or an upgrade is told to put every volume back where the definition
// mounts it.
func TestChangedVolumeTargets(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected *map[string]string
		refused  bool
	}{
		{name: "not given", args: []string{}},
		{name: "given", args: []string{"--volume-target", "data=/redis-data", "--volume-target", "config=/etc/redis"}, expected: &map[string]string{"data": "/redis-data", "config": "/etc/redis"}},
		{name: "given empty", args: []string{"--volume-target", ""}, expected: &map[string]string{}},
		{name: "given malformed", args: []string{"--volume-target", "data"}, refused: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var volumeTarget []string
			f := flag.NewFlagSet("test", flag.ContinueOnError)
			f.StringArrayVar(&volumeTarget, "volume-target", []string{}, "")
			if err := f.Parse(test.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			actual, err := changedVolumeTargets(f, "volume-target", volumeTarget)
			if test.refused {
				if err == nil {
					t.Errorf("expected %v to be refused", test.args)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if test.expected == nil {
				if actual != nil {
					t.Errorf("expected nil, got %v", *actual)
				}
				return
			}

			if actual == nil || !reflect.DeepEqual(*actual, *test.expected) {
				t.Errorf("expected %v, got %v", *test.expected, actual)
			}
		})
	}
}
