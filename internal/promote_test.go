package internal

import (
	"slices"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

func TestPromotionPlan(t *testing.T) {
	datastore := service.Datastores["redis"]
	serviceURL := "redis://:hunter2@dokku-redis-l:6379"

	tests := []struct {
		name              string
		environment       map[string]string
		recorded          []string
		expected          map[string]string
		expectedPreserved string
		expectedErr       string
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
			expectedPreserved: "DOKKU_REDIS_AQUA_URL",
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
		{
			name:        "a recorded key whose scheme changed is promoted",
			environment: map[string]string{"MB_DB_CONNECTION_URI": "rediss://:hunter2@dokku-redis-l:6379"},
			recorded:    []string{"MB_DB_CONNECTION_URI"},
			expected:    map[string]string{"REDIS_URL": "rediss://:hunter2@dokku-redis-l:6379"},
		},
		{
			name:        "a recorded key whose scheme changed is already promoted",
			environment: map[string]string{"REDIS_URL": "rediss://:hunter2@dokku-redis-l:6379"},
			recorded:    []string{"REDIS_URL"},
			expectedErr: "Service l already promoted as REDIS_URL",
		},
		{
			name:        "a recorded key pointed elsewhere is not linked",
			environment: map[string]string{"MB_DB_CONNECTION_URI": "redis://:p@host:6379/db"},
			recorded:    []string{"MB_DB_CONNECTION_URI"},
			expectedErr: "Not linked to app my-app",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries, preserved, err := PromotionEntries(PromoteServiceInput{
				AppName:     "my-app",
				Datastore:   datastore,
				ServiceName: "l",
			}, test.environment, LinkedConfigKeys(test.environment, test.recorded, serviceURL))
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
			if preserved != test.expectedPreserved {
				t.Errorf("expected the displaced url preserved as %q, got %q", test.expectedPreserved, preserved)
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

// Promoting a service moves the default variable from one service's record to
// another's, and the url it displaced is recorded under the alias it was moved
// to, so that unlinking either service afterwards unsets what it should.
func TestPromoteServiceRecordsTheKeys(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{
		"lollipop":   {"my-app"},
		"gobstopper": {"my-app"},
	})

	promotedURL := datastore.URL("lollipop", "")
	displacedURL := datastore.URL("gobstopper", "")
	if promotedURL == "" || displacedURL == "" || promotedURL == displacedURL {
		t.Fatalf("expected two distinct service urls, got %q and %q", promotedURL, displacedURL)
	}

	if err := service.SetLinkConfigKeys(datastore, "lollipop", "my-app", []string{"DOKKU_REDIS_BLUE_URL"}); err != nil {
		t.Fatalf("failed to record the keys: %s", err)
	}
	if err := service.SetLinkConfigKeys(datastore, "gobstopper", "my-app", []string{"REDIS_URL"}); err != nil {
		t.Fatalf("failed to record the keys: %s", err)
	}

	calls := fakeDokku(t, map[string]string{
		"REDIS_URL":            displacedURL,
		"DOKKU_REDIS_BLUE_URL": promotedURL,
	})

	if err := PromoteService(t.Context(), PromoteServiceInput{
		AppName:     "my-app",
		Datastore:   datastore,
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	expectedCalls := []string{"config:set my-app DOKKU_REDIS_AQUA_URL=" + displacedURL + " REDIS_URL=" + promotedURL}
	if actual := recordedCalls(t, calls); !slices.Equal(actual, expectedCalls) {
		t.Errorf("expected the calls %q, got %q", expectedCalls, actual)
	}

	if actual := service.LinkConfigKeys(datastore, "lollipop", "my-app"); !slices.Equal(actual, []string{"DOKKU_REDIS_BLUE_URL", "REDIS_URL"}) {
		t.Errorf("expected the promoted service to record the default variable, got %v", actual)
	}

	if actual := service.LinkConfigKeys(datastore, "gobstopper", "my-app"); !slices.Equal(actual, []string{"DOKKU_REDIS_AQUA_URL"}) {
		t.Errorf("expected the displaced service to record the alias its url moved to, got %v", actual)
	}
}
