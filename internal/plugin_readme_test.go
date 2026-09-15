package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	readme, err := Readme(ReadmeInput{
		Commands: helpTestCommands(),
		Data:     redisDocumentationData(t),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	tests := []struct {
		name     string
		expected string
	}{
		{name: "the title", expected: "# dokku redis "},
		// the version a real plugin checkout pins in its own Dockerfile wins, so
		// this fixture falls back to the definition's, which is a pinned
		// version rather than the floating latest the Go implementation used
		{name: "the image it installs", expected: "Currently defaults to installing [redis 8.8.0](https://hub.docker.com/_/redis/)."},
		{name: "the install command", expected: "sudo dokku plugin:install https://github.com/dokku/dokku-redis.git --name redis"},
		{name: "the command list", expected: "redis:list                                         # list all Redis services"},
		{name: "a usage section", expected: "### Basic Usage"},
		{name: "a command heading", expected: "### create a backup of the Redis service to an existing s3 bucket"},
		{name: "a usage block", expected: "```shell\n# usage\ndokku redis:backup <service> <bucket-name> [-u|--use-iam]\n```"},
		{name: "a flag", expected: "- `-u|--use-iam`: use the IAM profile associated with the current server"},
		{name: "prose from the documentation", expected: "Backup the `lollipop` service to the `my-s3-bucket` bucket on `AWS`:"},
		{name: "a command from the documentation", expected: "```shell\ndokku redis:backup lollipop my-s3-bucket --use-iam\n```"},
		{name: "the docker pull section", expected: "`REDIS_DISABLE_PULL` environment variable"},
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
