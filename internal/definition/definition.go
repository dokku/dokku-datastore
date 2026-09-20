// Package definition parses and validates a datastore definition: a
// docker-compose.yml carrying an x-dokku extension block, alongside a Dockerfile
// that pins the image. A definition is inert. It says what a service of this type
// looks like, never what any particular service looks like; rendering it against a
// service's state is what produces something runnable.
package definition

import (
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Definition is one datastore definition directory.
type Definition struct {
	// Name is the directory the definition was loaded from, e.g. "postgres-18".
	// It is not the command prefix: postgres-17 and postgres-18 share the prefix
	// "postgres".
	Name string

	// Service is the compose service describing the datastore container. Exactly
	// one service is allowed, so there is no ambiguity about which container the
	// verbs, logs and enter address.
	Service Service

	// Dokku is the x-dokku block: everything compose has no vocabulary for.
	Dokku Dokku

	// Configs are the top level configs, keyed by the name a service config
	// entry names in its source.
	Configs map[string]Config

	// Compose is the docker-compose.yml exactly as it was read. A definition may
	// use compose keys this binary does not model, and the comments in one are
	// where a definition explains itself, so a plugin shipping its own copy is
	// handed these bytes rather than a re-serialised parse of them.
	Compose []byte

	// Dockerfile is the definition's Dockerfile. Its FROM line is the default
	// image, the way the bash plugins' config derives the image with awk.
	Dockerfile []byte

	// DefaultImage and DefaultImageVersion are what the Dockerfile pins, which
	// is what a service gets when it names no image of its own. They are not
	// Service.Image, which is a template a service's own pin renders into.
	DefaultImage        string
	DefaultImageVersion string

	// Builds reports whether the Dockerfile does anything beyond declaring its
	// base. No definition shipped today does: a vendored script is mounted from
	// Rootfs rather than baked in, which keeps every datastore on the pull path.
	// It stays for the payload a mount cannot deliver, such as a compiled tool
	// or a package the base image lacks.
	Builds bool

	// Scripts are the bin/ hook scripts, keyed by path relative to bin/.
	Scripts map[string][]byte

	// Rootfs is the payload that appears inside the container, keyed by path
	// relative to rootfs/. Each file is written into the service directory and
	// mounted at its own path, read only. It is kept apart from Scripts because
	// the two are read by different things: bin/ runs on the host, rootfs/ runs
	// inside the container.
	Rootfs map[string][]byte

	// Privileged are the scripts installed root owned outside the service root
	// and granted in sudoers, keyed by path relative to privileged/. They are
	// how a definition does the one part of an operation that needs root.
	//
	// A plugin may ship one. Installing a plugin is a root action, and after it
	// the whole plugin tree - its scripts and its definitions alike - is owned by
	// the dokku user and its install script is run by root, so a definition is
	// trusted exactly as far as the plugin carrying it already was.
	Privileged map[string][]byte
}

// Service is the compose service. Only the keys the renderer has an opinion about
// are named; a definition may use compose features this binary does not yet
// understand without the binary having to be taught about them.
type Service struct {
	// Image is the image reference, templated so a definition can defer to the
	// version a service pinned: "{{ .Image }}:{{ .ImageVersion }}".
	Image string `yaml:"image"`

	// Command is the argv after the image. The user's config options are appended
	// after it, split the way a shell would split them.
	Command []string `yaml:"command"`

	// Environment are environment variables, values templated.
	Environment map[string]string `yaml:"environment"`

	// Volumes are bind mounts. Every source must be rooted at {{ .HostRoot }},
	// which is validated, so a mount is always absolute and always inside the
	// service's own directory.
	Volumes []Volume `yaml:"volumes"`

	// Ports are the container ports, each named so that the dsn, the readiness
	// probe and the info report address a port by meaning rather than by
	// position. This is what replaces the positional PLUGIN_DATASTORE_PORTS
	// tuple, where graphite waits on the third entry.
	Ports []Port `yaml:"ports"`

	// Healthcheck decides readiness for images shipping a usable probe tool.
	// A definition without one falls back to the dokku/wait sidecar against the
	// port named by Dokku.Wait.
	Healthcheck *Healthcheck `yaml:"healthcheck"`

	// Configs are the files seeded into the service's config directory before
	// the container starts. They replace the hand written config seeding every
	// plugin does in bash.
	Configs []ServiceConfig `yaml:"configs"`
}

// Config is a top level config: a file this definition supplies the content of.
//
// This deliberately means something other than what compose means. A docker
// config is immutable and re-projected on every start, but an operator edits a
// datastore's config file and expects the edit to survive a restart, so dokku
// seeds the content once into the bind mounted config directory and never
// clobbers it afterwards. The consequence is that a credential change would
// leave a seeded file stale, which nothing needs today.
type Config struct {
	// Content is the file's body, templated against the service's state.
	Content string `yaml:"content"`
}

// ServiceConfig mounts a top level config into the service.
type ServiceConfig struct {
	// Source is the key in the top level configs.
	Source string `yaml:"source"`

	// Target is the path inside the container, which must fall inside one of the
	// service's bind mounts or nothing would ever put the file there.
	Target string `yaml:"target"`

	// UID and GID own the seeded file, for images running as a user that has to
	// read it. Empty means the dokku user, as every datastore uses today.
	UID string `yaml:"uid"`
	GID string `yaml:"gid"`

	// Mode is the octal file mode, defaulting to 0644.
	Mode string `yaml:"mode"`
}

// Volume is a compose long-syntax bind mount.
type Volume struct {
	// Type is always "bind". Named volumes are rejected: dokku owns the service
	// root and every read path assumes the data is there on the host.
	Type string `yaml:"type"`

	// Source is the host path, which must be rooted at {{ .HostRoot }}.
	Source string `yaml:"source"`

	// Target is the path inside the container.
	Target string `yaml:"target"`
}

// Port is a compose long-syntax port entry with a mandatory name.
type Port struct {
	// Name is what everything else addresses the port by: "native", "http",
	// "management". It replaces indexing into a slice.
	Name string `yaml:"name"`

	// Target is the port inside the container.
	Target int `yaml:"target"`

	// Protocol is tcp or udp, defaulting to tcp. graphite's statsd port is udp
	// and has always been exposed as tcp, which this can finally say.
	Protocol string `yaml:"protocol"`

	// Primary marks the port the dsn and the readiness probe use when neither
	// names one. It replaces ServiceStruct.WaitPort and Ports[0].
	Primary bool `yaml:"primary"`
}

// Healthcheck is the compose healthcheck.
type Healthcheck struct {
	Test        []string `yaml:"test"`
	Interval    string   `yaml:"interval"`
	Timeout     string   `yaml:"timeout"`
	Retries     int      `yaml:"retries"`
	StartPeriod string   `yaml:"start_period"`
}

// Dokku is the x-dokku block: the parts of a datastore definition the compose
// specification has no vocabulary for.
type Dokku struct {
	// Plugin is the command prefix and the label on every container. Definitions
	// differing only by major version share it.
	Plugin string `yaml:"plugin"`

	// DataDirectory is the directory under the services root that this
	// datastore's services live in. It is the plugin name for every datastore
	// but graphite, whose services have always been stored under the name of the
	// image it runs rather than the name it is called by.
	//
	// Declared rather than assumed so that a datastore whose directory is not its
	// command name can say so, instead of the services an operator already has
	// becoming invisible the moment the binary looks somewhere else for them.
	DataDirectory string `yaml:"data_directory"`

	// Title is the datastore's name in prose. It is declared rather than derived
	// because it is irregular: MariaDB, CouchDB, MongoDB, RabbitMQ.
	Title string `yaml:"title"`

	// Variable is the environment variable prefix. It defaults to the uppercased
	// plugin name and is declared only by graphite, whose variable is STATSD.
	Variable string `yaml:"variable"`

	// Scheme is the default url scheme. It is neither the plugin name nor
	// derivable from it: graphite is statsd, mariadb is mysql, couchdb is http.
	// An app may override it per link with <Variable>_DATABASE_SCHEME.
	Scheme string `yaml:"scheme"`

	// Alias is the prefix of the config variable a linked app receives the url
	// on. Not derivable either: postgres, mysql and mariadb all use DATABASE.
	Alias string `yaml:"alias"`

	// AltAlias prefixes the generated alternate alias when Alias is taken.
	AltAlias string `yaml:"alt-alias"`

	// DSN is the url template a linked app receives. It is a template rather
	// than a set of flags because the plugins use seven different url shapes.
	DSN string `yaml:"dsn"`

	// WaitTimeout bounds the readiness probe, in seconds. A datastore that takes
	// longer than the probe's own default to answer says so, rather than being
	// reported as never having started.
	WaitTimeout int `yaml:"wait_timeout"`

	// Wait names the port to probe with the dokku/wait sidecar, for images with
	// no tool a compose healthcheck could use. Ignored when Healthcheck is set.
	Wait string `yaml:"wait"`

	// Secrets are the credentials generated once at create time and persisted
	// under the service root, keyed by the name templates refer to them by.
	Secrets map[string]Secret `yaml:"secrets"`

	// Requirements are what a datastore needs from the machine rather than from
	// docker, checked before a service is created so that a host which cannot
	// run it says so rather than leaving a container to fail obscurely.
	Requirements []Requirement `yaml:"requirements"`

	// Hooks are the steps a datastore needs run around a service's lifecycle,
	// which are commands in every respect except that they are not subcommands:
	// a user does not invoke them, the tool does.
	Hooks Hooks `yaml:"hooks"`

	// CustomCommands are the operations this datastore adds, which the tool
	// knows nothing about beyond how to run them. They are declared apart from
	// Commands so that being custom is a fact about the definition rather than
	// about what other datastores happen to declare.
	CustomCommands map[string]Command `yaml:"custom_commands"`

	// Triggers are the dokku plugin triggers this datastore implements, keyed
	// by trigger name. dokku finds a trigger by looking for a file named after
	// it in the plugin directory, so a plugin still ships one; declaring the
	// work here is what lets that file be generated rather than written, and
	// keeps the datastore specific half in the definition.
	//
	// A trigger runs once for each service the app is linked to, which is the
	// shape every one of them has: the tool finds the services and the
	// definition says what to do with each.
	Triggers map[string]Command `yaml:"triggers"`

	// Commands are the operations run against a service. The well-known keys
	// connect, export and import drive the built-in subcommands, and a key's
	// absence is what makes that subcommand unimplemented. Any other key becomes
	// an extra subcommand.
	Commands map[string]Command `yaml:"commands"`
}

// Secret is a credential generated at create time and written to the service root.
type Secret struct {
	// File is the service-root filename, PASSWORD or ROOTPASSWORD. These names
	// are part of the on-disk contract the bash plugins established.
	File string `yaml:"file"`

	// Length is the generated length in hex characters. The plugins use 16, 32
	// and 64; a definition records what its datastore actually uses.
	Length int `yaml:"length"`

	// Env is an environment variable that overrides generation, which is what
	// finally wires up --password and --root-password.
	Env string `yaml:"env"`
}

// Command is one operation against a service.
type Command struct {
	// Exec is the argv, each element a template. Argv rather than a shell string,
	// so a password containing a quote cannot break the command.
	Exec []string `yaml:"exec"`

	// Env is passed with docker exec --env rather than through argv, so secrets
	// stay out of the container's process table.
	Env map[string]string `yaml:"env"`

	// Mode is where Exec runs: service, offline, sidecar or host.
	Mode string `yaml:"mode"`

	// Image is the sidecar image, for Mode sidecar.
	Image string `yaml:"image"`

	// Volumes are mounts this command needs that the service's own do not
	// provide. A step that seeds a directory the service then mounts over has
	// to reach that directory by another path, or it would be looking at what
	// it is trying to fill.
	Volumes []Volume `yaml:"volumes"`

	// User is the in-container user to run as.
	User string `yaml:"user"`

	// Entrypoint replaces the image's own. A step that runs a plain command in
	// an image whose entrypoint starts the datastore has to clear it, or the
	// datastore starts instead of the command.
	Entrypoint *string `yaml:"entrypoint"`

	// Stdin is true when the command consumes standard input, as import does.
	Stdin bool `yaml:"stdin"`

	// Description and Arguments document a custom command. The base verbs take
	// theirs from the tool.
	Description string     `yaml:"description"`
	Arguments   []Argument `yaml:"arguments"`

	// Documentation is the long form prose the readme renders for a custom
	// command, and Group is the section it appears under.
	Documentation string `yaml:"documentation"`
	Group         string `yaml:"group"`
}

// Requirement is a precondition on the host.
type Requirement struct {
	// Sysctl is the kernel parameter to read, such as vm.max_map_count.
	Sysctl string `yaml:"sysctl"`

	// Minimum is the lowest acceptable value.
	Minimum int `yaml:"minimum"`

	// Message is what the operator is told when it is not met, which has to
	// say how to fix it: knowing a number is too low is not knowing what to do.
	Message string `yaml:"message"`
}

// Hooks are the steps run around a service's lifecycle.
type Hooks struct {
	// PreCreate runs before a service's container is created, when nothing of
	// the service exists but its directories. Clickhouse needs it: it mounts a
	// directory over the one its own configuration lives in, so the image's
	// configuration has to be copied out before the mount hides it.
	PreCreate *Command `yaml:"pre_create"`

	// PostCreate runs once a newly created service is answering, which is when
	// a datastore that cannot be set up by its image's own initialisation has
	// to be set up by hand. Couchdb's database is made this way, because the
	// image creates an admin account and nothing else.
	PostCreate *Command `yaml:"post_create"`
}

// Argument is a positional argument of an extra subcommand.
type Argument struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Optional    bool   `yaml:"optional"`
}

