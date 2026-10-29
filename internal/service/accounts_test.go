package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// datastoreNamed is one of the embedded datastores, on its newest definition
func datastoreNamed(t *testing.T, name string) *Datastore {
	t.Helper()

	found, ok := Datastores[name]
	if !ok {
		t.Fatalf("expected a %s datastore", name)
	}

	return found
}

// withAccountService is a service of a datastore with a password and the
// databases and users given recorded beside its own
func withAccountService(t *testing.T, s *Datastore, databases []string, users map[string]string) {
	t.Helper()

	withServiceRoot(t, s, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	if err := WriteDatabaseName(WriteDatabaseNameInput{Datastore: s, ServiceName: "lollipop"}); err != nil {
		t.Fatalf("failed to write the database name: %s", err)
	}

	for name, secret := range s.Definition.Dokku.Secrets {
		if err := os.WriteFile(s.SecretFile("lollipop", secret), []byte("hunter2-"+name), 0600); err != nil {
			t.Fatalf("failed to write the %s secret: %s", name, err)
		}
	}

	for _, database := range databases {
		if err := recordFile(Folders(s, "lollipop").Databases, database, database); err != nil {
			t.Fatalf("failed to record database %s: %s", database, err)
		}
	}

	for user, password := range users {
		if err := recordFile(Folders(s, "lollipop").Auth, user, password); err != nil {
			t.Fatalf("failed to record user %s: %s", user, err)
		}
	}
}

func TestDatabasesAndUsersAreRecorded(t *testing.T) {
	postgres := datastoreNamed(t, "postgres")
	withAccountService(t, postgres, []string{"paintshop", "atelier"}, map[string]string{"painter": "brush"})

	// a file the tool did not write is not handed to a datastore as a name
	if err := os.WriteFile(filepath.Join(Folders(postgres, "lollipop").Databases, ".paintshop.123"), []byte(""), 0600); err != nil {
		t.Fatalf("failed to write a stray file: %s", err)
	}

	if actual := Databases(postgres, "lollipop"); !slices.Equal(actual, []string{"lollipop", "atelier", "paintshop"}) {
		t.Errorf("expected the service's own database first and the rest sorted, got %v", actual)
	}

	if actual := Users(postgres, "lollipop"); !slices.Equal(actual, []string{"postgres", "painter"}) {
		t.Errorf("expected the service's own user and the one made, got %v", actual)
	}

	if actual := UserPassword(postgres, "lollipop", "painter"); actual != "brush" {
		t.Errorf("expected the made user's password, got %q", actual)
	}

	if actual := UserPassword(postgres, "lollipop", "postgres"); actual != "hunter2-password" {
		t.Errorf("expected the service's own password, got %q", actual)
	}

	if actual := UserPassword(postgres, "lollipop", "nobody"); actual != "" {
		t.Errorf("expected no password for a user the service does not hold, got %q", actual)
	}

	info, err := os.Stat(filepath.Join(Folders(postgres, "lollipop").Auth, "painter"))
	if err != nil {
		t.Fatalf("expected the user to be recorded: %s", err)
	}
	if info.Mode().Perm() != PrivateFileMode {
		t.Errorf("expected the password to be private, got %v", info.Mode().Perm())
	}
}

// redis has sixteen databases, addressed by index, and none to make
func TestFixedDatabases(t *testing.T) {
	redis := datastoreNamed(t, "redis")
	withAccountService(t, redis, nil, nil)

	databases := Databases(redis, "lollipop")
	if len(databases) != 16 || databases[0] != "0" || databases[15] != "15" {
		t.Errorf("expected the indexes 0 to 15, got %v", databases)
	}

	if actual := DatabasesSummary(redis, "lollipop"); actual != "0-15" {
		t.Errorf("expected the range 0-15, got %q", actual)
	}

	if actual := redis.DefaultDatabase("lollipop"); actual != "0" {
		t.Errorf("expected the default index 0, got %q", actual)
	}

	if err := ValidateNewDatabase(redis, "lollipop", "paintshop"); err == nil || !strings.Contains(err.Error(), "does not support creating databases") {
		t.Errorf("expected creating a database to be refused, got %v", err)
	}

	for _, database := range []string{"16", "-1", "first"} {
		if err := ValidateAccount(redis, "lollipop", Account{Database: database}); err == nil || !strings.Contains(err.Error(), "numbered from 0") {
			t.Errorf("expected database %s to be refused, got %v", database, err)
		}
	}

	if err := ValidateAccount(redis, "lollipop", Account{Database: "3"}); err != nil {
		t.Errorf("expected database 3 to be accepted, got %s", err)
	}
}

func TestValidateNewDatabase(t *testing.T) {
	postgres := datastoreNamed(t, "postgres")
	withAccountService(t, postgres, []string{"paintshop"}, nil)

	tests := []struct {
		name     string
		database string
		expected string
	}{
		{name: "an uppercase letter", database: "Paintshop", expected: "invalid database name"},
		{name: "a hyphen", database: "paint-shop", expected: "invalid database name"},
		{name: "a leading digit", database: "1paintshop", expected: "invalid database name"},
		{name: "too long", database: strings.Repeat("a", 33), expected: "invalid database name"},
		{name: "a quote", database: `paint"shop`, expected: "invalid database name"},
		{name: "one the datastore keeps", database: "template1", expected: "keeps for itself"},
		{name: "the service's own", database: "lollipop", expected: "already has a database lollipop"},
		{name: "one already made", database: "paintshop", expected: "already has a database paintshop"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateNewDatabase(postgres, "lollipop", test.database)
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %v", test.expected, err)
			}
		})
	}

	if err := ValidateNewDatabase(postgres, "lollipop", "atelier_2"); err != nil {
		t.Errorf("expected a new database to be accepted, got %s", err)
	}
}

