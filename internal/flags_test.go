package internal

import (
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

// clearImageEnv takes both pairs out of the environment, so a case that is
// about one of them is not answered by the other.
func clearImageEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{"REDIS_IMAGE", "REDIS_IMAGE_VERSION", LegacyImageVariable, LegacyImageVersionVariable} {
		t.Setenv(name, "")
	}
}

// The names the readme documents are the names create reads. They used not to
// be: the readme said REDIS_IMAGE and create read PLUGIN_IMAGE, which nothing
// on a host running this binary sets, so an operator who exported the
// documented name got a service on the definition's own image.
func TestUpdateFlagFromEnvReadsTheDocumentedNames(t *testing.T) {
	redis := service.Datastores["redis"]

	tests := []struct {
		name                 string
		environment          map[string]string
		image                string
		imageVersion         string
		expectedImage        string
		expectedImageVersion string
	}{
		{
			name: "the documented names",
			environment: map[string]string{
				"REDIS_IMAGE":         "redis/redis-stack-server",
				"REDIS_IMAGE_VERSION": "7.2.0-v10",
			},
			expectedImage:        "redis/redis-stack-server",
			expectedImageVersion: "7.2.0-v10",
		},
		{
			// a host carried over from the bash plugins may still export the
			// name their config.sh used internally
			name: "the names the bash plugins used",
			environment: map[string]string{
				LegacyImageVariable:        "redis/redis-stack-server",
				LegacyImageVersionVariable: "7.2.0-v10",
			},
			expectedImage:        "redis/redis-stack-server",
			expectedImageVersion: "7.2.0-v10",
		},
		{
			// neither is scoped to a datastore, so a host that exports one means
			// it for every plugin on the box - a coarser statement than naming
			// redis, and the one that loses
			name: "the documented name wins",
			environment: map[string]string{
				"REDIS_IMAGE":              "redis/redis-stack-server",
				"REDIS_IMAGE_VERSION":      "7.2.0-v10",
				LegacyImageVariable:        "valkey/valkey",
				LegacyImageVersionVariable: "9.9.9",
			},
			expectedImage:        "redis/redis-stack-server",
			expectedImageVersion: "7.2.0-v10",
		},
		{
			name:                 "a flag beats the environment",
			environment:          map[string]string{"REDIS_IMAGE": "valkey/valkey"},
			image:                "redis/redis-stack-server",
			imageVersion:         "7.2.0-v10",
			expectedImage:        "redis/redis-stack-server",
			expectedImageVersion: "7.2.0-v10",
		},
		{
			// the definition's default is not applied here. Applying it meant
			// the resolver was always handed two filled halves and could never
			// tell an image an operator named from one it had supplied itself
			name: "nothing is filled in from the definition",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearImageEnv(t)
			for name, value := range test.environment {
				t.Setenv(name, value)
			}

			updated, err := UpdateFlagFromEnv(UpdateFlagFromEnvInput{
				Datastore:    redis,
				Image:        test.image,
				ImageVersion: test.imageVersion,
			})
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if updated.Image != test.expectedImage {
				t.Errorf("expected image %q, got %q", test.expectedImage, updated.Image)
			}

			if updated.ImageVersion != test.expectedImageVersion {
				t.Errorf("expected image version %q, got %q", test.expectedImageVersion, updated.ImageVersion)
			}
		})
	}
}
