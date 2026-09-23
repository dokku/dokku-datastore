package service

import (
	"sort"
	"strings"
	"testing"
)

func TestResolveLogConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    ResolveLogConfigInput
		expected string
	}{
		{
			// the whole point of the change: a service nobody has said anything
			// about stops growing a log file without end
			name:     "a service with nothing set takes the default cap",
			input:    ResolveLogConfigInput{DaemonDriver: "json-file"},
			expected: "max-size=10m",
		},
		{
			// what dokku was told for its apps, which datastore containers were
			// never covered by
			name:     "dokku's own retention is inherited",
			input:    ResolveLogConfigInput{DaemonDriver: "json-file", GlobalMaxSize: "20m"},
			expected: "max-size=20m",
		},
		{
			name:     "the local driver takes a cap too",
			input:    ResolveLogConfigInput{DaemonDriver: "local"},
			expected: "max-size=10m",
		},
		{
			// docker refuses an option a driver does not understand, so a
			// container that could not be made is what inheriting here would cost
			name:     "a driver with no max-size takes none",
			input:    ResolveLogConfigInput{DaemonDriver: "journald", GlobalMaxSize: "20m"},
			expected: "",
		},
		{
			// the daemon could not be asked, so nothing is assumed about what it
			// would accept
			name:     "an unsettled daemon driver takes none",
			input:    ResolveLogConfigInput{GlobalMaxSize: "20m"},
			expected: "",
		},
		{
			name:     "a driver the service names is emitted",
			input:    ResolveLogConfigInput{Driver: "json-file"},
			expected: "json-file max-size=10m",
		},
		{
			// the service named journald, so the daemon's default is not what it
			// will be logged by and the cap is held back on the strength of the
			// service's own answer
			name:     "a driver the service names decides the cap",
			input:    ResolveLogConfigInput{Driver: "journald", DaemonDriver: "json-file"},
			expected: "journald",
		},
		{
			name:     "an option the service names wins over the default",
			input:    ResolveLogConfigInput{Options: "max-size=50m", DaemonDriver: "json-file", GlobalMaxSize: "20m"},
			expected: "max-size=50m",
		},
		{
			// the default is still inherited alongside an unrelated option, or a
			// service that wanted a tag would silently lose its cap
			name:     "an unrelated option keeps the default",
			input:    ResolveLogConfigInput{Options: "tag=lollipop", DaemonDriver: "json-file"},
			expected: "max-size=10m tag=lollipop",
		},
		{
			// docker has no unlimited, so this is how a service opts out rather
			// than something docker is ever asked for
			name:     "unlimited is how a service opts out",
			input:    ResolveLogConfigInput{Options: "max-size=unlimited", DaemonDriver: "json-file"},
			expected: "",
		},
		{
			name:     "an unlimited global leaves every service uncapped",
			input:    ResolveLogConfigInput{DaemonDriver: "json-file", GlobalMaxSize: "unlimited"},
			expected: "",
		},
		{
			name:     "an unlimited opt-out keeps the options beside it",
			input:    ResolveLogConfigInput{Options: "max-size=unlimited,max-file=3", DaemonDriver: "json-file"},
			expected: "max-file=3",
		},
		{
			// a value the service asked for is passed on whatever the driver is:
			// an operator who named one has named it, and docker is the one that
			// gets to refuse it
			name:     "an option the service names is not held back from a driver",
			input:    ResolveLogConfigInput{Driver: "journald", Options: "max-size=20m"},
			expected: "journald max-size=20m",
		},
		{
			name:     "several options",
			input:    ResolveLogConfigInput{Driver: "json-file", Options: "max-file=3,max-size=20m"},
			expected: "json-file max-file=3 max-size=20m",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := ResolveLogConfig(test.input)
			if err != nil {
				t.Fatalf("expected no error, got %q", err)
			}

			if actual := flatten(config); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// A malformed value reaches here from a hand edited property file as well as
// from a flag, so the resolver refuses it rather than trusting that whatever
// wrote it had been checked.
func TestResolveLogConfigRefusesWhatDockerWould(t *testing.T) {
	tests := []struct {
		name     string
		input    ResolveLogConfigInput
		expected string
	}{
		{
			name:     "a driver that is not a name",
			input:    ResolveLogConfigInput{Driver: "json file"},
			expected: `invalid log-driver value "json file"`,
		},
		{
			name:     "an option with no value",
			input:    ResolveLogConfigInput{Options: "max-size"},
			expected: `invalid log-opt entry "max-size"`,
		},
		{
			name:     "an option with no key",
			input:    ResolveLogConfigInput{Options: "=20m"},
			expected: `invalid log-opt entry "=20m"`,
		},
		{
			name:     "a key that is not a key",
			input:    ResolveLogConfigInput{Options: "max size=20m"},
			expected: `invalid log-opt key "max size"`,
		},
		{
			name:     "a max-size in an unknown unit",
			input:    ResolveLogConfigInput{Options: "max-size=20t"},
			expected: `invalid max-size value "20t"`,
		},
		{
			name:     "a max-file of zero",
			input:    ResolveLogConfigInput{Options: "max-file=0"},
			expected: `invalid max-file value "0"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveLogConfig(test.input)
			if err == nil {
				t.Fatal("expected an error, got none")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected the error to contain %q, got %q", test.expected, err)
			}
		})
	}
}

func TestParseLogOptions(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{name: "nothing at all", value: "", expected: ""},
		{name: "one option", value: "max-size=20m", expected: "max-size=20m"},
		{name: "several", value: "max-size=20m,max-file=3", expected: "max-file=3 max-size=20m"},
		{name: "spaces around an entry", value: " max-size=20m , max-file=3 ", expected: "max-file=3 max-size=20m"},
		// a trailing comma is what a shell loop building the value leaves behind
		{name: "an empty entry", value: "max-size=20m,", expected: "max-size=20m"},
		// the value half is the driver's vocabulary, not this plugin's
		{name: "a value holding an equals sign", value: "tag={{.Name}}=x", expected: "tag={{.Name}}=x"},
		{name: "an unknown option passes through", value: "syslog-address=tcp://example.com:514", expected: "syslog-address=tcp://example.com:514"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options, err := ParseLogOptions(test.value)
			if err != nil {
				t.Fatalf("expected no error, got %q", err)
			}

			if actual := flattenOptions(options); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

func TestValidateLogDriver(t *testing.T) {
	valid := []string{"", "json-file", "local", "journald", "none", "awslogs", "my-own-driver"}
	for _, value := range valid {
		if err := ValidateLogDriver(value); err != nil {
			t.Errorf("expected %q to be accepted, got %q", value, err)
		}
	}

	invalid := []string{"json file", "JSON-FILE", "-json", "json;rm", "1driver"}
	for _, value := range invalid {
		if err := ValidateLogDriver(value); err == nil {
			t.Errorf("expected %q to be refused, got no error", value)
		}
	}
}

// flatten renders a config the way the tests above read best: the driver, then
// the options in the order they are emitted in.
func flatten(config LogConfig) string {
	parts := []string{}
	if config.Driver != "" {
		parts = append(parts, config.Driver)
	}

	if options := flattenOptions(config.Options); options != "" {
		parts = append(parts, options)
	}

	return strings.Join(parts, " ")
}

func flattenOptions(options map[string]string) string {
	names := make([]string, 0, len(options))
	for name := range options {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+options[name])
	}

	return strings.Join(parts, " ")
}