// Modes a Command may run in.
const (
	// ModeService execs into the running datastore container. The default.
	ModeService = "service"

	// ModeOffline stops the service, runs a fresh container on the same image
	// and volumes, then starts it again. Redis's import needs it, because redis
	// reads its dump at boot and so the file cannot be swapped underneath a
	// running server.
	ModeOffline = "offline"

	// ModeSidecar runs in the command's own image, joined to the service network.
	// Memcached's connect needs it, since its image ships no client.
	ModeSidecar = "sidecar"

	// ModeHost runs on the host, with argv[0] resolved against the definition's
	// bin/ directory. Only graphite's nginx subcommands need it, and it is
	// accepted only from the embedded layer.
	ModeHost = "host"
)

// The readme usage sections a command can be documented under.
//
// Identifiers rather than the headings they render as, so that retitling a
// section does not rebucket every command in it and a definition declaring one
// is held to a value rather than to prose. The heading itself lives with the
// readme, which is the only thing that needs it.
const (
	// GroupNone is listed in the readme command list with no usage section of
	// its own. It is the zero value, so a command that declares nothing lands
	// here rather than in a section by accident.
	GroupNone = ""

	GroupBasicUsage        = "basic-usage"
	GroupServiceLifecycle  = "service-lifecycle"
	GroupServiceAutomation = "service-automation"
	GroupDataManagement    = "data-management"
	GroupBackups           = "backups"

	// GroupCustomCommands collects the commands a datastore adds for itself that
	// name no section of their own. It is last in the readme, and nothing but a
	// custom command is ever in it.
	GroupCustomCommands = "custom-commands"
)

