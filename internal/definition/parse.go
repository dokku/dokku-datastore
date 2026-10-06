package definition

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeFile is the subset of a compose document a definition may use. Keys the
// tool owns are named here so that a definition setting one is a load error
// rather than a surprise at create time.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Configs  map[string]Config         `yaml:"configs"`
	Dokku    Dokku                     `yaml:"x-dokku"`
}

// composeService adds the tool-owned keys to Service purely so they can be
// rejected with a message naming them.
type composeService struct {
	Service       `yaml:",inline"`
	ContainerName string    `yaml:"container_name"`
	Hostname      string    `yaml:"hostname"`
	Restart       string    `yaml:"restart"`
	Labels        yaml.Node `yaml:"labels"`
	Logging       yaml.Node `yaml:"logging"`
	Networks      yaml.Node `yaml:"networks"`
}

// ParseInput is the input for Parse.
type ParseInput struct {
	// Name is the definition directory name.
	Name string

	// Compose is the docker-compose.yml.
	Compose []byte

	// Dockerfile is the Dockerfile, whose FROM line supplies the default image.
	Dockerfile []byte

	// Scripts are the bin/ hook scripts, keyed by path relative to bin/.
	Scripts map[string][]byte

	// Rootfs is the image payload, keyed by path relative to rootfs/.
	Rootfs map[string][]byte

	// Privileged are the scripts installed root owned and granted in sudoers,
	// keyed by path relative to privileged/.
	Privileged map[string][]byte
}

// Parse reads and validates a definition. It executes no templates and touches no
// filesystem, so every definition the binary ships can be validated by a unit test.
func Parse(input ParseInput) (Definition, error) {
	file := composeFile{}
	if err := yaml.Unmarshal(input.Compose, &file); err != nil {
		return Definition{}, fmt.Errorf("%s: unable to parse docker-compose.yml: %w", input.Name, err)
	}

	if len(file.Services) != 1 {
		return Definition{}, fmt.Errorf("%s: expected exactly one service, got %d", input.Name, len(file.Services))
	}

	name := ""
	service := composeService{}
	for key, value := range file.Services {
		name = key
		service = value
	}

	image, version, trivial, err := ImageFromDockerfile(input.Dockerfile)
	if err != nil {
		return Definition{}, fmt.Errorf("%s: unable to read the Dockerfile: %w", input.Name, err)
	}

	definition := Definition{
		Name:                input.Name,
		Service:             service.Service,
		Configs:             file.Configs,
		Dokku:               file.Dokku,
		Compose:             input.Compose,
		Dockerfile:          input.Dockerfile,
		DefaultImage:        image,
		DefaultImageVersion: version,
		Builds:              !trivial,
		Scripts:             input.Scripts,
		Rootfs:              input.Rootfs,
		Privileged:          input.Privileged,
	}

	if definition.Dokku.Variable == "" {
		definition.Dokku.Variable = strings.ToUpper(definition.Dokku.Plugin)
	}

	if definition.Dokku.AltAlias == "" {
		// every plugin's alt alias is DOKKU_ plus its variable, graphite's
		// DOKKU_STATSD included, so it is derived rather than restated
		definition.Dokku.AltAlias = "DOKKU_" + definition.Dokku.Variable
	}

	if err := validate(input, name, service, definition); err != nil {
		return Definition{}, err
	}

	return definition, nil
}

