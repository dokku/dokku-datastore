package internal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
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

// More ports than a datastore has are refused, and the refusal names the order
// they are published in, with the tls ports rabbitmq gained last. Fewer are
// allowed down to its primary port, which an expose written before rabbitmq
// served tls passes.
func TestExposeServiceRefusesMorePortsThanTheDatastoreHas(t *testing.T) {
	datastore := service.Datastores["rabbitmq"]
	withPortFile(t, datastore, "lollipop", nil)

	err := ExposeService(context.Background(), ExposeServiceInput{
		Datastore:   datastore,
		Ports:       []string{"1", "2", "3", "4", "5", "6", "7"},
		ServiceName: "lollipop",
	})
	if err == nil {
		t.Fatal("expected seven ports to be refused")
	}

	expected := "7 ports to be exposed need to be provided in the following order: 5672,4369,35197,15672,5671,15671"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err)
	}

	if IsExposed(datastore, "lollipop") {
		t.Error("expected no port file after the refusal")
	}
}

func ptr(s string) *string {
	return &s
}

// A service exposed directly has no ambassador to hold its clients to a
// port-source-range, so it is refused rather than exposed with the range
// quietly ignored, and before the port file is written.
func TestExposeServiceRefusesADirectSourceRange(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	commandPrefix := datastore.Properties().CommandPrefix
	if err := common.PropertyWrite(commandPrefix, "lollipop", service.ExposeModeProperty, service.ExposeModeDirect); err != nil {
		t.Fatalf("failed to write the property: %v", err)
	}
	if err := common.PropertyWrite(commandPrefix, "lollipop", service.PortSourceRangeProperty, "10.0.0.0/8"); err != nil {
		t.Fatalf("failed to write the property: %v", err)
	}

	err := ExposeService(context.Background(), ExposeServiceInput{
		Datastore:   datastore,
		Ports:       []string{"1234"},
		ServiceName: "lollipop",
	})
	if err == nil {
		t.Fatal("expected a direct expose with a source range to be refused")
	}

	if !strings.Contains(err.Error(), "cannot be enforced") {
		t.Errorf("expected the refusal to say why, got %q", err)
	}

	if IsExposed(datastore, "lollipop") {
		t.Error("expected no port file after the refusal")
	}
}
