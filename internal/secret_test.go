package internal

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/mitchellh/cli"
)

// withSecretService creates a service holding a password, linked to the given
// apps
func withSecretService(t *testing.T, datastore *service.Datastore, serviceName string, password string, apps []string) {
	t.Helper()

	withInfoService(t, datastore, serviceName)
	writeInfoFile(t, datastore.SecretFile(serviceName, datastore.Definition.Dokku.Secrets["password"]), password)
	if len(apps) > 0 {
		writeInfoFile(t, service.Files(datastore, serviceName).Links, strings.Join(apps, "\n")+"\n")
	}
}

// secretProperty is the set property naming a secret
func secretProperty(t *testing.T, key string) SettableProperty {
	t.Helper()

	property, ok := LookupSettableProperty(key)
	if !ok || !property.IsSecret() {
		t.Fatalf("expected %s to be a password property", key)
	}

	return property
}

// A password is held to what can be written into a dsn, a config file and a
// statement as it is, and cannot be cleared.
func TestValidateSecretValue(t *testing.T) {
	tests := map[string]string{
		"":                        "cannot be cleared",
		"has space":               "may only contain",
		"has'quote":               "may only contain",
		"has@at":                  "may only contain",
		"has:colon":               "may only contain",
		"has/slash":               "may only contain",
		"two\nlines":              "may only contain",
		"a-perfectly_fine.pass~1": "",
	}

	for _, key := range []string{"password", "root-password"} {
		for value, expected := range tests {
			err := ValidatePropertyValue(key, value)
			if expected == "" {
				if err != nil {
					t.Errorf("expected %s %q to be accepted, got %q", key, value, err)
				}
				continue
			}

			if err == nil || !strings.Contains(err.Error(), expected) {
				t.Errorf("expected %s %q to be refused with %q, got %v", key, value, expected, err)
			}
		}
	}
}

// A password is only set on a datastore that has it and can change it.
func TestValidateSecretForTheService(t *testing.T) {
	memcached := service.Datastores["memcached"]
	if err := ValidateServicePropertyValue(memcached, "lollipop", "password", "value"); err == nil || !strings.Contains(err.Error(), "has no password") {
		t.Errorf("expected memcached to have no password to set, got %v", err)
	}

	redis := service.Datastores["redis"]
	if err := ValidateServicePropertyValue(redis, "lollipop", "root-password", "value"); err == nil || !strings.Contains(err.Error(), "has no root-password") {
		t.Errorf("expected redis to have no root password to set, got %v", err)
	}

	if err := ValidateServicePropertyValue(redis, "lollipop", "password", "value"); err != nil {
		t.Errorf("expected the redis password to be settable, got %q", err)
	}

	unchangeable := &service.Datastore{Definition: definition.Definition{Dokku: definition.Dokku{
		Plugin:  "thing",
		Secrets: map[string]definition.Secret{"password": {File: "PASSWORD", Length: 16}},
	}}}
	if err := ValidateServicePropertyValue(unchangeable, "lollipop", "password", "value"); err == nil || !strings.Contains(err.Error(), "no way to change") {
		t.Errorf("expected a password with no way to change it to be refused, got %v", err)
	}
}

// A password is changed by ChangeServiceSecret rather than written, since
// writing the file alone would leave the datastore holding the old one.
func TestSetPropertyRefusesToWriteAPassword(t *testing.T) {
	redis := service.Datastores["redis"]
	withSecretService(t, redis, "lollipop", "old", nil)

	if err := SetProperty(redis, "lollipop", "password", "new"); err == nil {
		t.Fatal("expected the password not to be written on its own")
	}

	if value := redis.SecretValue("lollipop", "password"); value != "old" {
		t.Errorf("expected the password to be left as it was, got %q", value)
	}
}

// A password set to what it already is touches nothing: not the datastore, not
// the file and not the linked apps.
func TestChangeServiceSecretLeavesTheSamePasswordAlone(t *testing.T) {
	redis := service.Datastores["redis"]
	withSecretService(t, redis, "lollipop", "unchanged", []string{"my-app"})
	calls := fakeDokku(t, map[string]string{"REDIS_URL": redis.URL("lollipop", "")})

	if err := ChangeServiceSecret(context.Background(), ChangeServiceSecretInput{
		Datastore:   redis,
		ServiceName: "lollipop",
		Property:    secretProperty(t, "password"),
		Value:       "unchanged",
		Logger:      Ui{Ui: cli.NewMockUi()},
	}); err != nil {
		t.Fatalf("expected an unchanged password to succeed, got %q", err)
	}

	if _, err := os.Stat(calls); err == nil {
		t.Errorf("expected dokku not to be asked anything, got %v", recordedCalls(t, calls))
	}
}