// validate rejects a definition the renderer could not honour, so that a mistake
// surfaces when the binary is built rather than when a service is created.
func validate(input ParseInput, serviceKey string, service composeService, definition Definition) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%s: %s", input.Name, fmt.Sprintf(format, args...))
	}

	for key, value := range map[string]bool{
		"container_name": service.ContainerName != "",
		"hostname":       service.Hostname != "",
		"labels":         !service.Labels.IsZero(),
		"logging":        !service.Logging.IsZero(),
		"networks":       !service.Networks.IsZero(),
		"restart":        service.Restart != "",
	} {
		if value {
			return fail("services.%s.%s is set by dokku and may not be set by a definition", serviceKey, key)
		}
	}

	for _, required := range []struct {
		field string
		value string
	}{
		{"plugin", definition.Dokku.Plugin},
		{"title", definition.Dokku.Title},
		{"scheme", definition.Dokku.Scheme},
		{"alias", definition.Dokku.Alias},
		{"dsn", definition.Dokku.DSN},
	} {
		if required.value == "" {
			return fail("x-dokku.%s is required", required.field)
		}
	}

	if definition.Service.Image == "" {
		return fail("services.%s.image is required", serviceKey)
	}

	seen := map[string]bool{}
	primaries := 0
	for _, port := range definition.Service.Ports {
		if port.Name == "" {
			return fail("every port needs a name, so that the dsn and the readiness probe can address one by meaning")
		}

		if seen[port.Name] {
			return fail("port name %q is used twice", port.Name)
		}
		seen[port.Name] = true

		if port.Target == 0 {
			return fail("port %q needs a target", port.Name)
		}

		switch port.Protocol {
		case "", ProtocolTCP, ProtocolUDP:
		default:
			return fail("port %q has protocol %q, which is neither tcp nor udp", port.Name, port.Protocol)
		}

		if port.Primary {
			primaries++
		}
	}

	// readiness is a tcp connect, so a datastore whose readiness would land on
	// a udp port has no way to be waited for. Graphite has a udp primary and
	// names a tcp port to wait on instead, which is the shape this allows.
	if wait, ok := definition.WaitPort(); ok && wait.Protocol == ProtocolUDP {
		return fail("port %q speaks udp, so readiness cannot probe it: name a tcp port in wait", wait.Name)
	}

	if primaries > 1 {
		return fail("only one port may be primary, got %d", primaries)
	}

	for _, volume := range definition.Service.Volumes {
		if volume.Type != "bind" {
			return fail("volume %q must be a bind mount: dokku owns the service root and every read path expects the data on the host", volume.Target)
		}

		if !strings.HasPrefix(volume.Source, "{{ .HostRoot }}") {
			return fail("volume source %q must be rooted at {{ .HostRoot }}, which is the path dockerd sees", volume.Source)
		}

		if volume.Target == "" {
			return fail("volume with source %q needs a target", volume.Source)
		}
	}

	// a volume is addressed by its key wherever a service moves it, both in the
	// volume-targets property and in a template, so a key has to be something
	// both of those can say
	seenKey := map[string]bool{}
	for _, volume := range definition.Service.Volumes {
		key := VolumeKey(volume)
		if key == "" {
			return fail("volume source %q is the service root itself, so the volume has no name to be moved by", volume.Source)
		}

		if strings.ContainsAny(key, " \t\n=:,") || strings.Contains(key, "{{") {
			return fail("volume source %q names the volume %q, which cannot be written as a volume-targets key", volume.Source, key)
		}

		if seenKey[key] {
			return fail("two volumes are mounted from %q", key)
		}
		seenKey[key] = true
	}

	for _, body := range templateBodies(definition, true) {
		for _, key := range TargetReferences(body) {
			if !seenKey[key] {
				return fail("%q names the target of the volume %q, which is not declared", body, key)
			}
		}
	}

	// a config is seeded once and never rewritten, so a target baked into one
	// would go on naming the old path after the service moved the volume
	for name, config := range definition.Configs {
		if strings.Contains(config.Content, ".Target") {
			return fail("config %q names a volume target, which would go stale once the file is seeded", name)
		}
	}

	if definition.Dokku.Wait != "" {
		if _, ok := definition.PortFor(definition.Dokku.Wait); !ok {
			return fail("x-dokku.wait names port %q, which is not declared", definition.Dokku.Wait)
		}
	}

	if definition.Service.Healthcheck == nil && definition.Dokku.Wait == "" && len(definition.Service.Ports) > 0 {
		return fail("a definition needs either a healthcheck or x-dokku.wait, or nothing decides when the service is ready")
	}

	for name, config := range definition.Configs {
		if config.Content == "" {
			// compose can also source a config from a file or an external
			// store; a definition cannot, because the only thing dokku has to
			// seed from is the definition itself
			return fail("config %q needs an inline content", name)
		}
	}

	seenConfig := map[string]bool{}
	for _, config := range definition.Service.Configs {
		if config.Source == "" {
			return fail("every config entry needs a source naming a top level config")
		}

		if _, ok := definition.Configs[config.Source]; !ok {
			return fail("config entry names %q, which is not a top level config", config.Source)
		}

		if config.Target == "" {
			return fail("config %q needs a target", config.Source)
		}

		if seenConfig[config.Target] {
			return fail("two configs are seeded to %q", config.Target)
		}
		seenConfig[config.Target] = true

		// a config outside every bind mount would be written where nothing
		// mounts it, so the container would never see the file
		if _, ok := definition.ServicePath(config.Target); !ok {
			return fail("config %q targets %q, which is not inside any bind mount", config.Source, config.Target)
		}

		if config.Mode != "" {
			if _, err := strconv.ParseUint(config.Mode, 8, 32); err != nil {
				return fail("config %q has mode %q, which is not an octal file mode", config.Source, config.Mode)
			}
		}
	}

	// a file source has to be marked as one, or it would be made a directory
	// before anything could write the file, and something has to make it, or
	// docker would make a directory there in its place
	for _, volume := range definition.Service.Volumes {
		seeded := false
		for _, config := range definition.Service.Configs {
			if path.Clean(config.Target) == path.Clean(volume.Target) {
				seeded = true
			}
		}

		if seeded && !volume.File {
			return fail("volume %q is a config file, so it needs x-file: true", volume.Target)
		}

		if volume.File && !seeded && definition.Dokku.Hooks.PreCreate == nil {
			return fail("volume %q is a file that neither a config nor a pre_create hook makes", volume.Target)
		}
	}

	for name, secret := range definition.Dokku.Secrets {
		if secret.File == "" {
			return fail("secret %q needs a file", name)
		}

		if secret.Length <= 0 {
			return fail("secret %q needs a positive length", name)
		}
	}

	for _, name := range definition.Dokku.ReservedNames {
		if strings.TrimSpace(name) == "" {
			return fail("x-dokku.reserved_names has an empty entry, which would reserve nothing")
		}
	}

	// a section is found by its title when two definitions of a datastore both
	// declare it, so a title has to name exactly one
	seenSection := map[string]bool{}
	for _, section := range definition.Dokku.Documentation {
		if strings.TrimSpace(section.Title) == "" {
			return fail("every x-dokku.documentation section needs a title")
		}

		if strings.TrimSpace(section.Body) == "" {
			return fail("documentation section %q needs a body", section.Title)
		}

		if seenSection[section.Title] {
			return fail("documentation section %q is declared twice", section.Title)
		}
		seenSection[section.Title] = true
	}

	for name := range definition.Dokku.Commands {
		// the base spec is fixed: a datastore's own commands are declared apart
		// from it, so that what the tool implements cannot be extended by a
		// definition choosing a name the tool has never heard of
		if !BaseCommands[name] {
			return fail("commands.%s is not a command the tool implements; declare it under custom_commands", name)
		}
	}

	for name, command := range definition.Dokku.CustomCommands {
		if BaseCommands[name] {
			return fail("custom_commands.%s is a command the tool implements; declare it under commands", name)
		}

		// a custom command nobody can discover is close to one that does not
		// exist, and the help and the readme have nothing else to describe it
		if command.Description == "" {
			return fail("custom command %q needs a description", name)
		}

		// a section nothing matches would leave the command documented nowhere,
		// with the readme rendering as though it had never been declared
		if command.Group != "" && !Groups[command.Group] {
			return fail("custom command %q names the unknown section %q", name, command.Group)
		}
	}

	// a step that migrates the data in place is only ever tried before the
	// export and import it stands in for, so a definition that does not migrate
	// has nothing for it to stand in for, and one that does needs both halves
	upgrade := definition.Dokku.Upgrade
	if len(upgrade.From) > 0 && !upgrade.Migrate {
		return fail("x-dokku.upgrade.from needs x-dokku.upgrade.migrate, since it stands in for the export and import a migration falls back to")
	}

	if upgrade.Migrate && (!definition.Implements("export") || !definition.Implements("import")) {
		return fail("x-dokku.upgrade.migrate needs an export and an import to carry the data across")
	}

	// what one definition's export writes is only known to be what another's
	// import reads when both are declared, and a definition that declares
	// neither still has the export and import subcommands to fall back to
	if (upgrade.Export == nil) != (upgrade.Import == nil) {
		return fail("x-dokku.upgrade.export and x-dokku.upgrade.import are declared together, since a migration uses one definition's export with another's import")
	}

	if upgrade.Export != nil && !upgrade.Migrate {
		return fail("x-dokku.upgrade.export needs x-dokku.upgrade.migrate, since only a migration runs it")
	}

	if upgrade.Import != nil && !upgrade.Import.Stdin {
		return fail("x-dokku.upgrade.import reads what the export wrote, so it needs stdin: true")
	}

	// what one definition's requires prints is only known to be what another's
	// check reads when both are declared, as with the export and import
	if (upgrade.Requires == nil) != (upgrade.Check == nil) {
		return fail("x-dokku.upgrade.requires and x-dokku.upgrade.check are declared together, since a migration checks one definition's requires with another's check")
	}

	if upgrade.Requires != nil && !upgrade.Migrate {
		return fail("x-dokku.upgrade.requires needs x-dokku.upgrade.migrate, since only a migration runs it")
	}

	if upgrade.Check != nil && !upgrade.Check.Stdin {
		return fail("x-dokku.upgrade.check reads what the requires printed, so it needs stdin: true")
	}

	// the data volume is what a migration moves aside and starts empty again
	if upgrade.Migrate && !seenKey["data"] {
		return fail("x-dokku.upgrade.migrate moves the data volume aside, so it needs a volume mounted from {{ .HostRoot }}/data")
	}

	for from := range upgrade.From {
		if from == input.Name {
			return fail("x-dokku.upgrade.from.%s names this definition, which a service is never moved onto from itself", from)
		}
	}

	// the directory the old data was moved to only exists while an upgrade is
	// running, so anything else naming it would render against nothing
	for _, body := range templateBodies(definition, false) {
		if strings.Contains(body, PreviousDataField) {
			return fail("%q names %s, which only x-dokku.upgrade.from steps are given", body, PreviousDataField)
		}
	}

	// every database is a form of the dumps alone. Backups export it wherever
	// it is declared, so an export without the import that loads it would make
	// backups nothing can restore, and the two take the same arguments so that
	// the export-args and import-args a service keeps hold for either form.
	// Checked in order of name, so that a command is always checked before its
	// own every-database form and the same mistake reports the same error
	commands := allCommands(definition)
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		command := commands[name]
		if command.AllDatabases == nil {
			continue
		}

		if name != "export" && name != "import" {
			return fail("command %q cannot declare all_databases; only export and import do", name)
		}

		if command.AllDatabases.AllDatabases != nil {
			return fail("command %q declares all_databases inside all_databases", name)
		}

		if command.AllDatabases.ExtraArgs != command.ExtraArgs {
			return fail("command %q must take extra arguments in its all_databases form exactly when it does itself", name)
		}
	}

	exportCommand, hasExport := definition.Dokku.Commands["export"]
	importCommand, hasImport := definition.Dokku.Commands["import"]
	exportsAll := hasExport && exportCommand.AllDatabases != nil
	importsAll := hasImport && importCommand.AllDatabases != nil
	if exportsAll != importsAll {
		return fail("export and import must declare all_databases together, so a dump of every database can be loaded again")
	}

	if importsAll && !importCommand.AllDatabases.Stdin {
		return fail("command %q reads the dump from stdin, so it needs stdin: true", "import"+AllDatabasesSuffix)
	}

	for name, command := range allCommands(definition) {
		if len(command.Exec) == 0 {
			return fail("command %q needs an exec", name)
		}

		switch command.Mode {
		case "", ModeService, ModeOffline:
		case ModeSidecar:
			// an image is optional: a sidecar that needs a tool the datastore
			// image lacks names one, and a sidecar that only needs to run
			// beside the service uses the service's own
		case ModeHost:
			// runs on the host rather than in a container, as the dokku user,
			// which is what a trigger reading a build directory needs
		default:
			return fail("command %q has an unknown mode %q", name, command.Mode)
		}

		// nothing else passes arguments on, so extra_args anywhere else would
		// be a setting that does nothing
		if command.ExtraArgs && !takesExtraArgs(name) {
			return fail("command %q cannot take extra arguments; only export and import do", name)
		}

		// a mount of its own belongs to a container started for the command,
		// and is resolved against the service root the way the service's are
		ownContainer := runsOnItsOwn(name) || command.Mode == ModeSidecar || command.Mode == ModeOffline
		if len(command.Volumes) > 0 && !ownContainer {
			return fail("command %q cannot mount volumes of its own; only hooks, upgrade steps and checks, and sidecar and offline commands run in a container of their own", name)
		}

		for _, volume := range command.Volumes {
			if volume.Type != "bind" || !strings.HasPrefix(volume.Source, HostRootTemplate) {
				return fail("command %q mounts %q, which must be a bind mount rooted at {{ .HostRoot }}", name, volume.Source)
			}

			if volume.Target == "" {
				return fail("command %q mounts %q without a target", name, volume.Source)
			}
		}
	}

	// an entrypoint belongs to a container started for the command. One exec'd
	// into the running service, or run on the host, has none to replace, so the
	// setting would do nothing. Hooks and upgrade steps are left out: they
	// always run in a container of their own, as does an upgrade's check.
	for name, command := range allCommands(definition) {
		if command.Entrypoint == nil || runsOnItsOwn(name) {
			continue
		}

		if command.Mode != ModeSidecar && command.Mode != ModeOffline {
			return fail("command %q cannot replace the entrypoint; only sidecar and offline commands run in a container of their own", name)
		}
	}

	return nil
}

