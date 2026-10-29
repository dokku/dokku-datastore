package internal

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// labeledService is a redis service named lollipop with the given labels
// already written
func labeledService(t *testing.T, labels map[string]string) *service.Datastore {
	t.Helper()

	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	if err := service.WriteLabels(datastore, "lollipop", labels); err != nil {
		t.Fatalf("failed to write the labels: %s", err)
	}

	return datastore
}

// readLabels is what the service has after a command ran
func readLabels(t *testing.T, datastore *service.Datastore) map[string]string {
	t.Helper()

	labels, err := service.ServiceLabels(datastore, "lollipop")
	if err != nil {
		t.Fatalf("failed to read the labels: %s", err)
	}

	return labels
}

func TestLabeledSet(t *testing.T) {
	datastore := service.Datastores["redis"]
	current := map[string]string{"traefik.enable": "false", "com.example.team": "data"}

	tests := []struct {
		name     string
		specs    []string
		expected map[string]string
		message  string
	}{
		{
			name:     "a new label",
			specs:    []string{"com.example.tier=gold"},
			expected: map[string]string{"traefik.enable": "false", "com.example.team": "data", "com.example.tier": "gold"},
			message:  "Set 1 label(s) on lollipop",
		},
		{
			// labelling a key the service has replaces its value rather than
			// being refused, so a value can be changed without unlabelling first
			name:     "a label the service has",
			specs:    []string{"traefik.enable=true"},
			expected: map[string]string{"traefik.enable": "true", "com.example.team": "data"},
			message:  "Set 1 label(s) on lollipop",
		},
		{
			name:     "several labels, one holding a comma",
			specs:    []string{"traefik.enable=true", "traefik.http.routers.a.middlewares=auth,compress"},
			expected: map[string]string{"traefik.enable": "true", "com.example.team": "data", "traefik.http.routers.a.middlewares": "auth,compress"},
			message:  "Set 2 label(s) on lollipop",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			labels, message, err := labeledSet(current, LabelServiceInput{Datastore: datastore, ServiceName: "lollipop", Specs: test.specs})
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if !reflect.DeepEqual(labels, test.expected) {
				t.Errorf("expected %v, got %v", test.expected, labels)
			}

			if message != test.message {
				t.Errorf("expected the message %q, got %q", test.message, message)
			}
		})
	}

	if !reflect.DeepEqual(current, map[string]string{"traefik.enable": "false", "com.example.team": "data"}) {
		t.Errorf("expected the current labels to be left alone, got %v", current)
	}
}

func TestLabeledSetRefuses(t *testing.T) {
	datastore := service.Datastores["redis"]

	tests := []struct {
		name     string
		specs    []string
		expected string
	}{
		{name: "no label", specs: nil, expected: "Must specify at least one label"},
		{name: "an empty label", specs: []string{""}, expected: "Must specify at least one label"},
		{name: "a label without a value", specs: []string{"traefik.enable"}, expected: "<key>=<value>"},
		{name: "a key given twice", specs: []string{"a=1", "a=2"}, expected: "more than once"},
		{name: "a key dokku keeps", specs: []string{"dokku.service=postgres"}, expected: "reserved"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := labeledSet(nil, LabelServiceInput{Datastore: datastore, ServiceName: "lollipop", Specs: test.specs})
			if err == nil {
				t.Fatal("expected the label to be refused")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %q", test.expected, err)
			}
		})
	}
}

func TestLabelServiceWritesTheLabels(t *testing.T) {
	datastore := labeledService(t, map[string]string{"com.example.team": "data"})

	if _, err := LabelService(LabelServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Specs:       []string{"traefik.enable=true"},
	}); err != nil {
		t.Fatalf("failed to label the service: %s", err)
	}

	expected := map[string]string{"com.example.team": "data", "traefik.enable": "true"}
	if actual := readLabels(t, datastore); !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

// One refused label leaves the service's labels as they were, rather than
// writing the ones named before it
func TestLabelServiceIsAllOrNothing(t *testing.T) {
	existing := map[string]string{"com.example.team": "data"}
	datastore := labeledService(t, existing)

	if _, err := LabelService(LabelServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Specs:       []string{"traefik.enable=true", "com.dokku.app-name=lollipop"},
	}); err == nil {
		t.Fatal("expected the label to be refused")
	}

	if actual := readLabels(t, datastore); !reflect.DeepEqual(actual, existing) {
		t.Errorf("expected the labels to be left as %v, got %v", existing, actual)
	}
}