// A password read as the service starts is only changed with the operator's
// consent to restart it, and is left as it was without it.
func TestChangeServiceSecretNeedsConsentToRestart(t *testing.T) {
	for _, plugin := range []string{"nats", "meilisearch", "clickhouse", "graphite", "typesense"} {
		t.Run(plugin, func(t *testing.T) {
			datastore := service.Datastores[plugin]
			withSecretService(t, datastore, "lollipop", "old", nil)

			err := ChangeServiceSecret(context.Background(), ChangeServiceSecretInput{
				Datastore:   datastore,
				ServiceName: "lollipop",
				Property:    secretProperty(t, "password"),
				Value:       "new",
				Logger:      Ui{Ui: cli.NewMockUi()},
			})
			if err == nil || !strings.Contains(err.Error(), "--allow-restart") {
				t.Fatalf("expected the change to need --allow-restart, got %v", err)
			}

			if value := datastore.SecretValue("lollipop", "password"); value != "old" {
				t.Errorf("expected the password to be left as it was, got %q", value)
			}
		})
	}
}

// A password told to the datastore is told to it while it runs, and a service
// that is not running keeps the password it has.
func TestChangeServiceSecretNeedsARunningService(t *testing.T) {
	redis := service.Datastores["redis"]
	withSecretService(t, redis, "lollipop", "old", nil)

	err := ChangeServiceSecret(context.Background(), ChangeServiceSecretInput{
		Datastore:    redis,
		ServiceName:  "lollipop",
		Property:     secretProperty(t, "password"),
		Value:        "new",
		AllowRestart: true,
		Logger:       Ui{Ui: cli.NewMockUi()},
	})
	if err == nil || !strings.Contains(err.Error(), "is not running") {
		t.Fatalf("expected a stopped service to be refused, got %v", err)
	}

	if value := redis.SecretValue("lollipop", "password"); value != "old" {
		t.Errorf("expected the password to be left as it was, got %q", value)
	}
}

// A password read as the container is made is written for the container to be
// made on, readable by the dokku user alone.
func TestChangeSecretWritesAPasswordReadAtStart(t *testing.T) {
	nats := service.Datastores["nats"]
	withSecretService(t, nats, "lollipop", "old", nil)

	if err := nats.ChangeSecret(context.Background(), "lollipop", "password", "new"); err != nil {
		t.Fatalf("expected the password to be written, got %q", err)
	}

	filename := nats.SecretFile("lollipop", nats.Definition.Dokku.Secrets["password"])
	if contents := readSettingFile(t, filename); contents != "new" {
		t.Errorf("expected the password file to hold the new password, got %q", contents)
	}

	info, err := os.Stat(filename)
	if err != nil {
		t.Fatalf("failed to stat %s: %s", filename, err)
	}

	if info.Mode().Perm() != service.PrivateFileMode {
		t.Errorf("expected the password file to have mode %o, got %o", service.PrivateFileMode, info.Mode().Perm())
	}
}

