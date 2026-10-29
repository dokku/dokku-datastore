package definition

import (
	"strings"
	"testing"
)

// accountsCompose is validCompose with a password, a dsn naming the account
// and the database, and the databases and users blocks a datastore that makes
// both declares, which each case then breaks in exactly one way
var accountsCompose = strings.Replace(validCompose,
	`  dsn: "{{ .Scheme }}://{{ .Host }}:{{ .Port.native }}"`,
	`  dsn: "{{ .Scheme }}://{{ .Username }}:{{ .Password }}@{{ .Host }}:{{ .Port.native }}/{{ .Database }}"`, 1) + `  secrets:
    password:
      file: PASSWORD
      length: 16
  databases:
    create:
      exec: ["thing-create-database", "{{ .Args.database }}", "{{ .Args.users }}"]
    destroy:
      exec: ["thing-destroy-database", "{{ .Args.database }}"]
  users:
    default: "{{ .ServiceName }}"
    secret: password
    reserved: [root]
    create:
      exec: ["thing-create-user", "{{ .Args.user }}", "{{ .Args.databases }}"]
      env:
        NEW_PASSWORD: "{{ .NewSecret }}"
    destroy:
      exec: ["thing-destroy-user", "{{ .Args.user }}"]
    change:
      exec: ["thing-change-user-password", "{{ .Args.user }}"]
      env:
        NEW_PASSWORD: "{{ .NewSecret }}"
`

// fixedCompose is validCompose with sixteen databases addressed by index, as
// redis has
var fixedCompose = strings.Replace(validCompose,
	`  dsn: "{{ .Scheme }}://{{ .Host }}:{{ .Port.native }}"`,
	`  dsn: "{{ .Scheme }}://{{ .Host }}:{{ .Port.native }}/{{ .Database }}"`, 1) + `  databases:
    fixed: 16
    default: "0"
`

func TestParseReadsDatabasesAndUsers(t *testing.T) {
	parsed, err := parseCompose(t, accountsCompose)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if !parsed.Dokku.Databases.Makes() {
		t.Error("expected a datastore that makes its databases")
	}

	if parsed.Dokku.Users == nil || parsed.Dokku.Users.Default != "{{ .ServiceName }}" {
		t.Errorf("expected the default user to be read, got %+v", parsed.Dokku.Users)
	}

	for _, subcommand := range []string{"create-database", "destroy-database", "create-user", "destroy-user", "set-user-password"} {
		if !parsed.Implements(subcommand) {
			t.Errorf("expected %s to be implemented", subcommand)
		}
	}
}

// a datastore with a fixed set of databases has a link name one, but none to
// make or destroy
func TestParseReadsFixedDatabases(t *testing.T) {
	parsed, err := parseCompose(t, fixedCompose)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if parsed.Dokku.Databases == nil || parsed.Dokku.Databases.Fixed != 16 {
		t.Fatalf("expected sixteen fixed databases, got %+v", parsed.Dokku.Databases)
	}

	if parsed.Dokku.Databases.Makes() {
		t.Error("expected a datastore with fixed databases not to make them")
	}

	for _, subcommand := range []string{"create-database", "destroy-database", "create-user"} {
		if parsed.Implements(subcommand) {
			t.Errorf("expected %s not to be implemented", subcommand)
		}
	}
}

// a datastore declaring neither has none of the commands
func TestImplementsNoAccounts(t *testing.T) {
	parsed, err := parseCompose(t, validCompose)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	for _, subcommand := range []string{"create-database", "destroy-database", "create-user", "destroy-user", "set-user-password"} {
		if parsed.Implements(subcommand) {
			t.Errorf("expected %s not to be implemented", subcommand)
		}
	}
}

