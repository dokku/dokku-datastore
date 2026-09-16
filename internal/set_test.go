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

	expected := "Invalid key specified, valid keys include: initial-network, post-create-network, post-start-network, backup-keyserver"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err)
	}
}

func TestSettableProperties(t *testing.T) {
	// the three the bash datastore plugins accept, and the keyserver the backup
	// image is told to fetch a public key from, which has nowhere else to be set
	expected := []string{"initial-network", "post-create-network", "post-start-network", "backup-keyserver"}
	if strings.Join(SettableProperties, ",") != strings.Join(expected, ",") {
		t.Errorf("expected %v, got %v", expected, SettableProperties)
	}
}

// The property name is written in two places that have to agree: the list that
// makes it settable, and the read in the backup path.
func TestKeyserverPropertyIsSettable(t *testing.T) {
	if !slices.Contains(SettableProperties, KeyserverProperty) {
		t.Errorf("expected %s to be settable, got %v", KeyserverProperty, SettableProperties)
	}

	// the message listing valid keys is built from the slice, so it says so
	if !strings.Contains(InvalidPropertyError().Error(), KeyserverProperty) {
		t.Errorf("expected the error to list it, got %q", InvalidPropertyError())
	}
}