// ImageFromDockerfile reads the image a definition pins, and reports whether the
// Dockerfile does anything beyond pinning it. A Dockerfile that only declares its
// base is a no-op the loader can skip building, which is what keeps eighteen of
// the twenty-two definitions on the existing pull path.
func ImageFromDockerfile(contents []byte) (image string, version string, trivial bool, err error) {
	trivial = true
	found := false

	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		switch strings.ToUpper(fields[0]) {
		case "ARG":
			continue
		case "FROM":
			if len(fields) < 2 {
				return "", "", false, fmt.Errorf("FROM needs an image")
			}

			reference := fields[1]
			if strings.HasPrefix(reference, "${") {
				// the ARG IMAGE / FROM ${IMAGE} idiom, so the real default is on
				// the ARG line
				reference = argDefault(contents)
			}

			image, version, found = CutImage(reference)
		default:
			// anything else is a real build: a COPY of vendored tooling, a RUN
			trivial = false
		}
	}

	if image == "" {
		return "", "", false, fmt.Errorf("no FROM instruction")
	}

	if !found {
		version = "latest"
	}

	return image, version, trivial, nil
}

// argDefault returns the default of the IMAGE build argument.
func argDefault(contents []byte) string {
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || strings.ToUpper(fields[0]) != "ARG" {
			continue
		}

		name, value, found := strings.Cut(fields[1], "=")
		if found && name == "IMAGE" {
			return strings.Trim(value, `"`)
		}
	}

	return ""
}

