package service

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku/plugins/common"
)

// The properties a service's container logging is configured with.
//
// They are properties rather than files under the service root because they are
// set after the fact as often as at create, and the set command only reaches
// properties. Both are read at the moment a container is made, so a change
// lands on the next container rather than on the running one.
const (
	// LogDriverProperty names the docker logging driver. Empty leaves the
	// container on whatever the daemon defaults to.
	LogDriverProperty = "log-driver"

	// LogOptProperty is a comma separated list of key=value docker log options.
	LogOptProperty = "log-opt"
)

// DefaultMaxSize is what a container's log is capped at when neither the
// service nor dokku says otherwise. It is dokku's own default for app
// containers, so a datastore is held to the same bound as the apps beside it.
const DefaultMaxSize = "10m"

// MaxSizeOption is the log option that bounds a log file, and UnlimitedMaxSize
// is dokku's word for not bounding it. Docker has no such value, so it is how a
// service opts out rather than something docker is ever asked for.
const (
	MaxSizeOption    = "max-size"
	MaxFileOption    = "max-file"
	UnlimitedMaxSize = "unlimited"
)

// maxSizeDrivers are the drivers that take a max-size, which is the same pair
// dokku core allows. Docker rejects an option a driver does not understand, so
// a container on any other driver is left uncapped rather than made unable to
// start.
var maxSizeDrivers = map[string]bool{
	"json-file": true,
	"local":     true,
}

