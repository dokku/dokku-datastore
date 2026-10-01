package service

import (
	"errors"
	"slices"
	"testing"
)

// A dump of the service's own database leaves the command to the verb's name,
// and one of every database runs the form the definition declares for it.
func TestDumpCommandChoosesTheEveryDatabaseForm(t *testing.T) {
	mysql := mysqlDatastore(t)

	for _, name := range []string{"export", "import"} {
		command, err := mysql.dumpCommand(name, false)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if command != nil {
			t.Errorf("expected %s of the service's own database to run the verb's own command, got %q", name, command.Exec)
		}

		command, err = mysql.dumpCommand(name, true)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if command == nil {
			t.Fatalf("expected %s of every database to run a command of its own", name)
		}

		declared, _ := mysql.Definition.CommandFor(name)
		if !slices.Equal(command.Exec, declared.AllDatabases.Exec) {
			t.Errorf("expected %s of every database to run %q, got %q", name, declared.AllDatabases.Exec, command.Exec)
		}
	}
}

// redis dumps the whole server already, so it declares no every-database form,
// and asking for one is refused rather than handed the single dump.
func TestDumpCommandRefusesEveryDatabaseWhereNoneIsDeclared(t *testing.T) {
	redis, ok := Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	if redis.ExportsAllDatabases() {
		t.Error("expected redis not to export every database")
	}

	for _, name := range []string{"export", "import"} {
		_, err := redis.dumpCommand(name, true)

		var refused ErrAllDatabasesUnsupported
		if !errors.As(err, &refused) {
			t.Fatalf("expected %s of every database to be refused, got %v", name, err)
		}
		if refused.Name != name || refused.Plugin != "redis" {
			t.Errorf("expected the refusal to name redis %s, got %+v", name, refused)
		}
	}
}

func TestExportsAllDatabases(t *testing.T) {
	for _, name := range []string{"clickhouse", "couchdb", "mariadb", "mongo", "mysql", "postgres"} {
		datastore, ok := Datastores[name]
		if !ok {
			t.Fatalf("expected %s to be registered", name)
		}

		if !datastore.ExportsAllDatabases() {
			t.Errorf("expected %s to export every database", name)
		}
	}
}
