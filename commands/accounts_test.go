package commands

import (
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal"
	"github.com/dokku/dokku-datastore/internal/service"
)

// documentedFlag reports whether a command lists a flag for a datastore
func documentedFlag(t *testing.T, documenter internal.PluginCommand, data internal.DocumentationData, flag string) bool {
	t.Helper()

	flags, err := internal.DocumentedFlags(documenter, data)
	if err != nil {
		t.Fatalf("unable to document the %s flags: %s", documenter.Name(), err)
	}

	for _, f := range flags {
		if strings.Contains(f.Label, flag) {
			return true
		}
	}

	return false
}

// --database and --user are documented only for a datastore that takes them,
// and the export, import and reset --database only for one that makes its
// databases: redis's sixteen are dumped and flushed all at once
func TestAccountFlagsAreDocumentedWhereTheyAreTaken(t *testing.T) {
	tests := map[string]struct {
		database      bool
		user          bool
		dumpsDatabase bool
	}{
		"postgres":  {database: true, user: true, dumpsDatabase: true},
		"mongo":     {database: true, user: true, dumpsDatabase: true},
		"redis":     {database: true, user: true},
		"solr":      {database: true, dumpsDatabase: true},
		"memcached": {},
	}

	for name, expected := range tests {
		t.Run(name, func(t *testing.T) {
			data := internal.NewDocumentationData(internal.DocumentationDataInput{Datastore: service.Datastores[name]})

			for _, documenter := range []internal.PluginCommand{&LinkCommand{}, &ConnectCommand{}} {
				if actual := documentedFlag(t, documenter, data, "--database"); actual != expected.database {
					t.Errorf("expected %s to list --database to be %t, got %t", documenter.Name(), expected.database, actual)
				}

				if actual := documentedFlag(t, documenter, data, "--user"); actual != expected.user {
					t.Errorf("expected %s to list --user to be %t, got %t", documenter.Name(), expected.user, actual)
				}
			}

			for _, documenter := range []internal.PluginCommand{&ExportCommand{}, &ImportCommand{}, &ResetCommand{}} {
				if actual := documentedFlag(t, documenter, data, "--database"); actual != expected.dumpsDatabase {
					t.Errorf("expected %s to list --database to be %t, got %t", documenter.Name(), expected.dumpsDatabase, actual)
				}
			}

			link := &LinkCommand{}
			documentation, err := internal.RenderDocumentation(link.Documentation(), data)
			if err != nil {
				t.Fatalf("unable to render the link documentation: %s", err)
			}

			if actual := strings.Contains(documentation, "--database"); actual != expected.database {
				t.Errorf("expected link to document --database to be %t, got %t:\n%s", expected.database, actual, documentation)
			}

			if actual := strings.Contains(documentation, "--user"); actual != expected.user {
				t.Errorf("expected link to document --user to be %t, got %t:\n%s", expected.user, actual, documentation)
			}
		})
	}
}

// a datastore with a fixed set of databases documents linking to one by index
func TestLinkDocumentsAFixedDatabaseByIndex(t *testing.T) {
	data := internal.NewDocumentationData(internal.DocumentationDataInput{Datastore: service.Datastores["redis"]})

	link := &LinkCommand{}
	documentation, err := internal.RenderDocumentation(link.Documentation(), data)
	if err != nil {
		t.Fatalf("unable to render the link documentation: %s", err)
	}

	for _, expected := range []string{"16 databases, numbered from 0", "--database 3"} {
		if !strings.Contains(documentation, expected) {
			t.Errorf("expected the link documentation to contain %q:\n%s", expected, documentation)
		}
	}

	if strings.Contains(documentation, "create-database") {
		t.Errorf("expected no mention of create-database for redis:\n%s", documentation)
	}
}

// every new command renders for a datastore it is documented for
func TestAccountCommandsRender(t *testing.T) {
	data := internal.NewDocumentationData(internal.DocumentationDataInput{Datastore: service.Datastores["postgres"]})

	for _, documenter := range []internal.PluginCommand{
		&CreateDatabaseCommand{}, &DestroyDatabaseCommand{}, &CreateUserCommand{}, &DestroyUserCommand{}, &SetUserPasswordCommand{},
	} {
		t.Run(documenter.Name(), func(t *testing.T) {
			for _, body := range []string{documenter.Description(), documenter.Usage(), documenter.Documentation()} {
				rendered, err := internal.RenderDocumentation(body, data)
				if err != nil {
					t.Fatalf("unable to render %q: %s", body, err)
				}

				if strings.Contains(rendered, "{{") {
					t.Errorf("expected %q to render, got %q", body, rendered)
				}
			}

			if !strings.Contains(documenter.Documentation(), `dokku {{.Command "`+documenter.Name()+`"}} lollipop`) {
				t.Errorf("expected %s to document an example of itself", documenter.Name())
			}
		})
	}
}
