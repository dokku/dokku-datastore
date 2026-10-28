package internal

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/mitchellh/cli"
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

func TestJDBCPromotionEntries(t *testing.T) {
	jdbcKeys := []string{"JDBC_DATABASE_BLUE_PASSWORD", "JDBC_DATABASE_BLUE_URL", "JDBC_DATABASE_BLUE_USERNAME"}
	promoted := map[string]string{
		"DATABASE_BLUE_URL":           "postgres://postgres:p@lollipop:5432/lollipop",
		"JDBC_DATABASE_BLUE_URL":      "jdbc:postgresql://lollipop:5432/lollipop?user=postgres&password=p",
		"JDBC_DATABASE_BLUE_USERNAME": "postgres",
		"JDBC_DATABASE_BLUE_PASSWORD": "p",
	}
	displaced := map[string]string{
		"DATABASE_URL":           "postgres://postgres:q@gobstopper:5432/gobstopper",
		"JDBC_DATABASE_URL":      "jdbc:postgresql://gobstopper:5432/gobstopper?user=postgres&password=q",
		"JDBC_DATABASE_USERNAME": "postgres",
		"JDBC_DATABASE_PASSWORD": "q",
	}
	withEnvironment := func(groups ...map[string]string) map[string]string {
		environment := map[string]string{}
		for _, group := range groups {
			maps.Copy(environment, group)
		}
		return environment
	}

	tests := []struct {
		name              string
		environment       map[string]string
		jdbcKeys          []string
		preservedKey      string
		expected          map[string]string
		expectedPreserved bool
		expectedErr       string
	}{
		{
			name:        "a service linked without --jdbc",
			environment: withEnvironment(displaced, map[string]string{"DATABASE_BLUE_URL": promoted["DATABASE_BLUE_URL"]}),
			expected:    map[string]string{},
		},
		{
			name:        "nothing to displace",
			environment: promoted,
			jdbcKeys:    jdbcKeys,
			expected: map[string]string{
				"JDBC_DATABASE_URL":      promoted["JDBC_DATABASE_BLUE_URL"],
				"JDBC_DATABASE_USERNAME": "postgres",
				"JDBC_DATABASE_PASSWORD": "p",
			},
		},
		{
			name:         "the displaced values go with the preserved url",
			environment:  withEnvironment(promoted, displaced),
			jdbcKeys:     jdbcKeys,
			preservedKey: "DOKKU_POSTGRES_AQUA_URL",
			expected: map[string]string{
				"JDBC_DATABASE_URL":                 promoted["JDBC_DATABASE_BLUE_URL"],
				"JDBC_DATABASE_USERNAME":            "postgres",
				"JDBC_DATABASE_PASSWORD":            "p",
				"JDBC_DOKKU_POSTGRES_AQUA_URL":      displaced["JDBC_DATABASE_URL"],
				"JDBC_DOKKU_POSTGRES_AQUA_USERNAME": "postgres",
				"JDBC_DOKKU_POSTGRES_AQUA_PASSWORD": "q",
			},
			expectedPreserved: true,
		},
		{
			// the displaced url is held elsewhere already, so it is not
			// preserved, and neither are the jdbc values beside it
			name:        "the displaced values are not preserved without the url",
			environment: withEnvironment(promoted, displaced),
			jdbcKeys:    jdbcKeys,
			expected: map[string]string{
				"JDBC_DATABASE_URL":      promoted["JDBC_DATABASE_BLUE_URL"],
				"JDBC_DATABASE_USERNAME": "postgres",
				"JDBC_DATABASE_PASSWORD": "p",
			},
		},
		{
			name:         "a name the displaced values would move to in use",
			environment:  withEnvironment(promoted, displaced, map[string]string{"JDBC_DOKKU_POSTGRES_AQUA_USERNAME": "someone"}),
			jdbcKeys:     jdbcKeys,
			preservedKey: "DOKKU_POSTGRES_AQUA_URL",
			expectedErr:  "Unable to preserve JDBC_DATABASE_USERNAME as JDBC_DOKKU_POSTGRES_AQUA_USERNAME, which is already in use",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries, preserved, err := JDBCPromotionEntries("DATABASE_URL", test.environment, []string{"DATABASE_BLUE_URL"}, test.jdbcKeys, test.preservedKey)
			if test.expectedErr != "" {
				if err == nil || err.Error() != test.expectedErr {
					t.Fatalf("expected error %q, got %v", test.expectedErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %q", err)
			}
			if preserved != test.expectedPreserved {
				t.Errorf("expected preserved to be %t, got %t", test.expectedPreserved, preserved)
			}
			if !maps.Equal(entries, test.expected) {
				t.Errorf("expected %v, got %v", test.expected, entries)
			}
		})
	}
}

// Promoting a service linked with --jdbc moves its jdbc values onto the ones
// named after the default variable, and the displaced ones follow the displaced
// url, in both the config and the records
func TestPromoteServiceMovesTheJDBCValues(t *testing.T) {
	datastore := jdbcServices(t, map[string][]string{
		"lollipop":   {"my-app"},
		"gobstopper": {"my-app"},
	})
	// a password of its own, so the two services' values differ
	if err := os.WriteFile(filepath.Join(service.Folders(datastore, "gobstopper").Root, "PASSWORD"), []byte("hunter3"), 0600); err != nil {
		t.Fatalf("failed to write the password: %s", err)
	}

	promotedURL := datastore.URL("lollipop", "")
	displacedURL := datastore.URL("gobstopper", "")
	promotedJDBC, _ := datastore.JDBC("lollipop")
	displacedJDBC, _ := datastore.JDBC("gobstopper")

	if err := service.SetLinkConfigKeys(datastore, "lollipop", "my-app", []string{"DOKKU_POSTGRES_BLUE_URL"}); err != nil {
		t.Fatalf("failed to record the keys: %s", err)
	}
	if err := service.SetLinkJDBCConfigKeys(datastore, "lollipop", "my-app", JDBCConfigKeys("DOKKU_POSTGRES_BLUE_URL").List()); err != nil {
		t.Fatalf("failed to record the jdbc keys: %s", err)
	}
	if err := service.SetLinkConfigKeys(datastore, "gobstopper", "my-app", []string{"DATABASE_URL"}); err != nil {
		t.Fatalf("failed to record the keys: %s", err)
	}
	if err := service.SetLinkJDBCConfigKeys(datastore, "gobstopper", "my-app", JDBCConfigKeys("DATABASE_URL").List()); err != nil {
		t.Fatalf("failed to record the jdbc keys: %s", err)
	}

	calls := fakeDokku(t, map[string]string{
		"DATABASE_URL":                      displacedURL,
		"JDBC_DATABASE_URL":                 displacedJDBC.URL,
		"JDBC_DATABASE_USERNAME":            displacedJDBC.Username,
		"JDBC_DATABASE_PASSWORD":            displacedJDBC.Password,
		"DOKKU_POSTGRES_BLUE_URL":           promotedURL,
		"JDBC_DOKKU_POSTGRES_BLUE_URL":      promotedJDBC.URL,
		"JDBC_DOKKU_POSTGRES_BLUE_USERNAME": promotedJDBC.Username,
		"JDBC_DOKKU_POSTGRES_BLUE_PASSWORD": promotedJDBC.Password,
	})

	if err := PromoteService(t.Context(), PromoteServiceInput{
		AppName:     "my-app",
		Datastore:   datastore,
		Logger:      Ui{Ui: cli.NewMockUi()},
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	expectedCalls := []string{"config:set my-app" +
		" DATABASE_URL=" + promotedURL +
		" DOKKU_POSTGRES_AQUA_URL=" + displacedURL +
		" JDBC_DATABASE_PASSWORD=" + promotedJDBC.Password +
		" JDBC_DATABASE_URL=" + promotedJDBC.URL +
		" JDBC_DATABASE_USERNAME=" + promotedJDBC.Username +
		" JDBC_DOKKU_POSTGRES_AQUA_PASSWORD=" + displacedJDBC.Password +
		" JDBC_DOKKU_POSTGRES_AQUA_URL=" + displacedJDBC.URL +
		" JDBC_DOKKU_POSTGRES_AQUA_USERNAME=" + displacedJDBC.Username,
	}
	if actual := recordedCalls(t, calls); !slices.Equal(actual, expectedCalls) {
		t.Errorf("expected the calls %q, got %q", expectedCalls, actual)
	}

	expectedPromoted := append(JDBCConfigKeys("DATABASE_URL").List(), JDBCConfigKeys("DOKKU_POSTGRES_BLUE_URL").List()...)
	slices.Sort(expectedPromoted)
	if actual := service.LinkJDBCConfigKeys(datastore, "lollipop", "my-app"); !slices.Equal(actual, expectedPromoted) {
		t.Errorf("expected the promoted service to record %v, got %v", expectedPromoted, actual)
	}

	expectedDisplaced := JDBCConfigKeys("DOKKU_POSTGRES_AQUA_URL").List()
	if actual := service.LinkJDBCConfigKeys(datastore, "gobstopper", "my-app"); !slices.Equal(actual, expectedDisplaced) {
		t.Errorf("expected the displaced service to record %v, got %v", expectedDisplaced, actual)
	}
}

// Promoting a service linked without --jdbc leaves the jdbc values alone, and
// says that they still point at the service the url was moved off
func TestPromoteServiceWithoutJDBCWarns(t *testing.T) {
	datastore := jdbcServices(t, map[string][]string{
		"lollipop":   {"my-app"},
		"gobstopper": {"my-app"},
	})
	if err := os.WriteFile(filepath.Join(service.Folders(datastore, "gobstopper").Root, "PASSWORD"), []byte("hunter3"), 0600); err != nil {
		t.Fatalf("failed to write the password: %s", err)
	}

	promotedURL := datastore.URL("lollipop", "")
	displacedURL := datastore.URL("gobstopper", "")
	displacedJDBC, _ := datastore.JDBC("gobstopper")

	if err := service.SetLinkConfigKeys(datastore, "lollipop", "my-app", []string{"DOKKU_POSTGRES_BLUE_URL"}); err != nil {
		t.Fatalf("failed to record the keys: %s", err)
	}
	if err := service.SetLinkJDBCConfigKeys(datastore, "gobstopper", "my-app", JDBCConfigKeys("DATABASE_URL").List()); err != nil {
		t.Fatalf("failed to record the jdbc keys: %s", err)
	}

	calls := fakeDokku(t, map[string]string{
		"DATABASE_URL":            displacedURL,
		"JDBC_DATABASE_URL":       displacedJDBC.URL,
		"DOKKU_POSTGRES_BLUE_URL": promotedURL,
	})
	ui := cli.NewMockUi()

	if err := PromoteService(t.Context(), PromoteServiceInput{
		AppName:     "my-app",
		Datastore:   datastore,
		Logger:      Ui{Ui: ui},
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	expectedCalls := []string{"config:set my-app DATABASE_URL=" + promotedURL + " DOKKU_POSTGRES_AQUA_URL=" + displacedURL}
	if actual := recordedCalls(t, calls); !slices.Equal(actual, expectedCalls) {
		t.Errorf("expected the calls %q, got %q", expectedCalls, actual)
	}

	if !strings.Contains(ui.ErrorWriter.String(), "JDBC_DATABASE_URL does not point at it") {
		t.Errorf("expected a warning about JDBC_DATABASE_URL, got %q", ui.ErrorWriter.String())
	}

	if actual := service.LinkJDBCConfigKeys(datastore, "gobstopper", "my-app"); !slices.Equal(actual, JDBCConfigKeys("DATABASE_URL").List()) {
		t.Errorf("expected the displaced service to keep its jdbc record, got %v", actual)
	}
}
