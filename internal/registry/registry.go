// Package registry resolves datastore types to the definitions implementing them,
// from the tree embedded in the binary and from a plugin checkout that may
// override it.
package registry

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dokku/dokku-datastore/internal/definition"
)

//go:embed all:definitions
var embedded embed.FS

// overrideDir is where a plugin checkout puts its own definitions. It is named
// rather than sitting at the repo root because a datastore split by major version
// has more than one definition and the root cannot say which.
const overrideDir = "datastore"

// Registry is every datastore this binary knows about.
type Registry struct {
	// definitions are keyed by definition name, e.g. "postgres-18".
	definitions map[string]definition.Definition

	// byPlugin groups definition names by command prefix, so that postgres-17
	// and postgres-18 are both reachable as "postgres".
	byPlugin map[string][]string
}

// LoadInput is the input for Load.
type LoadInput struct {
	// PluginDir is a plugin checkout. A plugin that ships definitions for a
	// datastore supplies all of them: the embedded ones are dropped rather than
	// merged, because a half merged datastore is a set of variants nobody wrote
	// down, and a service pinned to one the plugin does not ship would run a
	// definition its plugin cannot see.
	PluginDir string
}

// Load resolves every definition. A malformed definition in a plugin checkout is
// an error rather than a silent fall back to the embedded one, since falling back
// would quietly run something other than what the operator asked for.
func Load(input LoadInput) (*Registry, error) {
	registry := &Registry{
		definitions: map[string]definition.Definition{},
		byPlugin:    map[string][]string{},
	}

	names, err := fs.ReadDir(embedded, "definitions")
	if err != nil {
		return nil, fmt.Errorf("unable to read the embedded definitions: %w", err)
	}

	for _, entry := range names {
		if !entry.IsDir() {
			continue
		}

		parsed, err := parseEmbedded(entry.Name())
		if err != nil {
			return nil, err
		}

		registry.add(parsed)
	}

	if input.PluginDir != "" {
		overrides, err := parseOverrides(filepath.Join(input.PluginDir, overrideDir))
		if err != nil {
			return nil, err
		}

		if err := agreeOnPlugin(overrides); err != nil {
			return nil, err
		}

		for _, plugin := range overriddenPlugins(overrides) {
			registry.drop(plugin)
		}

		for _, parsed := range overrides {
			// the drop above cleared this plugin's own names, so anything still
			// standing under one of them belongs to a different datastore, and
			// adding over it would leave that datastore pointing at a definition
			// that is not its own
			if existing, taken := registry.definitions[parsed.Name]; taken {
				return nil, fmt.Errorf("%s: a %s definition is already named this", parsed.Name, existing.Dokku.Plugin)
			}

			registry.add(parsed)
		}
	}

	return registry, nil
}

// agreeOnPlugin refuses a checkout whose definitions do not all belong to the
// same datastore.
//
// A plugin ships the definitions for its own datastore and no other. Without
// this a dokku-redis checkout could add a postgres definition and the redis
// plugin would start answering for postgres, which is not a datastore it
// installs, documents or has commands for.
//
// The directory name is deliberately not checked against the plugin. A variant
// is named <plugin>-<major>, and the plugin's own name is not recoverable from
// the path either way: at runtime the checkout is <base>/<prefix>, but under
// generate it is whatever --plugin-dir was given, which may be a clone directory
// named anything at all.
func agreeOnPlugin(overrides []definition.Definition) error {
	if len(overrides) == 0 {
		return nil
	}

	expected := overrides[0].Dokku.Plugin
	for _, parsed := range overrides[1:] {
		if parsed.Dokku.Plugin != expected {
			return fmt.Errorf("%s: declares plugin %s, but %s declares %s: a plugin ships definitions for one datastore",
				parsed.Name, parsed.Dokku.Plugin, overrides[0].Name, expected)
		}
	}

	return nil
}

// overriddenPlugins is every datastore a checkout supplies definitions for, in
// the order they are first seen.
func overriddenPlugins(overrides []definition.Definition) []string {
	seen := map[string]bool{}
	plugins := []string{}
	for _, parsed := range overrides {
		if seen[parsed.Dokku.Plugin] {
			continue
		}

		seen[parsed.Dokku.Plugin] = true
		plugins = append(plugins, parsed.Dokku.Plugin)
	}

	return plugins
}

// drop forgets every definition belonging to a datastore.
func (r *Registry) drop(plugin string) {
	for _, name := range r.byPlugin[plugin] {
		delete(r.definitions, name)
	}

	delete(r.byPlugin, plugin)
}

// add records a definition under its name and its datastore.
func (r *Registry) add(parsed definition.Definition) {
	if _, exists := r.definitions[parsed.Name]; !exists {
		r.byPlugin[parsed.Dokku.Plugin] = append(r.byPlugin[parsed.Dokku.Plugin], parsed.Name)
		sortVariants(r.byPlugin[parsed.Dokku.Plugin])
	}

	r.definitions[parsed.Name] = parsed
}

