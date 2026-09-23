package internal

import (
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

func TestSetPropertyRejectsUnknownKeys(t *testing.T) {
	datastore := service.Datastores["redis"]

	// an unknown key must be refused before anything is written, and the message
	// has to name the keys that are accepted
	err := SetProperty(datastore, "lollipop", "not-a-property", "value")
	if err == nil {
		t.Fatal("expected an error for an unknown key, got none")
	}

	expected := "Invalid key specified, valid keys include: initial-network, post-create-network, post-start-network, backup-keyserver, log-driver, log-opt"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err)
	}
}

func TestSettableProperties(t *testing.T) {
	// the three the bash datastore plugins accept, the keyserver the backup image
	// is told to fetch a public key from, which has nowhere else to be set, and
	// the two that bound a container's log
	expected := []string{"initial-network", "post-create-network", "post-start-network", "backup-keyserver", "log-driver", "log-opt"}
	if strings.Join(SettableProperties, ",") != strings.Join(expected, ",") {
		t.Errorf("expected %v, got %v", expected, SettableProperties)
	}
}

// The property name is written in two places that have to agree: the list that
// makes it settable, and the read in the backup path.
func TestKeyserverPropertyIsSettable(t *testing.T) {
	if !slices.Contains(SettableProperties, service.KeyserverProperty) {
		t.Errorf("expected %s to be settable, got %v", service.KeyserverProperty, SettableProperties)
	}

	// the message listing valid keys is built from the slice, so it says so
	if !strings.Contains(InvalidPropertyError().Error(), service.KeyserverProperty) {
		t.Errorf("expected the error to list it, got %q", InvalidPropertyError())
	}
}

// The log properties are the first whose value is checked rather than only
// whose key is, because docker refuses to make a container from a malformed one
// and the service would be left unable to start by a command that said it had
// worked.
func TestSetPropertyRejectsAnUnusableValue(t *testing.T) {
	datastore := service.Datastores["redis"]

	tests := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{
			name:     "a driver that is not a name",
			key:      service.LogDriverProperty,
			value:    "not a driver",
			expected: `invalid log-driver value "not a driver"`,
		},
		{
			name:     "an option that is not a pair",
			key:      service.LogOptProperty,
			value:    "max-size",
			expected: `invalid log-opt entry "max-size"`,
		},
		{
			name:     "a max-size with no unit",
			key:      service.LogOptProperty,
			value:    "max-size=20",
			expected: `invalid max-size value "20"`,
		},
		{
			name:     "a max-file that is not a count",
			key:      service.LogOptProperty,
			value:    "max-size=20m,max-file=none",
			expected: `invalid max-file value "none"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := SetProperty(datastore, "lollipop", test.key, test.value)
			if err == nil {
				t.Fatalf("expected %s=%s to be refused, got no error", test.key, test.value)
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected the error to contain %q, got %q", test.expected, err)
			}
		})
	}
}

// Unsetting is how every other property is cleared, so an empty value has to
// reach the delete rather than being refused as an unusable one.
func TestSetPropertyAcceptsAnEmptyLogValue(t *testing.T) {
	for _, key := range []string{service.LogDriverProperty, service.LogOptProperty} {
		if err := ValidatePropertyValue(key, ""); err != nil {
			t.Errorf("expected an empty %s to be accepted, got %q", key, err)
		}
	}
}
