package internal

import (
	"slices"
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
