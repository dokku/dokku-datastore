package internal

import (
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/datastores"
)

func TestSetPropertyRejectsUnknownKeys(t *testing.T) {
	datastore := datastores.Datastores["redis"]

	// an unknown key must be refused before anything is written, and the message
	// has to name the keys that are accepted
	err := SetProperty(datastore, "lollipop", "not-a-property", "value")
	if err == nil {
		t.Fatal("expected an error for an unknown key, got none")
	}

	expected := "Invalid key specified, valid keys include: initial-network, post-create-network, post-start-network"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err)
	}
}

func TestSettableProperties(t *testing.T) {
	// the bash datastore plugins accept exactly these three
	expected := []string{"initial-network", "post-create-network", "post-start-network"}
	if strings.Join(SettableProperties, ",") != strings.Join(expected, ",") {
		t.Errorf("expected %v, got %v", expected, SettableProperties)
	}
}
