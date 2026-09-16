package internal

import (
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

func TestPromotionPlan(t *testing.T) {
	datastore := service.Datastores["redis"]
	serviceURL := "redis://:hunter2@dokku-redis-l:6379"

	tests := []struct {
		name        string
		environment map[string]string
		expected    map[string]string
		expectedErr string
	}{
		{
			name:        "the service is not linked",
			environment: map[string]string{"REDIS_URL": "redis://:p@host:6379/db"},
			expectedErr: "Not linked to app my-app",
		},
		{
			name:        "the service is already promoted",
			environment: map[string]string{"REDIS_URL": serviceURL},
			expectedErr: "Service l already promoted as REDIS_URL",
		},
		{
			name: "the displaced url is preserved under a generated alias",
			environment: map[string]string{
				"REDIS_URL":            "redis://:p@host:6379/db",
				"DOKKU_REDIS_BLUE_URL": serviceURL,
			},
			expected: map[string]string{
				"REDIS_URL":            serviceURL,
				"DOKKU_REDIS_AQUA_URL": "redis://:p@host:6379/db",
			},
		},
		{
			name: "no backup is made when another key already holds the displaced url",
			environment: map[string]string{
				"REDIS_URL":            "redis://:p@host:6379/db",
				"DOKKU_REDIS_RED_URL":  "redis://:p@host:6379/db",
				"DOKKU_REDIS_BLUE_URL": serviceURL,
			},
			expected: map[string]string{"REDIS_URL": serviceURL},
		},
		{
			name:        "nothing to preserve when the default variable is unset",
			environment: map[string]string{"DOKKU_REDIS_BLUE_URL": serviceURL},
			expected:    map[string]string{"REDIS_URL": serviceURL},
		},
		{
			name: "the alphabetically first linked key is promoted",
			environment: map[string]string{
				"DOKKU_REDIS_BLUE_URL": serviceURL,
				"DOKKU_REDIS_AQUA_URL": serviceURL + "?pool=5",
			},
			expected: map[string]string{"REDIS_URL": serviceURL + "?pool=5"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries, err := PromotionEntries(PromoteServiceInput{
				AppName:     "my-app",
				Datastore:   datastore,
				ServiceName: "l",
			}, test.environment, serviceURL)
			if test.expectedErr != "" {
				if err == nil {
					t.Fatalf("expected error %q, got none", test.expectedErr)
				}
				if err.Error() != test.expectedErr {
					t.Errorf("expected error %q, got %q", test.expectedErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %q", err)
			}
			if len(entries) != len(test.expected) {
				t.Fatalf("expected %v, got %v", test.expected, entries)
			}
			for key, value := range test.expected {
				if entries[key] != value {
					t.Errorf("expected %s=%q, got %q", key, value, entries[key])
				}
			}
		})
	}
}
