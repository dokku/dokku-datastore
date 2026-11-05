package service

import (
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/hostenv"
)

func TestValidateAmbassadorImage(t *testing.T) {
	valid := []string{
		"",
		"dokku/ambassador:0.8.4",
		" dokku/ambassador:0.8.2 ",
		"registry.example.com:5000/ambassador:1.0",
		"dokku/ambassador@sha256:b6aa8ebd1e659ff77463b5319a81a6a125297f4e757b6a13ead6b0b2b90543f2",
	}
	for _, value := range valid {
		if err := ValidateAmbassadorImage(value); err != nil {
			t.Errorf("expected %q to be accepted, got %q", value, err)
		}
	}

	// an image with no tag floats, which the images the plugin runs never do
	invalid := []string{"dokku/ambassador", "registry.example.com:5000/ambassador", "Dokku/Ambassador:0.8.4", "dokku/ambassador:", "dokku ambassador:0.8.4"}
	for _, value := range invalid {
		err := ValidateAmbassadorImage(value)
		if err == nil {
			t.Errorf("expected %q to be refused, got no error", value)
			continue
		}

		if !strings.Contains(err.Error(), AmbassadorImageProperty) {
			t.Errorf("expected the error to name %s, got %q", AmbassadorImageProperty, err)
		}
	}
}

// The variable wins, since the --ambassador-image flag sets it, then the global
// property, then the image the plugin pins.
func TestAmbassadorImagePrecedence(t *testing.T) {
	tests := []struct {
		name     string
		global   string
		env      string
		expected string
	}{
		{name: "nothing set anywhere", expected: hostenv.DefaultAmbassadorImage},
		{name: "the global property", global: "dokku/ambassador:0.8.2", expected: "dokku/ambassador:0.8.2"},
		{name: "the variable", env: "example/ambassador:1.0", expected: "example/ambassador:1.0"},
		{name: "the variable over the global property", global: "dokku/ambassador:0.8.2", env: "example/ambassador:1.0", expected: "example/ambassador:1.0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			redis := redisDatastore(t)
			withServiceRoot(t, redis, "lollipop")
			t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)
			t.Setenv(hostenv.AmbassadorImageVariable, test.env)

			if err := WriteGlobalProperty(redis, AmbassadorImageProperty, test.global); err != nil {
				t.Fatalf("failed to write the global property: %v", err)
			}

			image, err := AmbassadorImage(redis)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if image != test.expected {
				t.Errorf("expected %q, got %q", test.expected, image)
			}
		})
	}
}

// A variable naming an image with no tag is refused rather than skipped, since
// skipping it would publish the service through an image nobody asked for, and
// the error says which variable to fix.
func TestAmbassadorImageRefusesAMalformedVariable(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)
	t.Setenv(hostenv.AmbassadorImageVariable, "dokku/ambassador")

	_, err := AmbassadorImage(redis)
	if err == nil {
		t.Fatal("expected a malformed variable to be refused")
	}

	if !strings.Contains(err.Error(), hostenv.AmbassadorImageVariable) {
		t.Errorf("expected the error to name the variable, got %q", err)
	}
}

// A global property naming an image with no tag is refused the same way, and
// the error says it was the global property.
func TestAmbassadorImageRefusesAMalformedGlobalProperty(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)
	t.Setenv(hostenv.AmbassadorImageVariable, "")

	if err := WriteGlobalProperty(redis, AmbassadorImageProperty, "dokku/ambassador"); err != nil {
		t.Fatalf("failed to write the global property: %v", err)
	}

	_, err := AmbassadorImage(redis)
	if err == nil {
		t.Fatal("expected a malformed global property to be refused")
	}

	if !strings.Contains(err.Error(), "the global ambassador-image property") {
		t.Errorf("expected the error to name the global property, got %q", err)
	}
}

// The network an ambassador joins is the one its service container was made
// on, and none for a container that has no network of its own to share.
func TestAmbassadorNetwork(t *testing.T) {
	tests := map[string]string{
		"bridge":           "bridge",
		"default":          "bridge",
		"my-network":       "my-network",
		"host":             "",
		"none":             "",
		"container:abc123": "",
		"":                 "",
	}

	for mode, expected := range tests {
		if actual := ambassadorNetwork(mode); actual != expected {
			t.Errorf("expected network mode %q to give %q, got %q", mode, expected, actual)
		}
	}
}
