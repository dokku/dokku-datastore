package commands

import (
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"
)

// --jdbc is documented, in the prose and the flag list, only for a datastore
// with a jdbc driver, and unlink says it unsets what such a link set.
func TestJDBCIsDocumentedWhereItIsTaken(t *testing.T) {
	for name, expected := range map[string]bool{"postgres": true, "mysql": true, "mariadb": true, "redis": false} {
		t.Run(name, func(t *testing.T) {
			data := internal.NewDocumentationData(internal.DocumentationDataInput{Datastore: service.Datastores[name]})

			link := &LinkCommand{}
			documentation, err := internal.RenderDocumentation(link.Documentation(), data)
			if err != nil {
				t.Fatalf("unable to render the link documentation: %s", err)
			}

			if actual := strings.Contains(documentation, "--jdbc"); actual != expected {
				t.Errorf("expected link to document --jdbc to be %t, got %t:\n%s", expected, actual, documentation)
			}

			// the example names the driver and account the link really sets
			if expected && !strings.Contains(documentation, "JDBC_"+data.DefaultAlias+"_URL=jdbc:") {
				t.Errorf("expected link to show the jdbc url it sets, got:\n%s", documentation)
			}

			flags, err := internal.DocumentedFlags(link, data)
			if err != nil {
				t.Fatalf("unable to document the link flags: %s", err)
			}

			documented := false
			for _, f := range flags {
				if strings.Contains(f.Label, "--jdbc") {
					documented = true
				}
			}

			if documented != expected {
				t.Errorf("expected the link flags to list --jdbc to be %t, got %t", expected, documented)
			}

			unlink := &UnlinkCommand{}
			documentation, err = internal.RenderDocumentation(unlink.Documentation(), data)
			if err != nil {
				t.Fatalf("unable to render the unlink documentation: %s", err)
			}

			if actual := strings.Contains(documentation, "--jdbc"); actual != expected {
				t.Errorf("expected unlink to document --jdbc to be %t, got %t:\n%s", expected, actual, documentation)
			}
		})
	}
}