// CutImage splits an image reference into its repository and its tag.
//
// The last colon rather than the first, and only one that comes after the last
// slash. A private registry carries its port in the reference, so redis:8.10
// and registry.example.com:5000/redis:8.10 both have to split on the tag and
// neither on the port: splitting on the first colon recorded the second of
// those as the image registry.example.com at the version 5000/redis:8.10, and
// every later decision was made about a host name.
//
// A reference pinned by digest has no tag at all - the colon in sha256: belongs
// to the digest - so it comes back whole and untagged rather than cut in half.
func CutImage(reference string) (string, string, bool) {
	if strings.Contains(reference, "@") {
		return reference, "", false
	}

	colon := strings.LastIndex(reference, ":")
	if colon < 0 || colon < strings.LastIndex(reference, "/") {
		return reference, "", false
	}

	return reference[:colon], reference[colon+1:], true
}

// targetReference matches a template naming a volume's target, in either of the
// two ways a template can: as a field, or through index for a key a field
// cannot spell.
var targetReference = regexp.MustCompile(`\.Target\.([A-Za-z0-9_]+)|index\s+\.Target\s+"([^"]*)"`)

// TargetReferences returns the volume keys a template names the target of.
func TargetReferences(body string) []string {
	keys := []string{}
	for _, match := range targetReference.FindAllStringSubmatch(body, -1) {
		if match[1] != "" {
			keys = append(keys, match[1])
			continue
		}

		keys = append(keys, match[2])
	}

	return keys
}

