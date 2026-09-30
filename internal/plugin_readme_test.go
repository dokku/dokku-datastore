package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/service"
)

func TestProcessSentence(t *testing.T) {
	tests := []struct {
		name     string
		lines    []string
		expected string
	}{
		{
			name:     "lines are joined and the sentence is capitalized",
			lines:    []string{"you can tail logs for", "a particular service"},
			expected: "You can tail logs for a particular service:",
		},
		{
			name:     "single quotes become backticks",
			lines:    []string{"use the 'expose' subcommand."},
			expected: "Use the `expose` subcommand.",
		},
		{
			name:     "environment variables are quoted",
			lines:    []string{"it is possible to change the protocol for REDIS_URL on the app."},
			expected: "It is possible to change the protocol for `REDIS_URL` on the app.",
		},
		{
			name:     "an acronym at the end of a sentence keeps its punctuation outside the quotes",
			lines:    []string{"backup the service to the bucket on AWS"},
			expected: "Backup the service to the bucket on `AWS`:",
		},
		{
			name:     "the wildcard address is quoted",
			lines:    []string{"removing access to it from the public interface (0.0.0.0)"},
			expected: "Removing access to it from the public interface (`0.0.0.0`):",
		},
		{
			name:     "possessives survive the quote conversion",
			lines:    []string{"the plugin's own documentation"},
			expected: "The plugin's own documentation:",
		},
		{
			name:     "inline code starting with an s keeps its opening backtick",
			lines:    []string{"add 'sslmode=require' to the service's dsn."},
			expected: "Add `sslmode=require` to the service's dsn.",
		},
		{
			name:     "every sentence in a paragraph is capitalized",
			lines:    []string{"a redis service can be linked. here we link it to our app."},
			expected: "A redis service can be linked. Here we link it to our app.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := processSentence(test.lines); actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

func TestReadme(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")

	data := redisDocumentationData(t)
	readme, err := Readme(ReadmeInput{
		Commands: helpTestCommands(),
		Data:     data,
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	tests := []struct {
		name     string
		expected string
	}{
		{name: "the title", expected: "# dokku redis "},
		// the version is taken from the definition rather than written out here:
		// it is the definition's to change, and dependabot changes it, so a copy
		// of it in this file is a test that fails on every image bump
		{
			name: "the image it installs",
			expected: fmt.Sprintf("Currently defaults to installing [%s %s](https://hub.docker.com/_/%s/).",
				data.Image, data.ImageVersion, data.Image),
		},
		{name: "the install command", expected: "sudo dokku plugin:install https://github.com/dokku/dokku-redis.git --name redis"},
		{name: "the command list", expected: "redis:list                                         # list all Redis services"},
		{name: "a usage section", expected: "### Basic Usage"},
		{name: "a command heading", expected: "### create a backup of the Redis service to an existing s3 bucket"},
		{name: "a usage block", expected: "```shell\n# usage\ndokku redis:backup <service> <bucket-name> [-u|--use-iam]\n```"},
		{name: "a flag", expected: "- `-u|--use-iam`: use the IAM profile associated with the current server"},
		{name: "prose from the documentation", expected: "Backup the `lollipop` service to the `my-s3-bucket` bucket on `AWS`:"},
		{name: "a command from the documentation", expected: "```shell\ndokku redis:backup lollipop my-s3-bucket --use-iam\n```"},
		{name: "the docker pull section", expected: "`REDIS_DISABLE_PULL` environment variable"},
		{name: "the wait timeout section", expected: "`REDIS_WAIT_TIMEOUT` environment variable"},
		{name: "the wait timeout property", expected: "`wait-timeout` property with `dokku redis:set`"},
		{name: "the expose limits section", expected: "### Limiting where and to whom a service is exposed"},
		{name: "the expose limits apply without a restart", expected: "Either reaches a running service with `dokku redis:reexpose`"},
		{name: "the expose mode section", expected: "### Exposing a service without an ambassador"},
		{name: "the expose mode property", expected: "`expose-mode` property to `direct` with `dokku redis:set`"},
		{name: "the expose mode confirmation", expected: "Pass `--force` to stop and start it without being asked"},
		{name: "the exposed dsn section", expected: "### Connecting to an exposed service from outside the host"},
		{name: "the exposed dsn flag", expected: "`dokku redis:info lollipop --exposed-dsn`"},
		{name: "the exposed dsn host", expected: "`expose-host` property, set with `dokku redis:set`"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !strings.Contains(readme, test.expected) {
				t.Errorf("expected the readme to contain %q", test.expected)
			}
		})
	}

	if strings.Contains(readme, "\n\n\n") {
		t.Error("expected the readme to have no runs of blank lines")
	}

	if !strings.HasSuffix(readme, "disabled.\n") {
		t.Error("expected the readme to end in a single newline")
	}
}

func TestReadmeIsStable(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")

	input := ReadmeInput{Commands: helpTestCommands(), Data: redisDocumentationData(t)}
	first, err := Readme(input)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	for i := 0; i < 5; i++ {
		again, err := Readme(input)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}

		if again != first {
			t.Fatal("expected the readme to be identical on every run")
		}
	}
}

func TestReadmeIncludesTheSponsors(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")

	readme, err := Readme(ReadmeInput{
		Commands: helpTestCommands(),
		Data:     redisDocumentationData(t),
		Sponsors: []string{"josegonzalez", "dokku"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	for _, expected := range []string{"## Sponsors", "- [josegonzalez](https://github.com/josegonzalez)", "- [dokku](https://github.com/dokku)"} {
		if !strings.Contains(readme, expected) {
			t.Errorf("expected the readme to contain %q", expected)
		}
	}
}

func TestReadmeIncludesTheExtraDocumentation(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")

	pluginDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pluginDir, "docs"), 0755); err != nil {
		t.Fatalf("unable to create the docs directory: %s", err)
	}

	if err := os.WriteFile(filepath.Join(pluginDir, "docs", "backup.md"), []byte("Backups are stored forever.\n"), 0644); err != nil {
		t.Fatalf("unable to write the extra documentation: %s", err)
	}

	readme, err := Readme(ReadmeInput{
		Commands:  helpTestCommands(),
		Data:      redisDocumentationData(t),
		PluginDir: pluginDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if !strings.Contains(readme, "Backups are stored forever.") {
		t.Error("expected the readme to contain the extra documentation")
	}
}

func TestPluginSponsors(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		expected []string
	}{
		{
			name:     "a plugin with sponsors",
			contents: "[plugin]\nsponsors = [\"josegonzalez\", \"dokku\"]\n",
			expected: []string{"josegonzalez", "dokku"},
		},
		{
			name:     "a plugin without sponsors",
			contents: "[plugin]\ndescription = \"dokku redis service plugin\"\n",
			expected: []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pluginDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(pluginDir, "plugin.toml"), []byte(test.contents), 0644); err != nil {
				t.Fatalf("unable to write the plugin manifest: %s", err)
			}

			sponsors, err := PluginSponsors(pluginDir)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if strings.Join(sponsors, ",") != strings.Join(test.expected, ",") {
				t.Errorf("expected %v, got %v", test.expected, sponsors)
			}
		})
	}
}

func TestPluginSponsorsWithoutAManifest(t *testing.T) {
	sponsors, err := PluginSponsors(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if len(sponsors) != 0 {
		t.Errorf("expected no sponsors, got %v", sponsors)
	}
}

// The section is only written for a datastore whose tools take extra arguments.
func TestReadmeExtraArgs(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")
	clearImageEnv(t)

	for name, expected := range map[string]bool{"mysql": true, "redis": false} {
		t.Run(name, func(t *testing.T) {
			readme, err := Readme(ReadmeInput{
				Commands: helpTestCommands(),
				Data:     NewDocumentationData(DocumentationDataInput{Datastore: service.Datastores[name]}),
			})
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if actual := strings.Contains(readme, "### Passing extra arguments to export and import"); actual != expected {
				t.Errorf("expected the extra arguments section to be present to be %t, got %t", expected, actual)
			}

			if expected && !strings.Contains(readme, "`export-args` or `import-args` property with `dokku "+name+":set`") {
				t.Error("expected the section to name both properties and the set command")
			}
		})
	}
}

// The section is only written for a datastore that reserves names, and lists
// every one of them.
func TestReadmeReservedNames(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")
	clearImageEnv(t)

	for name, expected := range map[string]bool{"mysql": true, "redis": false} {
		t.Run(name, func(t *testing.T) {
			readme, err := Readme(ReadmeInput{
				Commands: helpTestCommands(),
				Data:     NewDocumentationData(DocumentationDataInput{Datastore: service.Datastores[name]}),
			})
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if actual := strings.Contains(readme, "### Reserved service names"); actual != expected {
				t.Errorf("expected the reserved names section to be present to be %t, got %t", expected, actual)
			}

			if expected && !strings.Contains(readme, "`information_schema`, `mysql`, `performance_schema`, `sys`") {
				t.Error("expected the section to list every reserved name")
			}
		})
	}
}

// A definition's own sections are written only for the datastore that declares
// them, after the ones every datastore has and before the docker pull section
// the readme ends with.
func TestReadmeDefinitionSections(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")
	clearImageEnv(t)

	for name, expected := range map[string]bool{"postgres": true, "redis": false} {
		t.Run(name, func(t *testing.T) {
			readme, err := Readme(ReadmeInput{
				Commands: append(helpTestCommands(), CustomCommands(service.Datastores[name])...),
				Data:     NewDocumentationData(DocumentationDataInput{Datastore: service.Datastores[name]}),
			})
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if actual := strings.Contains(readme, "### Encrypting connections with TLS"); actual != expected {
				t.Errorf("expected the tls section to be present to be %t, got %t", expected, actual)
			}

			if actual := strings.Contains(readme, "dokku postgres:certificate <service>"); actual != expected {
				t.Errorf("expected the certificate command to be documented to be %t, got %t", expected, actual)
			}

			if !strings.HasSuffix(readme, "disabled.\n") {
				t.Error("expected the readme to still end with the docker pull section")
			}

			if strings.Contains(readme, "\n\n\n") {
				t.Error("expected the readme to have no runs of blank lines")
			}

			if !expected {
				return
			}

			for _, want := range []string{
				"`/var/lib/dokku/services/postgres/lollipop/certs`",
				"add `sslmode=require` to the dsn",
				"```shell\ndokku postgres:certificate lollipop > server.crt\n```",
				"```\npostgres://postgres:PASSWORD@db.example.com:5432/lollipop?sslmode=verify-ca&sslrootcert=server.crt\n```",
			} {
				if !strings.Contains(readme, want) {
					t.Errorf("expected the tls section to contain %q", want)
				}
			}

			for _, want := range []string{
				"leaves the database in the `SQL_ASCII` encoding",
				"```shell\ndokku postgres:create lollipop --custom-env \"POSTGRES_INITDB_ARGS=--encoding=UTF8 --locale=C\"\n```",
			} {
				if !strings.Contains(readme, want) {
					t.Errorf("expected the encoding section to contain %q", want)
				}
			}

			if strings.Index(readme, "### Reserved service names") > strings.Index(readme, "### Encrypting connections with TLS") {
				t.Error("expected the definition's sections after the ones every datastore has")
			}

			if strings.Index(readme, "### Encrypting connections with TLS") > strings.Index(readme, "### Choosing the database encoding and locale") {
				t.Error("expected the definition's sections in the order it declares them")
			}
		})
	}
}

// A section is a template like the rest of the documentation, heading included,
// and one that does not render stops the readme rather than printing half of it.
func TestReadmeDefinitionSectionsAreTemplates(t *testing.T) {
	data := DocumentationData{
		CommandPrefix: "thing",
		Title:         "Thing",
		Sections: []definition.DocumentationSection{
			{Title: "Using {{.Title}}", Body: "a {{.Title}} service does things.\ndokku {{.CommandPrefix}}:info lollipop\n"},
		},
	}

	sections, err := readmeDefinitionSections(data)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	expected := []string{"### Using Thing", "A Thing service does things.", "```shell\ndokku thing:info lollipop\n```"}
	if strings.Join(sections, "\n\n") != strings.Join(expected, "\n\n") {
		t.Errorf("expected %q, got %q", expected, sections)
	}

	data.Sections[0].Body = "{{.Missing}}"
	if _, err := readmeDefinitionSections(data); err == nil {
		t.Error("expected a section naming missing data to be refused")
	}
}

// The section is only written for a datastore that mounts volumes, and lists
// where each of its definitions mounts each one, since that differs between a
// datastore's major versions.
func TestReadmeVolumeTargets(t *testing.T) {
	t.Setenv("DOKKU_NO_COLOR", "1")
	clearImageEnv(t)

	tests := []struct {
		name     string
		present  bool
		expected []string
	}{
		{name: "memcached", present: false},
		{name: "redis", present: true, expected: []string{"| redis | data | `/data` |", "| redis | config | `/usr/local/etc/redis` |", "`REDIS_VOLUME_TARGETS`"}},
		{name: "postgres", present: true, expected: []string{"| postgres-17 | data | `/var/lib/postgresql/data` |", "| postgres-18 | data | `/var/lib/postgresql` |"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readme, err := Readme(ReadmeInput{
				Commands: helpTestCommands(),
				Data:     NewDocumentationData(DocumentationDataInput{Datastore: service.Datastores[test.name]}),
			})
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			if actual := strings.Contains(readme, "### Moving where a service's volumes are mounted"); actual != test.present {
				t.Errorf("expected the volume targets section to be present to be %t, got %t", test.present, actual)
			}

			for _, expected := range test.expected {
				if !strings.Contains(readme, expected) {
					t.Errorf("expected the section to contain %q", expected)
				}
			}

			// a blank line inside a table ends it
			if test.present && !strings.Contains(readme, "| Definition | Volume | Mounted at |\n| --- | --- | --- |\n| ") {
				t.Error("expected the table rows to follow its header directly")
			}
		})
	}
}
