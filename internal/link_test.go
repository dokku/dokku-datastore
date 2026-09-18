package internal

import (
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
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
