package internal

import (
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

func TestGlobalPropertyNames(t *testing.T) {
	expected := []string{"disable-pull", "wait-timeout"}
	if actual := GlobalPropertyNames(); !slices.Equal(actual, expected) {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

// A key set for one service is not one set for all of them, so the per-service
// keys are refused with the keys that are taken instead
func TestValidateGlobalPropertyRejectsUnknownKeys(t *testing.T) {
	for _, key := range []string{"not-a-property", "memory", "password"} {
		err := ValidateGlobalProperty(key, "value")
		if err == nil {
			t.Fatalf("expected %s to be refused, got no error", key)
		}

		expected := "Invalid global key specified, valid keys include: disable-pull, wait-timeout"
		if err.Error() != expected {
			t.Errorf("expected %q, got %q", expected, err)
		}
	}
}

func TestValidateGlobalPropertyValues(t *testing.T) {
	tests := []struct {
		key   string
		value string
		valid bool
	}{
		{key: "disable-pull", value: "", valid: true},
		{key: "disable-pull", value: "true", valid: true},
		{key: "disable-pull", value: "false", valid: true},
		{key: "disable-pull", value: "yes", valid: false},
		{key: "disable-pull", value: "1", valid: false},
		{key: "wait-timeout", value: "", valid: true},
		{key: "wait-timeout", value: "120", valid: true},
		{key: "wait-timeout", value: "0", valid: false},
		{key: "wait-timeout", value: "forever", valid: false},
	}

	for _, test := range tests {
		t.Run(test.key+"="+test.value, func(t *testing.T) {
			err := ValidateGlobalProperty(test.key, test.value)
			if test.valid && err != nil {
				t.Errorf("expected %q to be accepted, got %q", test.value, err)
			}

			if !test.valid {
				if err == nil {
					t.Fatalf("expected %q to be refused, got no error", test.value)
				}

				if !strings.Contains(err.Error(), test.key) {
					t.Errorf("expected the error to name %s, got %q", test.key, err)
				}
			}
		})
	}
}

// A global property is kept for the datastore rather than for any service, and
// is read back by what the plugin does for every service of it
func TestSetGlobalPropertyRoundTrip(t *testing.T) {
	redis := service.Datastores["redis"]
	withInfoService(t, redis, "lollipop")

	if err := SetGlobalProperty(redis, service.WaitTimeoutProperty, " 120 "); err != nil {
		t.Fatalf("expected the wait timeout to be accepted, got %q", err)
	}

	value, err := ReadGlobalProperty(redis, service.WaitTimeoutProperty)
	if err != nil || value != "120" {
		t.Errorf("expected 120, got %q and %v", value, err)
	}

	timeout, err := service.WaitTimeout(redis, "lollipop")
	if err != nil || timeout != 120 {
		t.Errorf("expected the service to be waited on for 120 seconds, got %d and %v", timeout, err)
	}

	if reported := service.ServiceWaitTimeout(redis, "lollipop"); reported != "" {
		t.Errorf("expected the service to report nothing set of its own, got %q", reported)
	}

	if err := SetGlobalProperty(redis, service.WaitTimeoutProperty, ""); err != nil {
		t.Fatalf("expected clearing the wait timeout to be allowed, got %q", err)
	}

	if value, _ := ReadGlobalProperty(redis, service.WaitTimeoutProperty); value != "" {
		t.Errorf("expected nothing once cleared, got %q", value)
	}
}

// A value that is refused leaves what was there before
func TestSetGlobalPropertyRefusesAnUnusableValue(t *testing.T) {
	redis := service.Datastores["redis"]
	withInfoService(t, redis, "lollipop")

	if err := SetGlobalProperty(redis, service.DisablePullProperty, "true"); err != nil {
		t.Fatalf("expected true to be accepted, got %q", err)
	}

	if err := SetGlobalProperty(redis, service.DisablePullProperty, "yes"); err == nil {
		t.Fatal("expected yes to be refused, got no error")
	}

	if value := service.GlobalProperty(redis, service.DisablePullProperty); value != "true" {
		t.Errorf("expected the earlier value to be kept, got %q", value)
	}
}
