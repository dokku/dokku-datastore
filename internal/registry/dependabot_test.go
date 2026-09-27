package registry

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// dependabotConfig is the part of .github/dependabot.yml these tests read.
type dependabotConfig struct {
	Updates []struct {
		PackageEcosystem string `yaml:"package-ecosystem"`
		Directory        string `yaml:"directory"`
		Ignore           []struct {
			DependencyName string   `yaml:"dependency-name"`
			UpdateTypes    []string `yaml:"update-types"`
		} `yaml:"ignore"`
	} `yaml:"updates"`
}

// definitionsDirectory is where dependabot finds the definitions, as it names
// the directory: from the repository root.
const definitionsDirectory = "/internal/registry/definitions/"

func loadDependabot(t *testing.T) dependabotConfig {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join("..", "..", ".github", "dependabot.yml"))
	if err != nil {
		t.Fatalf("unable to read dependabot.yml: %s", err)
	}

	config := dependabotConfig{}
	if err := yaml.Unmarshal(contents, &config); err != nil {
		t.Fatalf("unable to parse dependabot.yml: %s", err)
	}

	return config
}

// Dependabot is configured one definition at a time, because a definition split
// by major version has to say which majors it accepts, and that cannot be said
// for a glob. So a definition missing from the list is one nothing watches, and
// a listed directory that no longer exists is an entry that fails every run.
func TestEveryDefinitionIsWatchedByDependabot(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	watched := []string{}
	for _, update := range loadDependabot(t).Updates {
		if update.PackageEcosystem != "docker" {
			continue
		}

		name, found := strings.CutPrefix(update.Directory, definitionsDirectory)
		if !found {
			continue
		}

		watched = append(watched, name)
		if _, ok := loaded.Definition(name); !ok {
			t.Errorf("dependabot watches %s, which is not a definition", update.Directory)
		}
	}

	for _, name := range loaded.Names() {
		if !slices.Contains(watched, name) {
			t.Errorf("%s is not watched by dependabot", name)
		}
	}
}

// An older major exists to keep the services created on it running what they
// were created with, so dependabot must never move it onto the next one.
//
// How that is held depends on how the image writes its tags. Where the major
// leads the tag, as 17.11 and 17-3.5 do, dependabot reads it as the semver
// major and an ignore of semver-major updates holds it. Where the major is a
// suffix, as 0.8.6-pg17 is, dependabot only ever moves a tag to one with the
// same suffix, and nothing more is needed - an ignore there would stop a major
// of the extension itself, which is not one of postgres's.
func TestAnOlderVariantIsHeldInsideItsMajorByDependabot(t *testing.T) {
	loaded, err := Load(LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	ignoresMajors := map[string]string{}
	for _, update := range loadDependabot(t).Updates {
		name, found := strings.CutPrefix(update.Directory, definitionsDirectory)
		if !found {
			continue
		}

		for _, ignore := range update.Ignore {
			if slices.Contains(ignore.UpdateTypes, "version-update:semver-major") {
				ignoresMajors[name] = ignore.DependencyName
			}
		}
	}

	checked := 0
	for _, plugin := range loaded.Plugins() {
		byFlavor := map[string][]string{}
		for _, name := range loaded.NamesFor(plugin) {
			flavor := parseVariant(name).flavor
			byFlavor[flavor] = append(byFlavor[flavor], name)
		}

		for _, names := range byFlavor {
			if len(names) < 2 {
				continue
			}

			// sorted oldest first, so everything but the last is an older variant
			for _, name := range names[:len(names)-1] {
				checked++
				t.Run(name, func(t *testing.T) {
					found, _ := loaded.Definition(name)
					if majorInSuffix(found.DefaultImageVersion, parseVariant(name)) {
						if _, ignored := ignoresMajors[name]; ignored {
							t.Errorf("carries its major in the suffix of %s, so ignoring semver-major updates would only stop the image's own majors",
								found.DefaultImageVersion)
						}
						return
					}

					if ignored := ignoresMajors[name]; ignored != found.DefaultImage {
						t.Errorf("expected dependabot to ignore semver-major updates of %s, got %q",
							found.DefaultImage, ignored)
					}
				})
			}
		}
	}

	if checked == 0 {
		t.Error("expected at least one older variant to check")
	}
}

// majorInSuffix reports whether a tag carries a definition's major after its
// leading version, the way 0.8.6-pg17 does, rather than leading with it.
func majorInSuffix(imageVersion string, found variant) bool {
	if found.prefix == "" {
		return false
	}

	parts := strings.Split(imageVersion, "-")
	return slices.Contains(parts[1:], found.prefix+strconv.Itoa(found.major))
}