// Groups are the sections a custom command may declare, which is every section
// but GroupNone and GroupCustomCommands: one means undocumented and the other is
// where a command goes when it names nothing.
var Groups = map[string]bool{
	GroupBasicUsage:        true,
	GroupServiceLifecycle:  true,
	GroupServiceAutomation: true,
	GroupDataManagement:    true,
	GroupBackups:           true,
}

// The protocols a port may declare. A port that names none is tcp, which is
// what docker assumes too.
const (
	ProtocolTCP = "tcp"
	ProtocolUDP = "udp"
)

// ServicesDirectory is the directory under the services root this datastore's
// services live in: what it declares, or its plugin name.
func (d Definition) ServicesDirectory() string {
	if d.Dokku.DataDirectory != "" {
		return d.Dokku.DataDirectory
	}

	return d.Dokku.Plugin
}

// PortFor returns a port by name.
func (d Definition) PortFor(name string) (Port, bool) {
	for _, port := range d.Service.Ports {
		if port.Name == name {
			return port, true
		}
	}

	return Port{}, false
}

// PrimaryPort returns the port the dsn and the readiness probe use when neither
// names one, which is the port marked primary or, failing that, the first.
func (d Definition) PrimaryPort() (Port, bool) {
	for _, port := range d.Service.Ports {
		if port.Primary {
			return port, true
		}
	}

	if len(d.Service.Ports) > 0 {
		return d.Service.Ports[0], true
	}

	return Port{}, false
}

