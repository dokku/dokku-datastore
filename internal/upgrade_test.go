package internal

import "testing"

// An upgrade that names only an image is nothing to do when the service already
// runs it. One that also changes a setting is a recreate whatever the image says,
// so the short circuit has to know the difference.
func TestUpgradeChangesSettings(t *testing.T) {
	value := "something"
	list := []string{"a-network"}

	tests := []struct {
		name     string
		input    UpgradeServiceInput
		expected bool
	}{
		{name: "nothing but an image", input: UpgradeServiceInput{Image: "redis", ImageVersion: "8.8.0"}},
		{name: "config options", input: UpgradeServiceInput{ConfigOptions: &value}, expected: true},
		{name: "custom env", input: UpgradeServiceInput{CustomEnv: &value}, expected: true},
		{name: "initial network", input: UpgradeServiceInput{InitialNetwork: &value}, expected: true},
		{name: "post create networks", input: UpgradeServiceInput{PostCreateNetworks: &list}, expected: true},
		{name: "post start networks", input: UpgradeServiceInput{PostStartNetworks: &list}, expected: true},
		{name: "shm size", input: UpgradeServiceInput{ShmSize: &value}, expected: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := test.input.changesSettings(); actual != test.expected {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

// Clearing a setting is a change like any other, and the reason these are
// pointers: an empty string has to be able to mean "set this to nothing" rather
// than "this was not asked about".
func TestUpgradeCanClearASetting(t *testing.T) {
	empty := ""
	if !(UpgradeServiceInput{ConfigOptions: &empty}).changesSettings() {
		t.Error("expected clearing config options to count as a change")
	}
}
