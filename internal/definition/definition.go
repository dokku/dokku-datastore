// Package definition parses and validates a datastore definition: a
// docker-compose.yml carrying an x-dokku extension block, alongside a Dockerfile
// that pins the image. A definition is inert. It says what a service of this type
// looks like, never what any particular service looks like; rendering it against a
// service's state is what produces something runnable.
package definition

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

	// Dockerfile is the definition's Dockerfile. Its FROM line is the default
	// image, the way the bash plugins' config derives the image with awk.
	Dockerfile []byte

	// Scripts are the bin/ hook scripts, keyed by file name.
	Scripts map[string][]byte
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
	// Plugin is the command prefix, the directory under the services root, and
	// the label on every container. Definitions differing only by major version
	// share it.
	Plugin string `yaml:"plugin"`

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

	// Wait names the port to probe with the dokku/wait sidecar, for images with
	// no tool a compose healthcheck could use. Ignored when Healthcheck is set.
	Wait string `yaml:"wait"`

	// Secrets are the credentials generated once at create time and persisted
	// under the service root, keyed by the name templates refer to them by.
	Secrets map[string]Secret `yaml:"secrets"`

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

	// User is the in-container user to run as.
	User string `yaml:"user"`

	// Stdin is true when the command consumes standard input, as import does.
	Stdin bool `yaml:"stdin"`

	// Description and Arguments document an extra subcommand. The built-in verbs
	// take theirs from the tool.
	Description string     `yaml:"description"`
	Arguments   []Argument `yaml:"arguments"`
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