// templateBodies is every template a definition renders against a service's
// scope, for the checks that hold for all of them. The upgrade steps are left
// out unless asked for, since they are the only ones rendered against more than
// a service's own scope.
func templateBodies(definition Definition, withUpgrade bool) []string {
	bodies := []string{definition.Service.Image, definition.Service.WorkingDir, definition.Dokku.DSN}
	bodies = append(bodies, definition.Service.Command...)
	for _, value := range definition.Service.Environment {
		bodies = append(bodies, value)
	}

	if definition.Service.Healthcheck != nil {
		bodies = append(bodies, definition.Service.Healthcheck.Test...)
	}

	for name, command := range allCommands(definition) {
		if !withUpgrade && strings.HasPrefix(name, "upgrade.from.") {
			continue
		}

		bodies = append(bodies, command.Image)
		bodies = append(bodies, command.Exec...)
		for _, value := range command.Env {
			bodies = append(bodies, value)
		}
	}

	return bodies
}

// allCommands is every command a definition declares, base and custom, for the
// checks that apply to both.
func allCommands(definition Definition) map[string]Command {
	commands := map[string]Command{}
	if definition.Dokku.Hooks.PreCreate != nil {
		commands["hooks.pre_create"] = *definition.Dokku.Hooks.PreCreate
	}

	if definition.Dokku.Hooks.PostCreate != nil {
		commands["hooks.post_create"] = *definition.Dokku.Hooks.PostCreate
	}

	// the every-database forms are commands of their own, checked and rendered
	// as the rest are
	for name, command := range definition.Dokku.Commands {
		commands[name] = command
		if command.AllDatabases != nil {
			commands[name+AllDatabasesSuffix] = *command.AllDatabases
		}
	}

	for name, command := range definition.Dokku.CustomCommands {
		commands[name] = command
		if command.AllDatabases != nil {
			commands[name+AllDatabasesSuffix] = *command.AllDatabases
		}
	}

	for name, command := range definition.Dokku.Triggers {
		commands["triggers."+name] = command
	}

	for name, command := range definition.Dokku.Upgrade.From {
		commands["upgrade.from."+name] = command
	}

	if definition.Dokku.Upgrade.Export != nil {
		commands["upgrade.export"] = *definition.Dokku.Upgrade.Export
	}

	if definition.Dokku.Upgrade.Import != nil {
		commands["upgrade.import"] = *definition.Dokku.Upgrade.Import
	}

	if definition.Dokku.Upgrade.Requires != nil {
		commands["upgrade.requires"] = *definition.Dokku.Upgrade.Requires
	}

	if definition.Dokku.Upgrade.Check != nil {
		commands["upgrade.check"] = *definition.Dokku.Upgrade.Check
	}

	return commands
}

// runsOnItsOwn reports whether a command, addressed as allCommands names it,
// always runs in a container started for it rather than in the service's own.
func runsOnItsOwn(name string) bool {
	return strings.HasPrefix(name, "hooks.") || strings.HasPrefix(name, "upgrade.from.") || name == "upgrade.check"
}

// takesExtraArgs reports whether a command, addressed as allCommands names it,
// is one an operator's extra arguments are passed on to.
func takesExtraArgs(name string) bool {
	switch strings.TrimSuffix(name, AllDatabasesSuffix) {
	case "export", "import":
		return true
	default:
		return false
	}
}
