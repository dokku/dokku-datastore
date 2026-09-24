package internal

import (
	"reflect"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

// cloneSource is a service with every setting a clone copies given a value, so
// that a setting dropped on the way through shows up as a difference
func cloneSource() serviceSettings {
	return serviceSettings{
		ConfigOptions:      "--appendonly yes",
		CustomEnv:          "ONE=1;TWO=2",
		InitialNetwork:     "initial",
		Keyserver:          "keys.example.com",
		LogDriver:          "json-file",
		LogOptions:         []string{"max-size=20m", "max-file=3"},
		Memory:             512,
		PostCreateNetworks: []string{"created-one", "created-two"},
		PostStartNetworks:  []string{"started"},
		RestartPolicy:      "unless-stopped",
		ShmSize:            "128m",
	}
}

// The bug this closes: a clone made without repeating every flag landed on the
// defaults rather than on what the source runs with.
func TestCloneKeepsTheSourceSettings(t *testing.T) {
	source := cloneSource()

	if actual := source.withOverrides(CloneServiceInput{}); !reflect.DeepEqual(actual, source) {
		t.Errorf("expected the source settings %+v, got %+v", source, actual)
	}
}

func TestCloneFlagOverridesOneSetting(t *testing.T) {
	policy := "no"
	networks := []string{"elsewhere"}
	memory := 1024

	tests := []struct {
		name     string
		input    CloneServiceInput
		expected func(*serviceSettings)
	}{
		{
			name:     "a string setting",
			input:    CloneServiceInput{RestartPolicy: &policy},
			expected: func(s *serviceSettings) { s.RestartPolicy = policy },
		},
		{
			name:     "a list setting",
			input:    CloneServiceInput{PostStartNetworks: &networks},
			expected: func(s *serviceSettings) { s.PostStartNetworks = networks },
		},
		{
			name:     "the memory limit",
			input:    CloneServiceInput{Memory: &memory},
			expected: func(s *serviceSettings) { s.Memory = memory },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := cloneSource()
			test.expected(&expected)

			if actual := cloneSource().withOverrides(test.input); !reflect.DeepEqual(actual, expected) {
				t.Errorf("expected %+v, got %+v", expected, actual)
			}
		})
	}
}

// The reason the overrides are pointers: a flag given empty has to be able to
// clear what the source has rather than read as not given.
func TestCloneCanClearASetting(t *testing.T) {
	empty := ""
	none := []string{}
	unlimited := 0

	actual := cloneSource().withOverrides(CloneServiceInput{
		ConfigOptions:      &empty,
		CustomEnv:          &empty,
		InitialNetwork:     &empty,
		LogDriver:          &empty,
		LogOptions:         &none,
		Memory:             &unlimited,
		PostCreateNetworks: &none,
		PostStartNetworks:  &none,
		RestartPolicy:      &empty,
		ShmSize:            &empty,
	})

	// the keyserver has no flag, so it is the one setting that is always copied
	expected := serviceSettings{
		Keyserver:          "keys.example.com",
		LogOptions:         none,
		PostCreateNetworks: none,
		PostStartNetworks:  none,
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %+v, got %+v", expected, actual)
	}
}

func TestReadServiceSettings(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	serviceFiles := service.Files(datastore, "lollipop")
	for filename, contents := range map[string]string{
		serviceFiles.ConfigOptions: "--appendonly yes",
		serviceFiles.Env:           "ONE=1\nTWO=2",
		serviceFiles.Memory:        "512",
		serviceFiles.ShmSize:       "128m",
	} {
		writeInfoFile(t, filename, contents)
	}

	for key, value := range map[string]string{
		"initial-network":             "initial",
		"post-create-network":         "created-one,created-two",
		"post-start-network":          "started",
		service.KeyserverProperty:     "keys.example.com",
		service.LogDriverProperty:     "json-file",
		service.LogOptProperty:        "max-size=20m,max-file=3",
		service.RestartPolicyProperty: "unless-stopped",
	} {
		if err := SetProperty(datastore, "lollipop", key, value); err != nil {
			t.Fatalf("failed to set the %s property: %s", key, err)
		}
	}

	actual, err := readServiceSettings(datastore, "lollipop")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if expected := cloneSource(); !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %+v, got %+v", expected, actual)
	}
}

// A service that never set anything has nothing to copy, and a list property
// that was never written is no entries rather than one empty one.
func TestReadServiceSettingsOfAnUnsetService(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	actual, err := readServiceSettings(datastore, "lollipop")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if expected := (serviceSettings{}); !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected no settings, got %+v", actual)
	}
}

// Read as unlimited, a memory limit that could not be parsed would be quietly
// dropped from the clone.
func TestReadServiceSettingsRefusesAnUnreadableMemory(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	writeInfoFile(t, service.Files(datastore, "lollipop").Memory, "lots")

	if _, err := readServiceSettings(datastore, "lollipop"); err == nil {
		t.Error("expected an unreadable memory limit to be refused")
	}
}