// logDriverPattern is a docker logging driver name. Matched by shape rather
// than against a list of the drivers docker ships, so a plugin driver an
// operator installed is still nameable.
var logDriverPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// logOptionKeyPattern is the shape of a docker log option key. The value half
// is the driver's business and is not inspected, beyond the two options this
// plugin has an opinion about.
var logOptionKeyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]*$`)

// maxSizePattern is a number followed by a unit, which is what docker takes and
// what dokku's own max-size validation accepts.
var maxSizePattern = regexp.MustCompile(`^[0-9]+[kmg]$`)

// LogConfig is the logging a container is made with.
type LogConfig struct {
	// Driver is the docker logging driver, empty for the daemon's default
	Driver string

	// Options are the docker log options, already resolved
	Options map[string]string
}

// Empty reports whether there is nothing to say about a container's logging.
func (c LogConfig) Empty() bool {
	return c.Driver == "" && len(c.Options) == 0
}

// ResolveLogConfigInput is the input for ResolveLogConfig. Every value the
// answer depends on appears here, so that what a service is capped at is a
// function of its input rather than of a docker daemon.
type ResolveLogConfigInput struct {
	// Driver is the log-driver property
	Driver string

	// Options is the log-opt property, as it is stored
	Options string

	// DaemonDriver is the driver docker defaults to, empty when the service
	// names one of its own or when the daemon could not be asked
	DaemonDriver string

	// GlobalMaxSize is dokku's own log retention, empty when there is none
	GlobalMaxSize string
}

// ResolveLogConfig decides the logging a container is made with.
//
// The service's own options win outright: a max-size it names is passed on
// whatever the driver is, because an operator who asked for one has asked for
// it. What is inherited is only the option the service did not name - dokku's
// global retention, or the default behind it - and that is the half held back
// from a driver which has no max-size, since docker refuses an option a driver
// does not understand and a container that cannot be made is worse than a log
// that grows.
//
// Pure, so the whole decision is pinned by a test that needs neither a daemon
// nor a dokku install.
func ResolveLogConfig(input ResolveLogConfigInput) (LogConfig, error) {
	driver := strings.TrimSpace(input.Driver)
	if err := ValidateLogDriver(driver); err != nil {
		return LogConfig{}, err
	}

	options, err := ParseLogOptions(input.Options)
	if err != nil {
		return LogConfig{}, err
	}

	if _, named := options[MaxSizeOption]; !named && maxSizeDrivers[effectiveDriver(driver, input.DaemonDriver)] {
		inherited := strings.TrimSpace(input.GlobalMaxSize)
		if inherited == "" {
			inherited = DefaultMaxSize
		}

		options[MaxSizeOption] = inherited
	}

	if options[MaxSizeOption] == UnlimitedMaxSize {
		delete(options, MaxSizeOption)
	}

	if len(options) == 0 {
		options = nil
	}

	return LogConfig{Driver: driver, Options: options}, nil
}

// effectiveDriver is the driver a container will actually be logged by: the one
// the service names, and otherwise the one the daemon defaults to.
func effectiveDriver(driver string, daemonDriver string) string {
	if driver != "" {
		return driver
	}

	return strings.TrimSpace(daemonDriver)
}

// ValidateLogDriver reports whether a value names a logging driver. An empty
// value is valid and means the daemon's default.
func ValidateLogDriver(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	if !logDriverPattern.MatchString(value) {
		return fmt.Errorf("invalid %s value %q, must be a docker logging driver name", LogDriverProperty, value)
	}

	return nil
}

// ParseLogOptions splits the stored log options into the map docker and compose
// are both handed. An empty value is an empty set rather than an error, which
// is what a service that has never been given any has.
func ParseLogOptions(value string) (map[string]string, error) {
	options := map[string]string{}

	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		name, option, found := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			return nil, fmt.Errorf("invalid %s entry %q, must be key=value", LogOptProperty, entry)
		}

		if !logOptionKeyPattern.MatchString(name) {
			return nil, fmt.Errorf("invalid %s key %q", LogOptProperty, name)
		}

		if err := validateLogOption(name, option); err != nil {
			return nil, err
		}

		options[name] = option
	}

	return options, nil
}

// ValidateLogOptions reports whether a value is a usable list of log options.
func ValidateLogOptions(value string) error {
	_, err := ParseLogOptions(value)
	return err
}

// validateLogOption checks the two options this plugin has an opinion about.
// Everything else is the driver's own vocabulary and is passed through, since a
// list of every option every driver takes would be wrong the moment docker
// added one.
func validateLogOption(name string, value string) error {
	switch name {
	case MaxSizeOption:
		if value == UnlimitedMaxSize || maxSizePattern.MatchString(value) {
			return nil
		}

		return fmt.Errorf("invalid %s value %q, must be a number followed by one of [k, m, g], or %s", MaxSizeOption, value, UnlimitedMaxSize)
	case MaxFileOption:
		count, err := strconv.Atoi(value)
		if err != nil || count < 1 {
			return fmt.Errorf("invalid %s value %q, must be a positive whole number", MaxFileOption, value)
		}

		return nil
	}

	return nil
}

// ServiceLogConfig is the logging a service's container is made with. It is
// ResolveLogConfig with the three things a pure function cannot know read off
// the host: the service's properties, dokku's own retention, and the driver the
// daemon defaults to.
func ServiceLogConfig(ctx context.Context, s *Datastore, serviceName string) (LogConfig, error) {
	commandPrefix := s.Properties().CommandPrefix
	driver := common.PropertyGet(commandPrefix, serviceName, LogDriverProperty)

	input := ResolveLogConfigInput{
		Driver:        driver,
		Options:       common.PropertyGet(commandPrefix, serviceName, LogOptProperty),
		GlobalMaxSize: globalMaxSize(ctx),
	}

	// asked only when the service names no driver, since the daemon's default
	// is what a container lands on only when nothing else says
	if strings.TrimSpace(driver) == "" {
		input.DaemonDriver = daemonLogDriver(ctx)
	}

	config, err := ResolveLogConfig(input)
	if err != nil {
		return LogConfig{}, fmt.Errorf("unable to resolve the log configuration for %s: %w", serviceName, err)
	}

	return config, nil
}

// Both of these are looked up once for the process. A command touching several
// services would otherwise ask the same two questions again for each of them,
// and neither answer changes while it runs.
var (
	daemonLogDriverOnce  sync.Once
	daemonLogDriverValue string

	globalMaxSizeOnce  sync.Once
	globalMaxSizeValue string
)

// daemonLogDriver is the driver docker defaults to. An answer that cannot be
// had is empty, which takes no inherited max-size rather than guessing one onto
// a driver that may not accept it.
func daemonLogDriver(ctx context.Context) string {
	daemonLogDriverOnce.Do(func() {
		result, err := execx.Run(ctx, common.ExecCommandInput{
			Command: common.DockerBin(),
			Args:    []string{"system", "info", "--format", "{{ .LoggingDriver }}"},
		})
		if err != nil {
			return
		}

		daemonLogDriverValue = strings.TrimSpace(result.StdoutContents())
	})

	return daemonLogDriverValue
}

// globalMaxSize is dokku's own log retention, read through the trigger dokku
// publishes for exactly this - a scheduler that builds its own containers and
// has to honour what logs:set was told. Outside a dokku install there is no
// trigger to fire and the answer is empty, which falls to DefaultMaxSize.
func globalMaxSize(ctx context.Context) string {
	globalMaxSizeOnce.Do(func() {
		result, err := execx.PlugnTrigger(ctx, common.PlugnTriggerInput{
			Trigger: "logs-get-property",
			Args:    []string{"--global", MaxSizeOption},
		})
		if err != nil {
			return
		}

		globalMaxSizeValue = strings.TrimSpace(result.StdoutContents())
	})

	return globalMaxSizeValue
}
