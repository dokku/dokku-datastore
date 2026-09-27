package internal

import (
	"context"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

// A service that is not exposed has no ports to reexpose, and is refused before
// anything touches docker. An empty port file is a service that is not exposed,
// as it is to expose.
func TestReexposeServiceRefusesAServiceThatIsNotExposed(t *testing.T) {
	datastore := service.Datastores["redis"]

	for name, contents := range map[string]*string{"no port file": nil, "an empty port file": ptr("")} {
		t.Run(name, func(t *testing.T) {
			withPortFile(t, datastore, "lollipop", contents)

			err := ReexposeService(context.Background(), ReexposeServiceInput{
				Datastore:   datastore,
				ServiceName: "lollipop",
			})
			if err == nil || err.Error() != "Service lollipop is not exposed" {
				t.Fatalf("expected the service to be refused as not exposed, got %v", err)
			}
		})
	}
}