// Implements reports whether the definition supplies what a subcommand needs.
// This is the only statement of which subcommands a datastore has; the bash
// plugins restated it in PLUGIN_UNIMPLEMENTED_SUBCOMMANDS, where it had drifted.
func (d Definition) Implements(subcommand string) bool {
	_, hasExport := d.Dokku.Commands["export"]
	_, hasImport := d.Dokku.Commands["import"]

	switch subcommand {
	case "connect", "export", "import":
		_, ok := d.Dokku.Commands[subcommand]
		return ok
	case "clone":
		return hasExport && hasImport
	case "backup", "backup-auth", "backup-deauth", "backup-schedule",
		"backup-schedule-cat", "backup-set-encryption",
		"backup-set-public-key-encryption", "backup-unschedule",
		"backup-unset-encryption", "backup-unset-public-key-encryption":
		// the backup family is built on export, which is what internal/backup.go
		// actually calls
		return hasExport
	default:
		return true
	}
}

// HostRootTemplate is the prefix every bind mount source starts with. It is
// matched literally rather than rendered, because the path the tool writes to is
// ServiceRoot while the path in the source is HostRoot, and the two differ on a
// docker in docker install.
const HostRootTemplate = "{{ .HostRoot }}"

// ServicePath maps a path inside the container back to a path relative to the
// service root, by walking the bind mounts. It is what lets a config declare
// where the file goes in the container and the tool work out where to write it.
func (d Definition) ServicePath(target string) (string, bool) {
	mount := ""
	resolved := ""
	found := false

	for _, volume := range d.Service.Volumes {
		relative, ok := under(volume.Target, target)
		if !ok {
			continue
		}

		// the longest matching mount wins, so a config under a nested mount is
		// written through that mount rather than through its parent
		if found && len(path.Clean(volume.Target)) <= len(mount) {
			continue
		}

		mount = path.Clean(volume.Target)
		resolved = path.Join(strings.TrimPrefix(volume.Source, HostRootTemplate), relative)
		found = true
	}

	if !found {
		return "", false
	}

	return strings.TrimPrefix(resolved, "/"), true
}

