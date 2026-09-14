package commands

import (
	"testing"

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