// Every linked app is pointed at the new password under the keys that hold the
// url, keeping what the app added around it, and restarted unless asked not to.
func TestUpdateLinkedAppURLs(t *testing.T) {
	for _, noRestart := range []bool{false, true} {
		t.Run(map[bool]string{false: "restarted", true: "not restarted"}[noRestart], func(t *testing.T) {
			redis := service.Datastores["redis"]
			withSecretService(t, redis, "lollipop", "old", []string{"my-app"})

			before := redis.URL("lollipop", "")
			calls := fakeDokku(t, map[string]string{
				"REDIS_URL":   before,
				"WORKER_URL":  strings.Replace(before, "redis://", "rediss://", 1) + "?ssl_cert_reqs=none",
				"UNRELATED":   "redis://:other@elsewhere:6379",
				"ANOTHER_KEY": "value",
			})
			if err := service.SetLinkConfigKeys(redis, "lollipop", "my-app", []string{"REDIS_URL", "WORKER_URL"}); err != nil {
				t.Fatalf("failed to record the link keys: %s", err)
			}

			input := ChangeServiceSecretInput{
				Datastore:   redis,
				ServiceName: "lollipop",
				Property:    secretProperty(t, "password"),
				Value:       "new",
				NoRestart:   noRestart,
				Logger:      Ui{Ui: cli.NewMockUi()},
			}

			linked, err := linkedAppURLs(context.Background(), redis, "lollipop", input.Logger, linkedAsDefaultUser)
			if err != nil {
				t.Fatalf("expected the linked apps to be read, got %q", err)
			}

			writeInfoFile(t, redis.SecretFile("lollipop", redis.Definition.Dokku.Secrets["password"]), "new")
			if err := updateLinkedAppURLs(context.Background(), input.repoint(), linked); err != nil {
				t.Fatalf("expected the linked apps to be updated, got %q", err)
			}

			after := redis.URL("lollipop", "")
			if after == before {
				t.Fatalf("expected the url to change with the password, got %q", after)
			}

			option := ""
			if noRestart {
				option = "--no-restart "
			}
			expected := []string{
				"config:set " + option + "my-app REDIS_URL=" + after + " WORKER_URL=" + strings.Replace(after, "redis://", "rediss://", 1) + "?ssl_cert_reqs=none",
			}
			if actual := recordedCalls(t, calls); !slices.Equal(actual, expected) {
				t.Errorf("expected %q, got %q", expected, actual)
			}
		})
	}
}

// The jdbc values a link made with --jdbc set are pointed at the new password
// along with the url, keeping a querystring the app added to the jdbc url, in
// the same config:set
func TestUpdateLinkedAppURLsWithJDBC(t *testing.T) {
	postgres := service.Datastores["postgres"]
	withSecretService(t, postgres, "lollipop", "old", []string{"my-app"})

	before := postgres.URL("lollipop", "")
	beforeJDBC, ok := postgres.JDBC("lollipop")
	if !ok {
		t.Fatal("expected the jdbc values to render")
	}

	calls := fakeDokku(t, map[string]string{
		"DATABASE_URL":           before,
		"JDBC_DATABASE_URL":      beforeJDBC.URL + "&sslmode=require",
		"JDBC_DATABASE_USERNAME": beforeJDBC.Username,
		"JDBC_DATABASE_PASSWORD": beforeJDBC.Password,
	})
	if err := service.SetLinkConfigKeys(postgres, "lollipop", "my-app", []string{"DATABASE_URL"}); err != nil {
		t.Fatalf("failed to record the link keys: %s", err)
	}
	if err := service.SetLinkJDBCConfigKeys(postgres, "lollipop", "my-app", []string{"JDBC_DATABASE_PASSWORD", "JDBC_DATABASE_URL", "JDBC_DATABASE_USERNAME"}); err != nil {
		t.Fatalf("failed to record the jdbc keys: %s", err)
	}

	input := ChangeServiceSecretInput{
		Datastore:   postgres,
		ServiceName: "lollipop",
		Property:    secretProperty(t, "password"),
		Value:       "new",
		NoRestart:   true,
		Logger:      Ui{Ui: cli.NewMockUi()},
	}

	linked, err := linkedAppURLs(context.Background(), postgres, "lollipop", input.Logger, linkedAsDefaultUser)
	if err != nil {
		t.Fatalf("expected the linked apps to be read, got %q", err)
	}

	writeInfoFile(t, postgres.SecretFile("lollipop", postgres.Definition.Dokku.Secrets["password"]), "new")
	if err := updateLinkedAppURLs(context.Background(), input.repoint(), linked); err != nil {
		t.Fatalf("expected the linked apps to be updated, got %q", err)
	}

	after := postgres.URL("lollipop", "")
	afterJDBC, _ := postgres.JDBC("lollipop")
	if afterJDBC.URL == beforeJDBC.URL {
		t.Fatalf("expected the jdbc url to change with the password, got %q", afterJDBC.URL)
	}

	// the username is left out, since the password does not change it
	expected := []string{
		"config:set --no-restart my-app DATABASE_URL=" + after + " JDBC_DATABASE_PASSWORD=new JDBC_DATABASE_URL=" + afterJDBC.URL + "&sslmode=require",
	}
	if actual := recordedCalls(t, calls); !slices.Equal(actual, expected) {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}