// under reports whether target sits at or below root, and where.
func under(root string, target string) (string, bool) {
	root = path.Clean(root)
	target = path.Clean(target)

	if root == target {
		return "", true
	}

	if !strings.HasPrefix(target, root+"/") {
		return "", false
	}

	return strings.TrimPrefix(target, root+"/"), true
}

// RootfsMode is the mode a payload file is written with. A file below a bin
// directory is executable, because that is what a bin directory means and
// because an embedded file has no mode of its own to carry: it would otherwise
// arrive unable to run, which fails when the verb is used rather than when the
// definition is loaded.
func RootfsMode(name string) fs.FileMode {
	for _, segment := range strings.Split(path.Dir(path.Clean(name)), "/") {
		if segment == "bin" || segment == "sbin" {
			return 0755
		}
	}

	return 0644
}

// BaseCommands are the command names the tool implements itself. A definition
// declares what it can do by supplying these; anything else it wants belongs in
// CustomCommands.
var BaseCommands = map[string]bool{
	"connect": true,
	"export":  true,
	"import":  true,
}

// CommandFor returns a command by name, base or custom.
func (d Definition) CommandFor(name string) (Command, bool) {
	if command, ok := d.Dokku.Commands[name]; ok {
		return command, true
	}

	command, ok := d.Dokku.CustomCommands[name]
	return command, ok
}

// TriggerFor returns the command a definition declares for a dokku trigger.
func (d Definition) TriggerFor(name string) (Command, bool) {
	command, ok := d.Dokku.Triggers[name]
	return command, ok
}

// TriggerNames returns the triggers a definition implements, sorted so that
// what a plugin ships is the same on every generation.
func (d Definition) TriggerNames() []string {
	names := make([]string, 0, len(d.Dokku.Triggers))
	for name := range d.Dokku.Triggers {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

// ImplementsCustom reports whether a datastore adds a command by this name.
func (d Definition) ImplementsCustom(name string) bool {
	_, ok := d.Dokku.CustomCommands[name]
	return ok
}

// WaitPort is the port readiness probes: the one the definition names, or the
// primary when it names none. It is the pair of PortFor and PrimaryPort that
// every caller was writing out by hand.
func (d Definition) WaitPort() (Port, bool) {
	if port, ok := d.PortFor(d.Dokku.Wait); ok {
		return port, true
	}

	return d.PrimaryPort()
}
