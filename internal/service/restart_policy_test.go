package service

import (
	"strings"
	"testing"
)

func TestValidateRestartPolicy(t *testing.T) {
	valid := []string{"", "no", "always", "unless-stopped", "on-failure", "on-failure:0", "on-failure:5", " always "}
	for _, value := range valid {
		if err := ValidateRestartPolicy(value); err != nil {
			t.Errorf("expected %q to be accepted, got %q", value, err)
		}
	}

	// on-failure:abc is accepted by dokku core and refused by docker, which is
	// the one place this is stricter than core
	invalid := []string{"sometimes", "Always", "on-failure:", "on-failure:abc", "on-failure:-1", "always:3", "no:1"}
	for _, value := range invalid {
		if err := ValidateRestartPolicy(value); err == nil {
			t.Errorf("expected %q to be refused, got no error", value)
		}
	}
}

func TestValidateRestartPolicyNamesTheProperty(t *testing.T) {
	err := ValidateRestartPolicy("sometimes")
	if err == nil {
		t.Fatal("expected an error")
	}

	if !strings.Contains(err.Error(), RestartPolicyProperty) {
		t.Errorf("expected the error to name %s, got %q", RestartPolicyProperty, err)
	}
}
