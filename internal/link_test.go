package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/mitchellh/cli"
)

func TestConfigKeysForURL(t *testing.T) {
	serviceURL := "redis://:hunter2@dokku-redis-ls:6379"

	tests := []struct {
		name        string
		environment map[string]string
		expected    []string
	}{
		{
			name:        "no config at all",
			environment: map[string]string{},
			expected:    []string{},
		},
		{
			name:        "nothing pointing at the service",
			environment: map[string]string{"OTHER_URL": "redis://:hunter2@dokku-redis-ms:6379"},
			expected:    []string{},
		},
		{
			name:        "the default alias",
			environment: map[string]string{"REDIS_URL": serviceURL},
			expected:    []string{"REDIS_URL"},
		},
		{
			name: "several keys, reported in a stable order",
			environment: map[string]string{
				"REDIS_URL":            serviceURL,
				"DOKKU_REDIS_AQUA_URL": serviceURL,
				"UNRELATED":            "something-else",
			},
			expected: []string{"DOKKU_REDIS_AQUA_URL", "REDIS_URL"},
		},
		{
			name:        "a url carrying a querystring still matches",
			environment: map[string]string{"REDIS_URL": serviceURL + "?pool=5"},
			expected:    []string{"REDIS_URL"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := ConfigKeysForURL(test.environment, serviceURL); !slices.Equal(actual, test.expected) {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

// A url that failed to render is empty, and every value contains the empty
// string: matching on it would have unlink unset the app's whole config.
func TestConfigKeysForURLWithAnEmptyURL(t *testing.T) {
	environment := map[string]string{"REDIS_URL": "redis://host:6379", "UNRELATED": "something-else"}

	if actual := ConfigKeysForURL(environment, ""); len(actual) != 0 {
		t.Errorf("expected an empty url to match nothing, got %v", actual)
	}
}

// fakeDokku puts a dokku on the path that prints the given config for
// config:export and records every command it is asked to run, so a link change
// can be checked without a dokku install. It returns the file the commands are
// recorded in.
func fakeDokku(t *testing.T, environment map[string]string) string {
	t.Helper()

	bin := t.TempDir()
	config := filepath.Join(bin, "config.json")
	calls := filepath.Join(bin, "calls")

	contents, err := json.Marshal(environment)
	if err != nil {
		t.Fatalf("failed to encode the config: %s", err)
	}
	if err := os.WriteFile(config, contents, 0644); err != nil {
		t.Fatalf("failed to write the config: %s", err)
	}

	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %q\nif [ \"$1\" = config:export ]; then cat %q; fi\n", calls, config)
	if err := os.WriteFile(filepath.Join(bin, "dokku"), []byte(script), 0755); err != nil {
		t.Fatalf("failed to write the fake dokku: %s", err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// the service-action triggers are skipped outside a dokku install
	t.Setenv("PLUGIN_PATH", "")

	return calls
}

// recordedCalls returns the dokku commands the fake was asked to run, bar the
// config:export every link change starts with
func recordedCalls(t *testing.T, calls string) []string {
	t.Helper()

	contents, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("failed to read the recorded calls: %s", err)
	}

	recorded := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		if !strings.HasPrefix(line, "config:export") {
			recorded = append(recorded, line)
		}
	}

	return recorded
}

// The links file decides whether an app is linked, since that is what destroy,
// linked and links read. An app whose url was repointed at another datastore
// used to be reported as not linked by unlink while destroy still refused.
func TestUnlinkService(t *testing.T) {
	tests := []struct {
		name          string
		links         []string
		config        func(serviceURL string) map[string]string
		expectedError string
		expectedCalls func(option string) []string
		expectedWarn  bool
	}{
		{
			name:  "linked, with the url in the config",
			links: []string{"my-app"},
			config: func(serviceURL string) map[string]string {
				return map[string]string{"REDIS_URL": serviceURL}
			},
			expectedCalls: func(option string) []string {
				return []string{
					"docker-options:remove my-app build,deploy,run " + option,
					"config:unset --no-restart my-app REDIS_URL",
				}
			},
		},
		{
			name:  "linked, with the url repointed at another datastore",
			links: []string{"my-app"},
			config: func(serviceURL string) map[string]string {
				return map[string]string{"REDIS_URL": "redis://:other@elsewhere:6379"}
			},
			expectedCalls: func(option string) []string {
				return []string{"docker-options:remove my-app build,deploy,run " + option}
			},
			expectedWarn: true,
		},
		{
			name:  "not in the links file, with the url in the config",
			links: []string{},
			config: func(serviceURL string) map[string]string {
				return map[string]string{"REDIS_URL": serviceURL}
			},
			expectedCalls: func(option string) []string {
				return []string{
					"docker-options:remove my-app build,deploy,run " + option,
					"config:unset --no-restart my-app REDIS_URL",
				}
			},
		},
		{
			name:  "not linked at all",
			links: []string{"other-app"},
			config: func(serviceURL string) map[string]string {
				return map[string]string{"REDIS_URL": "redis://:other@elsewhere:6379"}
			},
			expectedError: "Not linked to app my-app",
			expectedCalls: func(option string) []string {
				return []string{}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			datastore := linkedServices(t, map[string][]string{"lollipop": test.links})

			dokkuRoot := t.TempDir()
			t.Setenv("DOKKU_ROOT", dokkuRoot)
			if err := os.MkdirAll(filepath.Join(dokkuRoot, "my-app"), 0755); err != nil {
				t.Fatalf("failed to create the app root: %s", err)
			}

			serviceURL := datastore.URL("lollipop", "")
			if serviceURL == "" {
				t.Fatal("expected the service url to render")
			}

			calls := fakeDokku(t, test.config(serviceURL))
			ui := cli.NewMockUi()

			err := UnlinkService(t.Context(), UnlinkServiceInput{
				AppName:     "my-app",
				Datastore:   datastore,
				Logger:      Ui{Ui: ui},
				NoRestart:   true,
				ServiceName: "lollipop",
			})

			if test.expectedError == "" && err != nil {
				t.Fatalf("expected no error, got %s", err)
			}
			if test.expectedError != "" && (err == nil || err.Error() != test.expectedError) {
				t.Fatalf("expected %q, got %v", test.expectedError, err)
			}

			option := fmt.Sprintf("--link %s:%s", service.ContainerName(datastore, "lollipop"), service.DNSHostname(datastore, "lollipop"))
			if actual := recordedCalls(t, calls); !slices.Equal(actual, test.expectedCalls(option)) {
				t.Errorf("expected the calls %q, got %q", test.expectedCalls(option), actual)
			}

			// the app is out of the links file either way, and an app that was
			// never linked leaves the file as it was
			expectedLinks := slices.DeleteFunc(slices.Clone(test.links), func(app string) bool { return app == "my-app" })
			actualLinks := service.LinkedApps(t.Context(), service.LinkedAppsInput{Datastore: datastore, ServiceName: "lollipop"})
			if !slices.Equal(actualLinks, expectedLinks) {
				t.Errorf("expected the links %v, got %v", expectedLinks, actualLinks)
			}

			warned := strings.Contains(ui.ErrorWriter.String(), "none was unset")
			if warned != test.expectedWarn {
				t.Errorf("expected a warning to be %t, got the output %q", test.expectedWarn, ui.ErrorWriter.String())
			}
		})
	}
}

// Linking goes by the links file as unlinking does. A key that merely contains
// the alias no longer stands in for it, and an app whose config already holds
// the url is given the rest of the link rather than refused.
func TestLinkService(t *testing.T) {
	tests := []struct {
		name          string
		alias         string
		links         []string
		config        func(serviceURL string) map[string]string
		expectedError string
		expectedCalls func(serviceURL string, option string) []string
		expectedLinks []string
		expectedWarn  bool
	}{
		{
			name:  "a key merely containing the alias does not take its place",
			links: []string{},
			config: func(serviceURL string) map[string]string {
				return map[string]string{"EXTERNAL_REDIS_URL": "something"}
			},
			expectedCalls: func(serviceURL string, option string) []string {
				return []string{
					"docker-options:add my-app build,deploy,run " + option,
					"config:set --no-restart my-app REDIS_URL=" + serviceURL,
				}
			},
			expectedLinks: []string{"my-app"},
		},
		{
			name:  "a key merely containing the alias does not stop it being passed",
			alias: "REDIS",
			links: []string{},
			config: func(serviceURL string) map[string]string {
				return map[string]string{"CELERY_REDIS_URL": "redis://:other@elsewhere:6379"}
			},
			expectedCalls: func(serviceURL string, option string) []string {
				return []string{
					"docker-options:add my-app build,deploy,run " + option,
					"config:set --no-restart my-app REDIS_URL=" + serviceURL,
				}
			},
			expectedLinks: []string{"my-app"},
		},
		{
			name:  "the default alias holding another url",
			links: []string{},
			config: func(serviceURL string) map[string]string {
				return map[string]string{"REDIS_URL": "redis://:other@elsewhere:6379"}
			},
			expectedCalls: func(serviceURL string, option string) []string {
				return []string{
					"docker-options:add my-app build,deploy,run " + option,
					"config:set --no-restart my-app DOKKU_REDIS_AQUA_URL=" + serviceURL,
				}
			},
			expectedLinks: []string{"my-app"},
		},
		{
			name:  "not in the links file, with the url in the config",
			links: []string{},
			config: func(serviceURL string) map[string]string {
				return map[string]string{"REDIS_URL": serviceURL}
			},
			expectedCalls: func(serviceURL string, option string) []string {
				return []string{"docker-options:add my-app build,deploy,run " + option}
			},
			expectedLinks: []string{"my-app"},
			expectedWarn:  true,
		},
		{
			name:  "not in the links file, with the url under the alias passed",
			alias: "FOO",
			links: []string{},
			config: func(serviceURL string) map[string]string {
				return map[string]string{"FOO_URL": serviceURL}
			},
			expectedCalls: func(serviceURL string, option string) []string {
				return []string{"docker-options:add my-app build,deploy,run " + option}
			},
			expectedLinks: []string{"my-app"},
			expectedWarn:  true,
		},
		{
			name:  "linked, with the url in the config",
			links: []string{"my-app"},
			config: func(serviceURL string) map[string]string {
				return map[string]string{"REDIS_URL": serviceURL}
			},
			expectedError: "Already linked as REDIS_URL",
			expectedCalls: func(serviceURL string, option string) []string {
				return []string{}
			},
			expectedLinks: []string{"my-app"},
		},
		{
			name:  "linked, with the url repointed at another datastore",
			links: []string{"my-app"},
			config: func(serviceURL string) map[string]string {
				return map[string]string{"REDIS_URL": "redis://:other@elsewhere:6379"}
			},
			expectedError: "Already linked to app my-app",
			expectedCalls: func(serviceURL string, option string) []string {
				return []string{}
			},
			expectedLinks: []string{"my-app"},
		},
		{
			name:  "the alias passed holding another value",
			alias: "FOO",
			links: []string{},
			config: func(serviceURL string) map[string]string {
				return map[string]string{"FOO_URL": "something"}
			},
			expectedError: "Specified alias FOO already in use",
			expectedCalls: func(serviceURL string, option string) []string {
				return []string{}
			},
			expectedLinks: []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			datastore := linkedServices(t, map[string][]string{"lollipop": test.links})

			serviceURL := datastore.URL("lollipop", "")
			if serviceURL == "" {
				t.Fatal("expected the service url to render")
			}

			calls := fakeDokku(t, test.config(serviceURL))
			ui := cli.NewMockUi()

			err := LinkService(t.Context(), LinkServiceInput{
				Alias:       test.alias,
				AppName:     "my-app",
				Datastore:   datastore,
				Logger:      Ui{Ui: ui},
				NoRestart:   true,
				ServiceName: "lollipop",
			})

			if test.expectedError == "" && err != nil {
				t.Fatalf("expected no error, got %s", err)
			}
			if test.expectedError != "" && (err == nil || err.Error() != test.expectedError) {
				t.Fatalf("expected %q, got %v", test.expectedError, err)
			}

			option := fmt.Sprintf("--link %s:%s", service.ContainerName(datastore, "lollipop"), service.DNSHostname(datastore, "lollipop"))
			if actual := recordedCalls(t, calls); !slices.Equal(actual, test.expectedCalls(serviceURL, option)) {
				t.Errorf("expected the calls %q, got %q", test.expectedCalls(serviceURL, option), actual)
			}

			actualLinks := service.LinkedApps(t.Context(), service.LinkedAppsInput{Datastore: datastore, ServiceName: "lollipop"})
			if !slices.Equal(actualLinks, test.expectedLinks) {
				t.Errorf("expected the links %v, got %v", test.expectedLinks, actualLinks)
			}

			warned := strings.Contains(ui.ErrorWriter.String(), "none was set")
			if warned != test.expectedWarn {
				t.Errorf("expected a warning to be %t, got the output %q", test.expectedWarn, ui.ErrorWriter.String())
			}
		})
	}
}

func TestAlternateAlias(t *testing.T) {
	datastore := service.Datastores["redis"]

	tests := []struct {
		name        string
		environment map[string]string
		expected    string
	}{
		{
			name:        "nothing in use",
			environment: map[string]string{},
			expected:    "DOKKU_REDIS_AQUA",
		},
		{
			name:        "the default alias does not consume a color",
			environment: map[string]string{"REDIS_URL": "redis://host:6379"},
			expected:    "DOKKU_REDIS_AQUA",
		},
		{
			name:        "the first color is taken",
			environment: map[string]string{"DOKKU_REDIS_AQUA_URL": "redis://host:6379"},
			expected:    "DOKKU_REDIS_BLACK",
		},
		{
			name: "the first two colors are taken",
			environment: map[string]string{
				"DOKKU_REDIS_AQUA_URL":  "redis://host:6379",
				"DOKKU_REDIS_BLACK_URL": "redis://host:6379",
			},
			expected: "DOKKU_REDIS_BLUE",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := AlternateAlias(datastore, test.environment); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

func TestAlternateAliasExhausted(t *testing.T) {
	datastore := service.Datastores["redis"]

	environment := map[string]string{}
	for _, color := range alternateAliasColors {
		environment[datastore.Properties().AltAlias+"_"+color+"_URL"] = "redis://host:6379"
	}

	if actual := AlternateAlias(datastore, environment); actual != "" {
		t.Errorf("expected an empty alias once every color is in use, got %q", actual)
	}
}

// The app's config is read and written through the installed dokku rather than
// through a library compiled in, because where dokku keeps that config has
// moved between releases: linking against one layout and running against
// another wrote where nothing reads, and reported success.
func TestConfigSetArgs(t *testing.T) {
	entries := map[string]string{
		"REDIS_URL":     "redis://one",
		"DATABASE_URL":  "redis://two",
		"ANOTHER_THING": "three",
	}

	restarting := ConfigSetArgs("my-app", entries, true)
	if restarting[0] != "config:set" {
		t.Errorf("expected config:set, got %s", restarting[0])
	}

	// the app comes straight after the subcommand when nothing precedes it
	if restarting[1] != "my-app" {
		t.Errorf("expected the app second, got %v", restarting)
	}

	// sorted, so the same variables produce the same command twice
	expected := "config:set my-app ANOTHER_THING=three DATABASE_URL=redis://two REDIS_URL=redis://one"
	if actual := strings.Join(restarting, " "); actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}

// The subcommand stops reading flags at the first positional argument, so a
// flag after the app name would be taken for a variable to set rather than
// honoured.
func TestConfigSetArgsPutsTheFlagBeforeTheApp(t *testing.T) {
	args := ConfigSetArgs("my-app", map[string]string{"REDIS_URL": "redis://one"}, false)

	expected := "config:set --no-restart my-app REDIS_URL=redis://one"
	if actual := strings.Join(args, " "); actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}

	// and it is absent entirely when the app is meant to restart
	restarting := ConfigSetArgs("my-app", map[string]string{"REDIS_URL": "redis://one"}, true)
	if slices.Contains(restarting, "--no-restart") {
		t.Errorf("expected no flag when restarting, got %v", restarting)
	}
}

func TestConfigUnsetArgs(t *testing.T) {
	expected := "config:unset --no-restart my-app REDIS_URL DATABASE_URL"
	if actual := strings.Join(ConfigUnsetArgs("my-app", []string{"REDIS_URL", "DATABASE_URL"}, false), " "); actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}

	restarting := ConfigUnsetArgs("my-app", []string{"REDIS_URL"}, true)
	if actual := strings.Join(restarting, " "); actual != "config:unset my-app REDIS_URL" {
		t.Errorf("expected no flag when restarting, got %q", actual)
	}
}

// The scheme override is read from an environment already in hand, so asking
// for it costs no second call to dokku.
func TestSchemeForApp(t *testing.T) {
	redis := service.Datastores["redis"]

	if actual := SchemeForApp(redis, map[string]string{"REDIS_DATABASE_SCHEME": "rediss"}); actual != "rediss" {
		t.Errorf("expected the override, got %q", actual)
	}

	if actual := SchemeForApp(redis, map[string]string{}); actual != "" {
		t.Errorf("expected nothing without an override, got %q", actual)
	}
}