func TestValidateNewUser(t *testing.T) {
	postgres := datastoreNamed(t, "postgres")
	withAccountService(t, postgres, nil, map[string]string{"painter": "brush"})

	tests := []struct {
		name     string
		user     string
		expected string
	}{
		{name: "a space", user: "the painter", expected: "invalid user name"},
		{name: "the group every user is in", user: "dokku_users", expected: "keeps for itself"},
		{name: "the service's own", user: "postgres", expected: "already has a user postgres"},
		{name: "one already made", user: "painter", expected: "already has a user painter"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateNewUser(postgres, "lollipop", test.user)
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %v", test.expected, err)
			}
		})
	}

	// a datastore with no users has none to make
	solr := datastoreNamed(t, "solr")
	if err := ValidateNewUser(solr, "lollipop", "painter"); err == nil || !strings.Contains(err.Error(), "does not support creating users") {
		t.Errorf("expected solr to refuse a user, got %v", err)
	}
}

// only what was made in a service can be destroyed: the service's own goes
// with the service
func TestValidateCreated(t *testing.T) {
	postgres := datastoreNamed(t, "postgres")
	withAccountService(t, postgres, []string{"paintshop"}, map[string]string{"painter": "brush"})

	if err := ValidateCreatedDatabase(postgres, "lollipop", "lollipop"); err == nil || !strings.Contains(err.Error(), "only removed with the service") {
		t.Errorf("expected the service's own database to be refused, got %v", err)
	}

	if err := ValidateCreatedDatabase(postgres, "lollipop", "atelier"); err == nil || !strings.Contains(err.Error(), "has no database atelier") {
		t.Errorf("expected a database never made to be refused, got %v", err)
	}

	if err := ValidateCreatedDatabase(postgres, "lollipop", "paintshop"); err != nil {
		t.Errorf("expected a made database to be accepted, got %s", err)
	}

	if err := ValidateCreatedUser(postgres, "lollipop", "postgres"); err == nil || !strings.Contains(err.Error(), "postgres:set lollipop password") {
		t.Errorf("expected the service's own user to be pointed at set, got %v", err)
	}

	if err := ValidateCreatedUser(postgres, "lollipop", "painter"); err != nil {
		t.Errorf("expected a made user to be accepted, got %s", err)
	}
}

func TestValidateAccount(t *testing.T) {
	postgres := datastoreNamed(t, "postgres")
	withAccountService(t, postgres, []string{"paintshop"}, map[string]string{"painter": "brush"})

	if err := ValidateAccount(postgres, "lollipop", Account{Database: "paintshop", User: "painter"}); err != nil {
		t.Errorf("expected a made database and user to be accepted, got %s", err)
	}

	if err := ValidateAccount(postgres, "lollipop", Account{Database: "lollipop", User: "postgres"}); err != nil {
		t.Errorf("expected the service's own to be accepted by name, got %s", err)
	}

	if err := ValidateAccount(postgres, "lollipop", Account{Database: "atelier"}); err == nil || !strings.Contains(err.Error(), "has no database atelier") {
		t.Errorf("expected an unknown database to be refused, got %v", err)
	}

	if err := ValidateAccount(postgres, "lollipop", Account{User: "sculptor"}); err == nil || !strings.Contains(err.Error(), "has no user sculptor") {
		t.Errorf("expected an unknown user to be refused, got %v", err)
	}

	memcached := datastoreNamed(t, "memcached")
	for _, account := range []Account{{Database: "paintshop"}, {User: "painter"}} {
		if err := ValidateAccount(memcached, "lollipop", account); err == nil || !strings.Contains(err.Error(), "does not support") {
			t.Errorf("expected memcached to refuse %+v, got %v", account, err)
		}
	}
}