// parseEmbedded reads one definition out of the tree compiled into the binary.
func parseEmbedded(name string) (definition.Definition, error) {
	read := func(path string) ([]byte, error) {
		return embedded.ReadFile(filepath.Join("definitions", name, path))
	}

	compose, err := read("docker-compose.yml")
	if err != nil {
		return definition.Definition{}, fmt.Errorf("%s: unable to read docker-compose.yml: %w", name, err)
	}

	dockerfile, err := read("Dockerfile")
	if err != nil {
		return definition.Definition{}, fmt.Errorf("%s: unable to read Dockerfile: %w", name, err)
	}

	scripts, err := readTree(embedded, path.Join("definitions", name, "bin"))
	if err != nil {
		return definition.Definition{}, fmt.Errorf("%s: %w", name, err)
	}

	rootfs, err := readTree(embedded, path.Join("definitions", name, "rootfs"))
	if err != nil {
		return definition.Definition{}, fmt.Errorf("%s: %w", name, err)
	}

	privileged, err := readTree(embedded, path.Join("definitions", name, "privileged"))
	if err != nil {
		return definition.Definition{}, fmt.Errorf("%s: %w", name, err)
	}

	return definition.Parse(definition.ParseInput{
		Name:       name,
		Compose:    compose,
		Dockerfile: dockerfile,
		Scripts:    scripts,
		Rootfs:     rootfs,
		Privileged: privileged,
	})
}

// readTree reads every file below a directory, keyed by path relative to it. A
// missing directory is not an error: most definitions ship neither bin/ nor
// rootfs/. Rootfs is a tree rather than a flat list, so this walks rather than
// listing one level, which is what lets a definition place a file anywhere in
// the image.
func readTree(fsys fs.FS, root string) (map[string][]byte, error) {
	files := map[string][]byte{}

	err := fs.WalkDir(fsys, root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}

			return err
		}

		if entry.IsDir() {
			return nil
		}

		contents, err := fs.ReadFile(fsys, current)
		if err != nil {
			return fmt.Errorf("unable to read %s: %w", current, err)
		}

		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}

		files[filepath.ToSlash(relative)] = contents
		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, nil
	}

	return files, nil
}

// parseOverrides reads every definition a plugin checkout supplies.
func parseOverrides(root string) ([]definition.Definition, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("unable to read %s: %w", root, err)
	}

	parsed := []definition.Definition{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		compose, err := os.ReadFile(filepath.Join(root, name, "docker-compose.yml"))
		if os.IsNotExist(err) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("%s: unable to read docker-compose.yml: %w", name, err)
		}

		dockerfile, err := os.ReadFile(filepath.Join(root, name, "Dockerfile"))
		if err != nil {
			return nil, fmt.Errorf("%s: unable to read Dockerfile: %w", name, err)
		}

		checkout := os.DirFS(filepath.Join(root, name))

		scripts, err := readTree(checkout, "bin")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}

		rootfs, err := readTree(checkout, "rootfs")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}

		privileged, err := readTree(checkout, "privileged")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}

		one, err := definition.Parse(definition.ParseInput{
			Name:       name,
			Compose:    compose,
			Dockerfile: dockerfile,
			Scripts:    scripts,
			Rootfs:     rootfs,
			Privileged: privileged,
		})
		if err != nil {
			return nil, err
		}

		parsed = append(parsed, one)
	}

	return parsed, nil
}

// Definition returns a definition by name.
func (r *Registry) Definition(name string) (definition.Definition, bool) {
	found, ok := r.definitions[name]
	return found, ok
}

// For returns the definition a service of a given datastore type runs. Where a
// datastore is split by major version the imageVersion selects between them; a
// service with no recorded version, which is what a service whose container is
// gone looks like, gets the newest rather than an error.
func (r *Registry) For(plugin string, imageVersion string) (definition.Definition, error) {
	names, ok := r.byPlugin[plugin]
	if !ok {
		return definition.Definition{}, fmt.Errorf("datastore type %s is not supported", plugin)
	}

	if len(names) == 1 {
		return r.definitions[names[0]], nil
	}

	major := majorVersion(imageVersion)
	if major != "" {
		if found, ok := r.definitions[plugin+"-"+major]; ok {
			return found, nil
		}
	}

	// byPlugin is sorted, so the last entry is the newest major
	return r.definitions[names[len(names)-1]], nil
}

// NamesFor returns every definition belonging to a datastore type, oldest first.
func (r *Registry) NamesFor(plugin string) []string {
	return append([]string{}, r.byPlugin[plugin]...)
}

// Plugins returns every datastore type, sorted.
func (r *Registry) Plugins() []string {
	plugins := make([]string, 0, len(r.byPlugin))
	for plugin := range r.byPlugin {
		plugins = append(plugins, plugin)
	}

	sort.Strings(plugins)
	return plugins
}

// Names returns every definition name, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.definitions))
	for name := range r.definitions {
		names = append(names, name)
	}

	sort.Strings(names)
	return names
}

// sortVariants orders a datastore's definitions oldest first.
//
// By the number rather than by the name: a datastore that reaches a tenth major
// version would otherwise sort it before its seventh, and the newest, which is
// what a service with no recorded version falls back to, would be the wrong one.
func sortVariants(names []string) {
	sort.Slice(names, func(i int, j int) bool {
		left, right := variantVersion(names[i]), variantVersion(names[j])
		if left != right {
			return left < right
		}

		return names[i] < names[j]
	})
}

// variantVersion is the major version a definition's name ends in, or zero for
// a datastore that is not split by version.
func variantVersion(name string) int {
	_, suffix, found := strings.Cut(name, "-")
	if !found {
		return 0
	}

	version, err := strconv.Atoi(suffix)
	if err != nil {
		return 0
	}

	return version
}

// majorVersion returns the leading numeric component of an image tag, so that
// 18.4 selects postgres-18 and v1.45.0 selects nothing in particular.
func majorVersion(imageVersion string) string {
	digits := strings.Builder{}
	for _, char := range strings.TrimPrefix(imageVersion, "v") {
		if char < '0' || char > '9' {
			break
		}

		digits.WriteRune(char)
	}

	return digits.String()
}
