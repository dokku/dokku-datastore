package service

import (
	"strings"
	"testing"

	"github.com/dokku/dokku/plugins/common"
)

func TestValidateWaitTimeout(t *testing.T) {
	valid := []string{"", "1", "60", " 120 "}
	for _, value := range valid {
		if err := ValidateWaitTimeout(value); err != nil {
			t.Errorf("expected %q to be accepted, got %q", value, err)
		}
	}

	// zero is refused rather than read as unset, so clearing the setting is only
	// ever done by giving no value
	invalid := []string{"0", "-1", "abc", "1.5", "60s", "1m"}
	for _, value := range invalid {
		err := ValidateWaitTimeout(value)
		if err == nil {
			t.Errorf("expected %q to be refused, got no error", value)
			continue
		}

		if !strings.Contains(err.Error(), WaitTimeoutProperty) {
			t.Errorf("expected the error to name %s, got %q", WaitTimeoutProperty, err)
		}
	}
}

// Named after the variable the rest of a datastore's settings are, so that the
// readme and the code say the same thing.
func TestWaitTimeoutVariable(t *testing.T) {
	if variable := redisDatastore(t).Properties().WaitTimeoutVariable; variable != "REDIS_WAIT_TIMEOUT" {
		t.Errorf("expected REDIS_WAIT_TIMEOUT, got %s", variable)
	}
}

// The most specific answer wins: the service's own, then the host's, then the
// definition's, and otherwise none at all so the probe keeps its own default.
func TestWaitTimeoutPrecedence(t *testing.T) {
	withDefault := *redisDatastore(t)
	withDefault.Definition.Dokku.WaitTimeout = 60

	tests := []struct {
		name      string
		datastore Datastore
		property  string
		env       string
		expected  int
	}{
		{name: "nothing set anywhere", datastore: *redisDatastore(t), expected: 0},
		{name: "the definition's default", datastore: withDefault, expected: 60},
		{name: "the host over the definition", datastore: withDefault, env: "90", expected: 90},
		{name: "the service over the host", datastore: withDefault, env: "90", property: "120", expected: 120},
		{name: "the service with nothing else set", datastore: *redisDatastore(t), property: "45", expected: 45},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withServiceRoot(t, &test.datastore, "lollipop")
			t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)
			t.Setenv(test.datastore.Properties().WaitTimeoutVariable, test.env)

			if test.property != "" {
				if err := common.PropertyWrite(test.datastore.Properties().CommandPrefix, "lollipop", WaitTimeoutProperty, test.property); err != nil {
					t.Fatalf("failed to write the property: %v", err)
				}
			}

			timeout, err := WaitTimeout(&test.datastore, "lollipop")
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if timeout != test.expected {
				t.Errorf("expected %d, got %d", test.expected, timeout)
			}

			if reported := ServiceWaitTimeout(&test.datastore, "lollipop"); reported != test.property {
				t.Errorf("expected the service to report %q as set, got %q", test.property, reported)
			}
		})
	}
}

// A host variable that is not a number is refused rather than skipped, since
// skipping it would wait for a time nobody asked for, and the error says which
// variable to fix.
func TestWaitTimeoutRefusesAMalformedVariable(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)
	t.Setenv(redis.Properties().WaitTimeoutVariable, "forever")

	_, err := WaitTimeout(redis, "lollipop")
	if err == nil {
		t.Fatal("expected a malformed variable to be refused")
	}

	if !strings.Contains(err.Error(), "REDIS_WAIT_TIMEOUT") {
		t.Errorf("expected the error to name the variable, got %q", err)
	}
}
