package internal

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/mitchellh/cli"
)

// accountServices is jdbcServices with a database named paintshop and a user
// named painter made in every service, recorded the way create-database and
// create-user record them
func accountServices(t *testing.T, links map[string][]string) *service.Datastore {
	t.Helper()

	datastore := jdbcServices(t, links)
	for serviceName := range links {
		folders := service.Folders(datastore, serviceName)
		for directory, contents := range map[string]map[string]string{
			folders.Databases: {"paintshop": "paintshop"},
			folders.Auth:      {"painter": "brush"},
		} {
			if err := os.MkdirAll(directory, 0750); err != nil {
				t.Fatalf("failed to make %s: %s", directory, err)
			}

			for name, content := range contents {
				if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0600); err != nil {
					t.Fatalf("failed to record %s: %s", name, err)
				}
			}
		}
	}

	return datastore
}

// linkAs records the database and user my-app was linked to lollipop on
func linkAs(t *testing.T, datastore *service.Datastore, appName string, account service.Account) {
	t.Helper()

	if err := service.SetLinkDatabase(datastore, "lollipop", appName, account.Database); err != nil {
		t.Fatalf("failed to record the database: %s", err)
	}

	if err := service.SetLinkUser(datastore, "lollipop", appName, account.User); err != nil {
		t.Fatalf("failed to record the user: %s", err)
	}
}

func TestLinkServiceOnADatabaseAndUser(t *testing.T) {
	datastore := accountServices(t, map[string][]string{"lollipop": {}})
	calls := fakeDokku(t, map[string]string{})

	if err := LinkService(t.Context(), LinkServiceInput{
		AppName:     "my-app",
		Database:    "paintshop",
		Datastore:   datastore,
		JDBC:        true,
		Logger:      Ui{Ui: cli.NewMockUi()},
		NoRestart:   true,
		ServiceName: "lollipop",
		User:        "painter",
	}); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	url := "postgres://painter:brush@dokku-postgres-lollipop:5432/paintshop"
	jdbc := "jdbc:postgresql://dokku-postgres-lollipop:5432/paintshop?user=painter&password=brush"
	expected := "config:set --no-restart my-app DATABASE_URL=" + url + " JDBC_DATABASE_PASSWORD=brush JDBC_DATABASE_URL=" + jdbc + " JDBC_DATABASE_USERNAME=painter"
	if actual := recordedCalls(t, calls); !slices.Contains(actual, expected) {
		t.Errorf("expected %q among the calls, got %q", expected, actual)
	}

	if account := service.LinkAccount(datastore, "lollipop", "my-app"); account != (service.Account{Database: "paintshop", User: "painter"}) {
		t.Errorf("expected the account to be recorded, got %+v", account)
	}
}

// a link on the service's own database and user records nothing, so that it
// looks the way every link made before a service could hold more does
func TestLinkServiceOnTheServicesOwnRecordsNothing(t *testing.T) {
	datastore := accountServices(t, map[string][]string{"lollipop": {}})
	fakeDokku(t, map[string]string{})

	// a record a lost link left is forgotten
	linkAs(t, datastore, "my-app", service.Account{Database: "paintshop"})

	if err := LinkService(t.Context(), LinkServiceInput{
		AppName:     "my-app",
		Datastore:   datastore,
		Logger:      Ui{Ui: cli.NewMockUi()},
		NoRestart:   true,
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	if account := service.LinkAccount(datastore, "lollipop", "my-app"); !account.IsDefault() {
		t.Errorf("expected nothing to be recorded, got %+v", account)
	}
}

func TestLinkServiceRefusesAnAccountTheServiceDoesNotHold(t *testing.T) {
	tests := []struct {
		name      string
		datastore func(t *testing.T) *service.Datastore
		input     LinkServiceInput
		expected  string
	}{
		{
			name:      "an unknown database",
			datastore: func(t *testing.T) *service.Datastore { return accountServices(t, map[string][]string{"lollipop": {}}) },
			input:     LinkServiceInput{Database: "atelier"},
			expected:  "service lollipop has no database atelier",
		},
		{
			name:      "an unknown user",
			datastore: func(t *testing.T) *service.Datastore { return accountServices(t, map[string][]string{"lollipop": {}}) },
			input:     LinkServiceInput{User: "sculptor"},
			expected:  "service lollipop has no user sculptor",
		},
		{
			name: "a datastore with no users",
			datastore: func(t *testing.T) *service.Datastore {
				return linkedServicesOf(t, service.Datastores["solr"], map[string][]string{"lollipop": {}})
			},
			input:    LinkServiceInput{User: "painter"},
			expected: "Solr does not support linking or addressing a user",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			datastore := test.datastore(t)
			calls := fakeDokku(t, map[string]string{})

			input := test.input
			input.AppName = "my-app"
			input.Datastore = datastore
			input.Logger = Ui{Ui: cli.NewMockUi()}
			input.NoRestart = true
			input.ServiceName = "lollipop"

			err := LinkService(t.Context(), input)
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("expected an error containing %q, got %v", test.expected, err)
			}

			if actual := recordedCalls(t, calls); len(actual) != 0 {
				t.Errorf("expected dokku not to be asked anything, got %q", actual)
			}
		})
	}
}