// the url of the service's own database and user is the one a link made before
// a service could hold others was given, so it is still found by it
func TestAccountURLOfTheServicesOwnIsUnchanged(t *testing.T) {
	expected := map[string]string{
		"clickhouse": "clickhouse://lollipop:hunter2-password@dokku-clickhouse-lollipop:9000/lollipop",
		"couchdb":    "http://lollipop:hunter2-password@dokku-couchdb-lollipop:5984/lollipop",
		"mariadb":    "mysql://mariadb:hunter2-password@dokku-mariadb-lollipop:3306/lollipop",
		"mongo":      "mongodb://lollipop:hunter2-password@dokku-mongo-lollipop:27017/lollipop",
		"mysql":      "mysql://mysql:hunter2-password@dokku-mysql-lollipop:3306/lollipop",
		"omnisci":    "omnisci://omnisci:hunter2-password@dokku-omnisci-lollipop:6274/lollipop",
		"postgres":   "postgres://postgres:hunter2-password@dokku-postgres-lollipop:5432/lollipop",
		"rabbitmq":   "amqp://lollipop:hunter2-password@dokku-rabbitmq-lollipop:5672/lollipop",
		"redis":      "redis://:hunter2-password@dokku-redis-lollipop:6379",
		"rethinkdb":  "rethinkdb://dokku-rethinkdb-lollipop:28015/lollipop",
		"solr":       "http://dokku-solr-lollipop:8983/solr/lollipop",
	}

	for name, url := range expected {
		t.Run(name, func(t *testing.T) {
			s := datastoreNamed(t, name)
			withAccountService(t, s, nil, nil)

			if actual := s.AccountURL("lollipop", Account{}, ""); actual != url {
				t.Errorf("expected %q, got %q", url, actual)
			}

			if actual := s.URL("lollipop", ""); actual != url {
				t.Errorf("expected info's dsn %q, got %q", url, actual)
			}
		})
	}
}