func TestUnlabeledSet(t *testing.T) {
	datastore := service.Datastores["redis"]
	current := map[string]string{"traefik.enable": "true", "com.example.team": "data", "com.example.tier": "gold"}

	tests := []struct {
		name     string
		input    UnlabelServiceInput
		expected map[string]string
		message  string
	}{
		{
			name:     "one label",
			input:    UnlabelServiceInput{Keys: []string{"traefik.enable"}},
			expected: map[string]string{"com.example.team": "data", "com.example.tier": "gold"},
			message:  "Removed 1 label(s) from lollipop",
		},
		{
			name:     "several labels",
			input:    UnlabelServiceInput{Keys: []string{"traefik.enable", "com.example.tier"}},
			expected: map[string]string{"com.example.team": "data"},
			message:  "Removed 2 label(s) from lollipop",
		},
		{
			name:     "every label",
			input:    UnlabelServiceInput{All: true},
			expected: map[string]string{},
			message:  "Removed all labels on lollipop",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.input.Datastore = datastore
			test.input.ServiceName = "lollipop"

			labels, message, err := unlabeledSet(current, test.input)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if len(labels) != len(test.expected) || (len(labels) > 0 && !reflect.DeepEqual(labels, test.expected)) {
				t.Errorf("expected %v, got %v", test.expected, labels)
			}

			if message != test.message {
				t.Errorf("expected the message %q, got %q", test.message, message)
			}
		})
	}
}

func TestUnlabeledSetRefuses(t *testing.T) {
	datastore := service.Datastores["redis"]
	current := map[string]string{"traefik.enable": "true"}

	tests := []struct {
		name     string
		input    UnlabelServiceInput
		expected string
	}{
		{name: "no label", input: UnlabelServiceInput{}, expected: "Must specify at least one label, use redis:unlabel --all to remove all labels"},
		{name: "a label with all", input: UnlabelServiceInput{All: true, Keys: []string{"traefik.enable"}}, expected: "A label cannot be specified with --all"},
		{name: "a label the service does not have", input: UnlabelServiceInput{Keys: []string{"com.example.team"}}, expected: "Label com.example.team does not exist on lollipop"},
		{name: "a label named with its value", input: UnlabelServiceInput{Keys: []string{"traefik.enable=true"}}, expected: "removed by its key alone, as traefik.enable"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.input.Datastore = datastore
			test.input.ServiceName = "lollipop"

			_, _, err := unlabeledSet(current, test.input)
			if err == nil {
				t.Fatal("expected the unlabel to be refused")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %q", test.expected, err)
			}
		})
	}
}

// One key naming a label the service does not have leaves every other one in
// place, rather than removing the ones named before it
func TestUnlabelServiceIsAllOrNothing(t *testing.T) {
	existing := map[string]string{"traefik.enable": "true", "com.example.team": "data"}
	datastore := labeledService(t, existing)

	if _, err := UnlabelService(UnlabelServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Keys:        []string{"traefik.enable", "com.example.tier"},
	}); err == nil {
		t.Fatal("expected the unlabel to be refused")
	}

	if actual := readLabels(t, datastore); !reflect.DeepEqual(actual, existing) {
		t.Errorf("expected the labels to be left as %v, got %v", existing, actual)
	}
}

// Removing every label clears the property, and doing it again is not an error
func TestUnlabelServiceAllClearsTheLabels(t *testing.T) {
	datastore := labeledService(t, map[string]string{"traefik.enable": "true"})

	for range 2 {
		if _, err := UnlabelService(UnlabelServiceInput{Datastore: datastore, ServiceName: "lollipop", All: true}); err != nil {
			t.Fatalf("failed to remove every label: %s", err)
		}

		if actual := readLabels(t, datastore); len(actual) != 0 {
			t.Errorf("expected no labels, got %v", actual)
		}
	}
}

// A label the plugin keeps is refused where the command starts, before the
// service root and its credentials are written.
func TestCreateServiceRefusesAReservedLabel(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	err := CreateService(t.Context(), CreateServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Labels:      map[string]string{"dokku.service": "postgres"},
	})
	if err == nil {
		t.Fatal("expected a reserved label to be refused, got no error")
	}

	if !strings.Contains(err.Error(), "dokku.service") {
		t.Errorf("expected the error to name the label, got %q", err)
	}

	if root := service.Folders(datastore, "lollipop").Root; common.DirectoryExists(root) {
		t.Errorf("a refused create left %s behind", root)
	}
}