// an app linked on another database is found by that database's url, so
// linking it again is refused rather than adding a second alias
func TestLinkServiceAlreadyLinkedOnADatabase(t *testing.T) {
	datastore := accountServices(t, map[string][]string{"lollipop": {"my-app"}})
	linkAs(t, datastore, "my-app", service.Account{Database: "paintshop"})
	fakeDokku(t, map[string]string{"DATABASE_URL": "postgres://postgres:hunter2@dokku-postgres-lollipop:5432/paintshop"})

	err := LinkService(t.Context(), LinkServiceInput{
		AppName:     "my-app",
		Datastore:   datastore,
		Logger:      Ui{Ui: cli.NewMockUi()},
		NoRestart:   true,
		ServiceName: "lollipop",
	})
	if err == nil || err.Error() != "Already linked as DATABASE_URL" {
		t.Fatalf("expected the link to be refused, got %v", err)
	}
}

func TestUnlinkServiceOnADatabaseAndUser(t *testing.T) {
	datastore := accountServices(t, map[string][]string{"lollipop": {"my-app"}})
	linkAs(t, datastore, "my-app", service.Account{Database: "paintshop", User: "painter"})

	dokkuRoot := t.TempDir()
	t.Setenv("DOKKU_ROOT", dokkuRoot)
	if err := os.MkdirAll(filepath.Join(dokkuRoot, "my-app"), 0755); err != nil {
		t.Fatalf("failed to create the app root: %s", err)
	}

	calls := fakeDokku(t, map[string]string{
		"DATABASE_URL": "postgres://painter:brush@dokku-postgres-lollipop:5432/paintshop",
		"OTHER_URL":    "postgres://postgres:hunter2@dokku-postgres-lollipop:5432/lollipop",
	})

	if err := UnlinkService(t.Context(), UnlinkServiceInput{
		AppName:     "my-app",
		Datastore:   datastore,
		Logger:      Ui{Ui: cli.NewMockUi()},
		NoRestart:   true,
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	// the url of the account the app was linked on, and nothing else
	if actual := recordedCalls(t, calls); !slices.Contains(actual, "config:unset --no-restart my-app DATABASE_URL") {
		t.Errorf("expected DATABASE_URL alone to be unset, got %q", actual)
	}

	if account := service.LinkAccount(datastore, "lollipop", "my-app"); !account.IsDefault() {
		t.Errorf("expected the account to be forgotten, got %+v", account)
	}
}

// promote finds the url an app holds by the account it was linked on, both for
// the service promoted and for the one it displaces
func TestPromoteServiceOnADatabase(t *testing.T) {
	datastore := accountServices(t, map[string][]string{
		"lollipop":   {"my-app"},
		"gobstopper": {"my-app"},
	})
	linkAs(t, datastore, "my-app", service.Account{Database: "paintshop", User: "painter"})

	promotedURL := datastore.LinkURL("lollipop", "my-app", "")
	displacedURL := datastore.LinkURL("gobstopper", "my-app", "")
	if !strings.HasSuffix(promotedURL, "/paintshop") {
		t.Fatalf("expected the promoted url to name paintshop, got %q", promotedURL)
	}

	if err := service.SetLinkConfigKeys(datastore, "lollipop", "my-app", []string{"DOKKU_POSTGRES_BLUE_URL"}); err != nil {
		t.Fatalf("failed to record the keys: %s", err)
	}
	if err := service.SetLinkConfigKeys(datastore, "gobstopper", "my-app", []string{"DATABASE_URL"}); err != nil {
		t.Fatalf("failed to record the keys: %s", err)
	}

	calls := fakeDokku(t, map[string]string{
		"DATABASE_URL":            displacedURL,
		"DOKKU_POSTGRES_BLUE_URL": promotedURL,
	})

	if err := PromoteService(t.Context(), PromoteServiceInput{
		AppName:     "my-app",
		Datastore:   datastore,
		Logger:      Ui{Ui: cli.NewMockUi()},
		ServiceName: "lollipop",
	}); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	expected := "config:set my-app DATABASE_URL=" + promotedURL + " DOKKU_POSTGRES_AQUA_URL=" + displacedURL
	if actual := recordedCalls(t, calls); !slices.Contains(actual, expected) {
		t.Errorf("expected %q among the calls, got %q", expected, actual)
	}

	if actual := service.LinkConfigKeys(datastore, "gobstopper", "my-app"); !slices.Equal(actual, []string{"DOKKU_POSTGRES_AQUA_URL"}) {
		t.Errorf("expected the displaced service to record the alias its url moved to, got %v", actual)
	}
}

// a password belongs to one account, so changing one repoints the apps linked
// as that account and leaves the rest alone
func TestPasswordChangesRepointOnlyTheirApps(t *testing.T) {
	datastore := accountServices(t, map[string][]string{"lollipop": {"own-app", "painter-app"}})
	linkAs(t, datastore, "painter-app", service.Account{User: "painter"})

	// the fake hands every app the same config, which holds a url of each
	fakeDokku(t, map[string]string{
		"DATABASE_URL": "postgres://painter:brush@dokku-postgres-lollipop:5432/lollipop",
		"OWN_URL":      "postgres://postgres:hunter2@dokku-postgres-lollipop:5432/lollipop",
	})
	logger := Ui{Ui: cli.NewMockUi()}

	own, err := linkedAppURLs(t.Context(), datastore, "lollipop", logger, linkedAsDefaultUser)
	if err != nil {
		t.Fatalf("expected the linked apps to be read, got %s", err)
	}
	if len(own) != 1 || own[0].appName != "own-app" {
		t.Errorf("expected a change of the service's own password to read own-app alone, got %+v", own)
	}

	painters, err := linkedAppURLs(t.Context(), datastore, "lollipop", logger, func(account service.Account) bool {
		return account.User == "painter"
	})
	if err != nil {
		t.Fatalf("expected the linked apps to be read, got %s", err)
	}
	if len(painters) != 1 || painters[0].appName != "painter-app" {
		t.Fatalf("expected a change of painter's password to read painter-app alone, got %+v", painters)
	}

	// the datastore has taken the new password, which is then recorded
	if err := os.WriteFile(filepath.Join(service.Folders(datastore, "lollipop").Auth, "painter"), []byte("easel"), 0600); err != nil {
		t.Fatalf("failed to record the new password: %s", err)
	}

	calls := fakeDokku(t, map[string]string{})
	if err := updateLinkedAppURLs(t.Context(), repointInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Changed:     "the password of user painter",
		NoRestart:   true,
		Logger:      logger,
	}, painters); err != nil {
		t.Fatalf("expected the apps to be repointed, got %s", err)
	}

	expected := "config:set --no-restart painter-app DATABASE_URL=postgres://painter:easel@dokku-postgres-lollipop:5432/lollipop"
	if actual := recordedCalls(t, calls); !slices.Equal(actual, []string{expected}) {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}

func TestChangeUserPasswordRefusesTheServicesOwn(t *testing.T) {
	datastore := accountServices(t, map[string][]string{"lollipop": {}})

	err := ChangeUserPassword(t.Context(), ChangeUserPasswordInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		User:        "postgres",
		Logger:      Ui{Ui: cli.NewMockUi()},
	})
	if err == nil || !strings.Contains(err.Error(), "postgres:set lollipop password") {
		t.Errorf("expected to be pointed at set, got %v", err)
	}
}

// a database or user an app is linked to is refused before anything is asked
// of the datastore, which needs no container to say so
func TestDestroyRefusesWhatIsLinked(t *testing.T) {
	datastore := accountServices(t, map[string][]string{"lollipop": {"my-app"}})
	linkAs(t, datastore, "my-app", service.Account{Database: "paintshop", User: "painter"})

	input := AccountInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Logger:      Ui{Ui: cli.NewMockUi()},
	}

	input.Name = "paintshop"
	if err := DestroyDatabase(t.Context(), input); err == nil || !strings.Contains(err.Error(), "linked to app(s): my-app") {
		t.Errorf("expected the database to be refused, got %v", err)
	}

	input.Name = "painter"
	if err := DestroyUser(t.Context(), input); err == nil || !strings.Contains(err.Error(), "linked to app(s): my-app") {
		t.Errorf("expected the user to be refused, got %v", err)
	}

	// both are still recorded
	if databases := service.CreatedDatabases(datastore, "lollipop"); !slices.Equal(databases, []string{"paintshop"}) {
		t.Errorf("expected paintshop to be kept, got %v", databases)
	}
	if users := service.CreatedUsers(datastore, "lollipop"); !slices.Equal(users, []string{"painter"}) {
		t.Errorf("expected painter to be kept, got %v", users)
	}
}

