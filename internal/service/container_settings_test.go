package service

import (
	"strings"
	"testing"
)

// A service memory is a whole number of megabytes, and zero is refused rather
// than read as no size at all, since telling a datastore to hold nothing is
// never what was meant. Empty leaves the size to the datastore.
func TestValidateServiceMemory(t *testing.T) {
	for _, value := range []string{"", "1", "64", "4096"} {
		if err := ValidateServiceMemory(value); err != nil {
			t.Errorf("expected %q to be accepted, got %s", value, err)
		}
	}

	for _, value := range []string{"0", "-1", "64m", "1.5", " 64"} {
		err := ValidateServiceMemory(value)
		if err == nil {
			t.Errorf("expected %q to be refused", value)
			continue
		}

		if !strings.Contains(err.Error(), "invalid service-memory value") {
			t.Errorf("expected the error for %q to name the property, got %s", value, err)
		}
	}
}

// A datastore with no way to be told a service memory refuses one, so that a
// value is never recorded where it would not reach the datastore. Nothing given
// is never refused.
func TestCheckServiceMemory(t *testing.T) {
	tests := []struct {
		name      string
		datastore string
		value     string
		refused   bool
	}{
		{name: "memcached takes one", datastore: "memcached", value: "64"},
		{name: "redis takes one", datastore: "redis", value: "64"},
		{name: "elasticsearch takes one", datastore: "elasticsearch", value: "512"},
		{name: "postgres has none", datastore: "postgres", value: "64", refused: true},
		{name: "nothing given to postgres", datastore: "postgres", value: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			datastore, ok := Datastores[test.datastore]
			if !ok {
				t.Fatalf("expected %s to be registered", test.datastore)
			}

			err := CheckServiceMemory(datastore.Definition, test.value)
			if !test.refused {
				if err != nil {
					t.Errorf("expected no error, got %s", err)
				}
				return
			}

			if err == nil || !strings.Contains(err.Error(), "--service-memory") {
				t.Errorf("expected the flag to be named in a refusal, got %v", err)
			}
		})
	}
}

// A new service takes the datastore's default only when it was given nothing,
// so that one given an empty value, as a clone of a service without one is,
// is made without one.
func TestDefaultServiceMemory(t *testing.T) {
	given := func(value string) *string { return &value }

	tests := []struct {
		name      string
		datastore string
		given     *string
		expected  string
	}{
		{name: "memcached given nothing", datastore: "memcached", expected: "64"},
		{name: "memcached given a value", datastore: "memcached", given: given("256"), expected: "256"},
		{name: "memcached given empty", datastore: "memcached", given: given(""), expected: ""},
		{name: "redis given nothing", datastore: "redis", expected: ""},
		{name: "redis given a value", datastore: "redis", given: given("256"), expected: "256"},
		{name: "postgres given nothing", datastore: "postgres", expected: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			datastore, ok := Datastores[test.datastore]
			if !ok {
				t.Fatalf("expected %s to be registered", test.datastore)
			}

			if actual := DefaultServiceMemory(datastore.Definition, test.given); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}
