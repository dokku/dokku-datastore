package internal

import (
	"errors"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
)

// withSysctl decides what the machine reports, so the check is tested rather
// than the machine it is tested on: a developer's machine may not have the
// parameter at all.
func withSysctl(t *testing.T, values map[string]int) {
	t.Helper()

	previous := sysctlReader
	sysctlReader = func(name string) (int, error) {
		value, ok := values[name]
		if !ok {
			return 0, errors.New("no such parameter")
		}

		return value, nil
	}

	t.Cleanup(func() {
		sysctlReader = previous
	})
}

func TestCheckRequirements(t *testing.T) {
	withSysctl(t, map[string]int{"vm.max_map_count": 262144})

	tests := []struct {
		name         string
		requirements []definition.Requirement
		refused      bool
	}{
		{
			name:         "nothing required",
			requirements: nil,
		},
		{
			// every machine has more than one of these
			name:         "a requirement the machine meets",
			requirements: []definition.Requirement{{Sysctl: "vm.max_map_count", Minimum: 262144}},
		},
		{
			name:         "a requirement the machine cannot meet",
			requirements: []definition.Requirement{{Sysctl: "vm.max_map_count", Minimum: 262145, Message: "raise it"}},
			refused:      true,
		},
		{
			// a reading that could not be taken is not a failed requirement,
			// and refusing to create a service over one would be worse than
			// trying
			name:         "a parameter the machine does not have",
			requirements: []definition.Requirement{{Sysctl: "vm.not.a.real.parameter", Minimum: 1}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := CheckRequirements(test.requirements)
			if test.refused && err == nil {
				t.Fatal("expected the requirement to be refused")
			}

			if !test.refused && err != nil {
				t.Fatalf("unexpected refusal: %s", err)
			}

			// knowing a number is too low is not knowing what to do about it
			if test.refused && !strings.Contains(err.Error(), "raise it") {
				t.Errorf("expected the message to say how to fix it, got %q", err)
			}
		})
	}
}