func TestParseRejectsBrokenAccounts(t *testing.T) {
	tests := []struct {
		name     string
		compose  string
		expected string
	}{
		{
			name:     "databases with no destroy",
			compose:  strings.Replace(accountsCompose, "    destroy:\n      exec: [\"thing-destroy-database\", \"{{ .Args.database }}\"]\n", "", 1),
			expected: "x-dokku.databases needs both create and destroy, or fixed",
		},
		{
			name:     "fixed databases that are also made",
			compose:  strings.Replace(fixedCompose, "    fixed: 16\n", "    fixed: 16\n    create:\n      exec: [\"thing\"]\n", 1),
			expected: "fixed databases alongside create or destroy",
		},
		{
			name:     "a default past the fixed databases",
			compose:  strings.Replace(fixedCompose, `default: "0"`, `default: "16"`, 1),
			expected: `x-dokku.databases.default is "16", which is not an index below 16`,
		},
		{
			name:     "a default that is not an index",
			compose:  strings.Replace(fixedCompose, `default: "0"`, `default: "first"`, 1),
			expected: "which is not an index below 16",
		},
		{
			name:     "a negative number of fixed databases",
			compose:  strings.Replace(fixedCompose, "fixed: 16", "fixed: -1", 1),
			expected: "which is not a number of databases",
		},
		{
			name:     "a default for databases that are made",
			compose:  strings.Replace(accountsCompose, "  databases:\n", "  databases:\n    default: \"0\"\n", 1),
			expected: "only names an index of fixed databases",
		},
		{
			name:     "databases a dsn never names",
			compose:  strings.Replace(fixedCompose, "{{ .Port.native }}/{{ .Database }}", "{{ .Port.native }}", 1),
			expected: "the dsn never names .Database",
		},
		{
			name:     "users with no default",
			compose:  strings.Replace(accountsCompose, "    default: \"{{ .ServiceName }}\"\n", "", 1),
			expected: "x-dokku.users.default is required",
		},
		{
			name:     "users with no secret",
			compose:  strings.Replace(accountsCompose, "    secret: password\n", "", 1),
			expected: "x-dokku.users.secret is required",
		},
		{
			name:     "users naming a secret that is not declared",
			compose:  strings.Replace(accountsCompose, "    secret: password\n", "    secret: root_password\n", 1),
			expected: `x-dokku.users.secret names "root_password", which is not a declared secret`,
		},
		{
			name:     "users with no change",
			compose:  accountsCompose[:strings.Index(accountsCompose, "    change:")],
			expected: "x-dokku.users needs create, destroy and change",
		},
		{
			name:     "users a dsn never names",
			compose:  strings.Replace(accountsCompose, "{{ .Username }}:{{ .Password }}@", "", 1),
			expected: "the dsn never names .Username",
		},
		{
			name:     "an empty reserved user",
			compose:  strings.Replace(accountsCompose, "reserved: [root]", `reserved: [""]`, 1),
			expected: "x-dokku.users.reserved has an empty entry",
		},
		{
			name:     "an account command run on the host",
			compose:  strings.Replace(accountsCompose, "    destroy:\n      exec: [\"thing-destroy-user\"", "    destroy:\n      mode: host\n      exec: [\"thing-destroy-user\"", 1),
			expected: `command "users.destroy" runs host`,
		},
		{
			name:     "an account command run offline",
			compose:  strings.Replace(accountsCompose, "    create:\n      exec: [\"thing-create-database\"", "    create:\n      mode: offline\n      exec: [\"thing-create-database\"", 1),
			expected: `command "databases.create" runs offline`,
		},
		{
			name:     "an account command reading stdin",
			compose:  strings.Replace(accountsCompose, "    destroy:\n      exec: [\"thing-destroy-user\"", "    destroy:\n      stdin: true\n      exec: [\"thing-destroy-user\"", 1),
			expected: `command "users.destroy" reads stdin`,
		},
		{
			// the password is only handed over to make an account or change it
			name:     "a new password where none is handed over",
			compose:  strings.Replace(accountsCompose, `exec: ["thing-destroy-user", "{{ .Args.user }}"]`, `exec: ["thing-destroy-user", "{{ .NewSecret }}"]`, 1),
			expected: "which only a secret's change is given",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseCompose(t, test.compose)
			if err == nil {
				t.Fatal("expected an error")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %q", test.expected, err)
			}
		})
	}
}

// the default account is rendered against the rest of the scope, and its
// password read from the secret the users block names
func TestWithDefaultAccount(t *testing.T) {
	parsed, err := parseCompose(t, accountsCompose)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	scope := parsed.WithDefaultAccount(Scope{
		ServiceName: "lollipop",
		Database:    "lollipop_db",
		Secret:      map[string]string{"password": "hunter2"},
	})

	for _, check := range []struct {
		name     string
		actual   string
		expected string
	}{
		{name: "username", actual: scope.Username, expected: "lollipop"},
		{name: "default username", actual: scope.DefaultUsername, expected: "lollipop"},
		{name: "password", actual: scope.Password, expected: "hunter2"},
		{name: "default database", actual: scope.DefaultDatabase, expected: "lollipop_db"},
	} {
		if check.actual != check.expected {
			t.Errorf("expected the %s %q, got %q", check.name, check.expected, check.actual)
		}
	}
}

// a datastore with no users has no account to render as, and a template naming
// one renders empty rather than failing
func TestWithDefaultAccountWithoutUsers(t *testing.T) {
	parsed, err := parseCompose(t, fixedCompose)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	scope := parsed.WithDefaultAccount(Scope{Database: "0"})
	if scope.Username != "" || scope.Password != "" {
		t.Errorf("expected no account, got %q and %q", scope.Username, scope.Password)
	}

	if scope.DefaultDatabase != "0" {
		t.Errorf("expected the default database %q, got %q", "0", scope.DefaultDatabase)
	}
}
