package internal

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

// withPortFile points service.DokkuLibRoot at a temporary directory and, when
// contents is non-nil, writes them to the service's port file. DokkuLibRoot is a
// package level variable assigned in init(), so it has to be swapped directly
// rather than through the environment, which means these tests cannot run in
// parallel.
func withPortFile(t *testing.T, s *service.Datastore, serviceName string, contents *string) {
	t.Helper()

	previous := service.DokkuLibRoot
	service.DokkuLibRoot = t.TempDir()
	t.Cleanup(func() {
		service.DokkuLibRoot = previous
	})

	serviceRoot := service.Folders(s, serviceName).Root
	if err := os.MkdirAll(serviceRoot, 0755); err != nil {
		t.Fatalf("failed to create service root: %v", err)
	}

	if contents == nil {
		return
	}

	if err := os.WriteFile(filepath.Join(serviceRoot, "PORT"), []byte(*contents), 0644); err != nil {
		t.Fatalf("failed to write port file: %v", err)
	}
}

func TestIsExposed(t *testing.T) {
	tests := []struct {
		name     string
		portFile *string
		expected bool
	}{
		{
			name:     "no port file",
			portFile: nil,
			expected: false,
		},
		{
			name:     "empty port file",
			portFile: ptr(""),
			expected: false,
		},
		{
			name:     "a single port",
			portFile: ptr("33201\n"),
			expected: true,
		},
	}

	datastore := service.Datastores["redis"]
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withPortFile(t, datastore, "lollipop", test.portFile)
			if actual := IsExposed(datastore, "lollipop"); actual != test.expected {
				t.Errorf("expected %t, got %t", test.expected, actual)
			}
		})
	}
}

func TestConfiguredPorts(t *testing.T) {
	tests := []struct {
		name     string
		portFile *string
		expected string
	}{
		{
			name:     "no port file",
			portFile: nil,
			expected: "",
		},
		{
			name:     "empty port file",
			portFile: ptr(""),
			expected: "",
		},
		{
			name:     "a single port",
			portFile: ptr("33201\n"),
			expected: "33201",
		},
		{
			name:     "several ports",
			portFile: ptr("33201 33202\n"),
			expected: "33201 33202",
		},
	}

	datastore := service.Datastores["redis"]
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withPortFile(t, datastore, "lollipop", test.portFile)
			if actual := ConfiguredPorts(datastore, "lollipop"); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

func TestAlreadyExposedError(t *testing.T) {
	// The bash datastore plugins emit this exact string, and downstream plugin
	// test suites assert on it.
	datastore := service.Datastores["redis"]
	withPortFile(t, datastore, "ls", ptr("6379\n"))

	expected := "Service ls already exposed on port(s) 6379"
	if actual := AlreadyExposedError(datastore, "ls"); actual.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, actual)
	}
}

// A port the ambassador cannot publish is refused before the port file is
// written, since that file alone is what says a service is exposed.
func TestExposeServiceRefusesAnInvalidPort(t *testing.T) {
	datastore := service.Datastores["redis"]
	for _, port := range []string{"0", "65536", "port", "localhost:6379", "::1:6379"} {
		t.Run(port, func(t *testing.T) {
			withPortFile(t, datastore, "lollipop", nil)

			err := ExposeService(context.Background(), ExposeServiceInput{
				Datastore:   datastore,
				Ports:       []string{port},
				ServiceName: "lollipop",
			})
			if err == nil {
				t.Fatalf("expected %q to be refused", port)
			}

			if IsExposed(datastore, "lollipop") {
				t.Errorf("expected no port file after refusing %q", port)
			}
		})
	}
}

func ptr(s string) *string {
	return &s
}