func TestAccountURL(t *testing.T) {
	tests := []struct {
		name     string
		account  Account
		expected string
	}{
		{name: "postgres", account: Account{Database: "paintshop", User: "painter"}, expected: "postgres://painter:brush@dokku-postgres-lollipop:5432/paintshop"},
		{name: "mysql", account: Account{Database: "paintshop"}, expected: "mysql://mysql:hunter2-password@dokku-mysql-lollipop:3306/paintshop"},
		{name: "mariadb", account: Account{User: "painter"}, expected: "mysql://painter:brush@dokku-mariadb-lollipop:3306/lollipop"},
		// every account authenticates against the service's own database
		{name: "mongo", account: Account{Database: "paintshop", User: "painter"}, expected: "mongodb://painter:brush@dokku-mongo-lollipop:27017/paintshop?authSource=lollipop"},
		{name: "mongo", account: Account{User: "painter"}, expected: "mongodb://painter:brush@dokku-mongo-lollipop:27017/lollipop"},
		{name: "couchdb", account: Account{Database: "paintshop", User: "painter"}, expected: "http://painter:brush@dokku-couchdb-lollipop:5984/paintshop"},
		{name: "clickhouse", account: Account{Database: "paintshop", User: "painter"}, expected: "clickhouse://painter:brush@dokku-clickhouse-lollipop:9000/paintshop"},
		{name: "rabbitmq", account: Account{Database: "paintshop", User: "painter"}, expected: "amqp://painter:brush@dokku-rabbitmq-lollipop:5672/paintshop"},
		{name: "omnisci", account: Account{Database: "paintshop", User: "painter"}, expected: "omnisci://painter:brush@dokku-omnisci-lollipop:6274/paintshop"},
		{name: "solr", account: Account{Database: "paintshop"}, expected: "http://dokku-solr-lollipop:8983/solr/paintshop"},
		{name: "rethinkdb", account: Account{Database: "paintshop"}, expected: "rethinkdb://dokku-rethinkdb-lollipop:28015/paintshop"},
		{name: "redis", account: Account{Database: "3"}, expected: "redis://:hunter2-password@dokku-redis-lollipop:6379/3"},
		{name: "redis", account: Account{User: "painter"}, expected: "redis://painter:brush@dokku-redis-lollipop:6379"},
		{name: "redis", account: Account{Database: "0", User: "default"}, expected: "redis://:hunter2-password@dokku-redis-lollipop:6379"},
	}

	for _, test := range tests {
		t.Run(test.name+" "+test.account.Database+" "+test.account.User, func(t *testing.T) {
			s := datastoreNamed(t, test.name)
			withAccountService(t, s, []string{"paintshop"}, map[string]string{"painter": "brush"})

			if actual := s.AccountURL("lollipop", test.account, ""); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

func TestAccountJDBC(t *testing.T) {
	postgres := datastoreNamed(t, "postgres")
	withAccountService(t, postgres, []string{"paintshop"}, map[string]string{"painter": "brush"})

	values, ok := postgres.AccountJDBC("lollipop", Account{Database: "paintshop", User: "painter"})
	if !ok {
		t.Fatal("expected the jdbc values to render")
	}

	expected := JDBCValues{
		URL:      "jdbc:postgresql://dokku-postgres-lollipop:5432/paintshop?user=painter&password=brush",
		Username: "painter",
		Password: "brush",
	}
	if values != expected {
		t.Errorf("expected %+v, got %+v", expected, values)
	}

	if own, _ := postgres.AccountJDBC("lollipop", Account{}); own.Username != "postgres" {
		t.Errorf("expected the service's own account by default, got %+v", own)
	}
}

func TestLinkAccount(t *testing.T) {
	postgres := datastoreNamed(t, "postgres")
	withAccountService(t, postgres, []string{"paintshop"}, map[string]string{"painter": "brush"})

	// a link made before a service could hold others records nothing, and is
	// on the service's own
	if account := LinkAccount(postgres, "lollipop", "my-app"); !account.IsDefault() {
		t.Errorf("expected the service's own, got %+v", account)
	}

	if err := SetLinkDatabase(postgres, "lollipop", "my-app", "paintshop"); err != nil {
		t.Fatalf("failed to record the database: %s", err)
	}
	if err := SetLinkUser(postgres, "lollipop", "my-app", "painter"); err != nil {
		t.Fatalf("failed to record the user: %s", err)
	}
	if err := SetLinkUser(postgres, "lollipop", "other-app", "painter"); err != nil {
		t.Fatalf("failed to record the user: %s", err)
	}

	if account := LinkAccount(postgres, "lollipop", "my-app"); account != (Account{Database: "paintshop", User: "painter"}) {
		t.Errorf("expected the recorded account, got %+v", account)
	}

	if actual := postgres.LinkURL("lollipop", "my-app", ""); actual != "postgres://painter:brush@dokku-postgres-lollipop:5432/paintshop" {
		t.Errorf("expected the url of the recorded account, got %q", actual)
	}

	if apps := AppsLinkedOn(postgres, "lollipop", LinkUserProperty, "painter"); !slices.Equal(apps, []string{"my-app", "other-app"}) {
		t.Errorf("expected both apps linked as painter, got %v", apps)
	}

	if apps := AppsLinkedOn(postgres, "lollipop", LinkDatabaseProperty, "paintshop"); !slices.Equal(apps, []string{"my-app"}) {
		t.Errorf("expected one app linked to paintshop, got %v", apps)
	}

	// the service's own records nothing, which forgets what was recorded
	if err := SetLinkDatabase(postgres, "lollipop", "my-app", ""); err != nil {
		t.Fatalf("failed to forget the database: %s", err)
	}
	if account := LinkAccount(postgres, "lollipop", "my-app"); account != (Account{User: "painter"}) {
		t.Errorf("expected only the user to be recorded, got %+v", account)
	}
}

func TestSetLinkDatabaseReplacesACorruptRecord(t *testing.T) {
	postgres := datastoreNamed(t, "postgres")
	withAccountService(t, postgres, []string{"paintshop"}, nil)

	if err := os.MkdirAll(filepath.Join(DokkuLibRoot, "config", "postgres", "lollipop"), 0755); err != nil {
		t.Fatalf("failed to make the property directory: %s", err)
	}
	if err := os.WriteFile(filepath.Join(DokkuLibRoot, "config", "postgres", "lollipop", LinkDatabaseProperty), []byte("{not json"), 0644); err != nil {
		t.Fatalf("failed to write a corrupt record: %s", err)
	}

	if err := SetLinkDatabase(postgres, "lollipop", "my-app", "paintshop"); err != ErrCorruptLinkConfigKeys {
		t.Fatalf("expected the corrupt record to be reported once replaced, got %v", err)
	}

	if account := LinkAccount(postgres, "lollipop", "my-app"); account.Database != "paintshop" {
		t.Errorf("expected the database to be recorded over the corrupt record, got %+v", account)
	}
}

func TestCommandNames(t *testing.T) {
	postgres := datastoreNamed(t, "postgres")
	redis := datastoreNamed(t, "redis")

	connect, _ := postgres.Definition.CommandFor("connect")
	if !CommandNames(connect, ".Database") || !CommandNames(connect, ".Username") {
		t.Errorf("expected postgres's connect to name the database and the user: %v", connect.Exec)
	}

	// .DefaultDatabase is not .Database
	export, _ := redis.Definition.CommandFor("export")
	if CommandNames(export, ".Database") {
		t.Errorf("expected redis's export not to name a database: %v", export.Exec)
	}
}

// a command that would never be told which database it was asked for refuses
// it rather than acting on the service's own without a word
func TestCheckAccount(t *testing.T) {
	redis := datastoreNamed(t, "redis")
	withAccountService(t, redis, nil, nil)

	if err := redis.checkAccount("lollipop", "export", nil, Account{Database: "3"}); err == nil || !strings.Contains(err.Error(), "cannot be given --database") {
		t.Errorf("expected redis's export to refuse a database, got %v", err)
	}

	if err := redis.checkAccount("lollipop", "connect", nil, Account{Database: "3"}); err != nil {
		t.Errorf("expected redis's connect to take a database, got %s", err)
	}

	postgres := datastoreNamed(t, "postgres")
	withAccountService(t, postgres, []string{"paintshop"}, map[string]string{"painter": "brush"})

	// export dumps as the service's own account whatever database it is
	// asked for, so a user is for connect alone
	if err := postgres.checkAccount("lollipop", "export", nil, Account{User: "painter"}); err == nil || !strings.Contains(err.Error(), "cannot be given --user") {
		t.Errorf("expected postgres's export to refuse a user, got %v", err)
	}

	if err := postgres.checkAccount("lollipop", "connect", nil, Account{Database: "paintshop", User: "painter"}); err != nil {
		t.Errorf("expected postgres's connect to take a database and a user, got %s", err)
	}

	if err := postgres.checkAccount("lollipop", "reset", nil, Account{Database: "atelier"}); err == nil || !strings.Contains(err.Error(), "has no database atelier") {
		t.Errorf("expected an unknown database to be refused, got %v", err)
	}
}

func TestExportRefusesADatabaseWithEveryDatabase(t *testing.T) {
	postgres := datastoreNamed(t, "postgres")
	withAccountService(t, postgres, []string{"paintshop"}, nil)

	err := postgres.ExportService(context.Background(), ExportServiceInput{
		Datastore:    postgres,
		ServiceName:  "lollipop",
		AllDatabases: true,
		Database:     "paintshop",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be used with --all-databases") {
		t.Errorf("expected --database to be refused with --all-databases, got %v", err)
	}
}

// the databases and users commands are told to the running datastore, so a
// service that is not running records nothing
func TestCreateDatabaseNeedsARunningService(t *testing.T) {
	postgres := datastoreNamed(t, "postgres")
	withAccountService(t, postgres, nil, nil)

	err := postgres.CreateDatabase(context.Background(), "lollipop", "paintshop")
	if err == nil || !strings.Contains(err.Error(), "is not running") {
		t.Fatalf("expected the create to be refused, got %v", err)
	}

	if databases := CreatedDatabases(postgres, "lollipop"); len(databases) != 0 {
		t.Errorf("expected nothing to be recorded, got %v", databases)
	}
}

// every command is handed the databases and users the service holds, and a
// command's own arguments win over them
func TestAccountArguments(t *testing.T) {
	postgres := datastoreNamed(t, "postgres")
	withAccountService(t, postgres, []string{"paintshop"}, map[string]string{"painter": "brush"})

	arguments := postgres.accountArguments("lollipop", map[string]string{"database": "atelier"})
	expected := map[string]string{
		"database":  "atelier",
		"databases": "lollipop paintshop",
		"users":     "postgres painter",
	}
	for key, value := range expected {
		if arguments[key] != value {
			t.Errorf("expected %s to be %q, got %q", key, value, arguments[key])
		}
	}
}