func TestCreateUserChecksThePassword(t *testing.T) {
	datastore := accountServices(t, map[string][]string{"lollipop": {}})

	err := CreateUser(t.Context(), AccountInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Name:        "sculptor",
		Password:    "it's",
		Logger:      Ui{Ui: cli.NewMockUi()},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid password value") {
		t.Errorf("expected the password to be refused, got %v", err)
	}
}

func TestAccountPassword(t *testing.T) {
	postgres := service.Datastores["postgres"]

	generated, err := accountPassword(postgres, "")
	if err != nil {
		t.Fatalf("expected a password to be generated, got %s", err)
	}

	// the length the service's own is generated at
	if length := postgres.Definition.Dokku.Secrets["password"].Length; len(generated) != length {
		t.Errorf("expected %d characters, got %q", length, generated)
	}

	if given, err := accountPassword(postgres, "brush"); err != nil || given != "brush" {
		t.Errorf("expected the given password, got %q and %v", given, err)
	}
}

// a rename carries the account the app was linked on, so the renamed app is
// found by the url it was copied with
func TestARenameCarriesTheAccount(t *testing.T) {
	datastore := accountServices(t, map[string][]string{"lollipop": {"my-app"}})
	linkAs(t, datastore, "my-app", service.Account{Database: "paintshop", User: "painter"})
	input := triggerInput(datastore)

	if err := CopyAppLinks(t.Context(), input, "my-app", "renamed-app"); err != nil {
		t.Fatalf("failed to copy the app links: %s", err)
	}

	if err := RemoveAppLinks(t.Context(), input, "my-app"); err != nil {
		t.Fatalf("failed to remove the app links: %s", err)
	}

	if account := service.LinkAccount(datastore, "lollipop", "renamed-app"); account != (service.Account{Database: "paintshop", User: "painter"}) {
		t.Errorf("expected the renamed app to be recorded on the account, got %+v", account)
	}

	if account := service.LinkAccount(datastore, "lollipop", "my-app"); !account.IsDefault() {
		t.Errorf("expected the old name to record nothing, got %+v", account)
	}
}

// the users are made in a clone before its databases, so that each database
// is made with every user given access to it. Nothing runs here, so the first
// step is the one that fails
func TestCloneAccountsMakesTheUsersFirst(t *testing.T) {
	datastore := accountServices(t, map[string][]string{"lollipop": {}, "lollipop_copy": {}})
	if err := os.WriteFile(filepath.Join(service.Folders(datastore, "lollipop_copy").Root, "DATABASE_NAME"), []byte("lollipop_copy"), 0644); err != nil {
		t.Fatalf("failed to write the database name: %s", err)
	}
	for _, directory := range []string{service.Folders(datastore, "lollipop_copy").Databases, service.Folders(datastore, "lollipop_copy").Auth} {
		if err := os.RemoveAll(directory); err != nil {
			t.Fatalf("failed to empty the clone: %s", err)
		}
	}

	err := cloneAccounts(context.Background(), CloneServiceInput{
		Datastore:      datastore,
		ServiceName:    "lollipop",
		NewServiceName: "lollipop_copy",
		Logger:         Ui{Ui: cli.NewMockUi()},
	})
	if err == nil || !strings.Contains(err.Error(), "unable to clone user painter") {
		t.Errorf("expected the user to be made first, got %v", err)
	}
}
