package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku/plugins/common"
)

// MountsProperty holds the mounts a service's container is made with beyond the
// ones its definition declares, as a json list.
//
// Json rather than the comma joined list the other list properties are kept
// in, because a mount's own options are a comma separated list. Read at the
// moment a container is made, like the log properties, so a change lands on the
// next container rather than on the running one.
const MountsProperty = "mounts"

// The key=value tokens a mount spec's option list recognizes. Each mirrors a
// flag the mount command takes, and both are spelled the way dokku's
// storage:mount spells them, so a spec written for one reads the same in the
// other.
const (
	mountSpecKeySubpath     = "volume-subpath"
	mountSpecKeyVolumeChown = "volume-chown"
)

// mountOptionGroups are the options docker's -v takes besides ro and rw, which
// a mount holds as a field of its own. Docker refuses a container given two
// options from one group, so at most one of each is accepted.
var mountOptionGroups = [][]string{
	{"z", "Z"},
	{"shared", "rshared", "slave", "rslave", "private", "rprivate"},
	{"nocopy"},
	{"consistent", "cached", "delegated"},
}

// volumeNamePattern is a docker volume name as dokku's storage:mount accepts
// one: two characters or more, starting with a letter or a digit
var volumeNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]+$`)

// Mount is a host path or a docker volume mounted into a service's container.
//
// It carries the fields dokku's storage attachments do, less the ones that have
// nothing to say about a service: a service is one process in one container,
// so there is no phase and no process type to scope a mount to.
type Mount struct {
	// Source is an absolute host path or the name of a docker volume
	Source string `json:"source"`

	// ContainerPath is where the source appears inside the container
	ContainerPath string `json:"container_path"`

	// Readonly mounts the source read only
	Readonly bool `json:"readonly,omitempty"`

	// VolumeOptions are the other docker mount options, comma separated
	VolumeOptions string `json:"volume_options,omitempty"`

	// Subpath is recorded and reported but not applied, the way dokku's
	// storage plugin treats it for a docker-local app
	Subpath string `json:"subpath,omitempty"`

	// Chown is recorded and reported but not applied, the way dokku's storage
	// plugin treats it for a docker-local app
	Chown string `json:"volume_chown,omitempty"`
}

// MountFields says which of a mount's settings a spec named, so that one also
// given as a flag can be refused rather than resolved by which came last.
type MountFields struct {
	Readonly      bool
	VolumeOptions bool
	Subpath       bool
	Chown         bool
}

// Volume is the docker -v argument for a mount. The subpath and chown are left
// out, which is what dokku's storage plugin does for a docker-local app.
func (m Mount) Volume() string {
	options := []string{}
	if m.Readonly {
		options = append(options, "ro")
	}
	if m.VolumeOptions != "" {
		options = append(options, m.VolumeOptions)
	}

	volume := m.Source + ":" + m.ContainerPath
	if len(options) > 0 {
		volume += ":" + strings.Join(options, ",")
	}

	return volume
}

// Spec is a mount written out in the grammar a mount spec is read in, so every
// field is shown, including the ones docker is never handed.
func (m Mount) Spec() string {
	options := []string{}
	if m.Readonly {
		options = append(options, "ro")
	}
	if m.VolumeOptions != "" {
		options = append(options, m.VolumeOptions)
	}
	if m.Subpath != "" {
		options = append(options, mountSpecKeySubpath+"="+m.Subpath)
	}
	if m.Chown != "" {
		options = append(options, mountSpecKeyVolumeChown+"="+m.Chown)
	}

	spec := m.Source + ":" + m.ContainerPath
	if len(options) > 0 {
		spec += ":" + strings.Join(options, ",")
	}

	return spec
}

// ParseMountSpec reads a "<source>:<container-dir>[:<options>]" argument.
//
// The option list is the one dokku's storage:mount --replace reads, less the
// phase key: ro or rw, volume-subpath=<path>, volume-chown=<option>, and any
// other bare token as a docker mount option. Order within it is not
// significant, so anything said twice is refused rather than resolved by
// position, and an unrecognized key is refused rather than handed to docker.
//
// The returned mount has not been validated; ValidateMount does that.
func ParseMountSpec(spec string) (Mount, MountFields, error) {
	parts := strings.SplitN(spec, ":", 3)
	mount := Mount{}
	fields := MountFields{}
	if len(parts) >= 1 {
		mount.Source = parts[0]
	}
	if len(parts) >= 2 {
		mount.ContainerPath = parts[1]
	}

	if mount.Source == "" || mount.ContainerPath == "" {
		return Mount{}, MountFields{}, fmt.Errorf("Invalid mount specified: %s", spec) //nolint:staticcheck // matches dokku's storage plugin
	}

	// a trailing colon is the no-options case rather than an empty option
	if len(parts) < 3 || parts[2] == "" {
		return mount, fields, nil
	}

	sawReadonly := false
	sawWritable := false
	remaining := []string{}
	for _, token := range strings.Split(parts[2], ",") {
		if token == "" {
			return Mount{}, MountFields{}, fmt.Errorf("Mount spec %q has an empty mount option", spec) //nolint:staticcheck // matches dokku's storage plugin
		}

		switch token {
		case "ro":
			sawReadonly = true
			continue
		case "rw":
			sawWritable = true
			continue
		}

		// a bare docker option carries no "=", and neither does a token
		// starting with one, which is no key at all. Both are docker options,
		// and ValidateMount says whether docker would take them
		index := strings.Index(token, "=")
		if index <= 0 {
			remaining = append(remaining, token)
			continue
		}

		key, value := token[:index], token[index+1:]
		if key != mountSpecKeySubpath && key != mountSpecKeyVolumeChown {
			return Mount{}, MountFields{}, fmt.Errorf("Mount spec %q specifies unknown key %q; supported keys are %s and %s", spec, key, mountSpecKeyVolumeChown, mountSpecKeySubpath) //nolint:staticcheck // matches dokku's storage plugin
		}

		if value == "" {
			return Mount{}, MountFields{}, fmt.Errorf("Mount spec %q has an empty value for %s", spec, key) //nolint:staticcheck // matches dokku's storage plugin
		}

		switch key {
		case mountSpecKeySubpath:
			if fields.Subpath {
				return Mount{}, MountFields{}, fmt.Errorf("Mount spec %q specifies %s more than once", spec, key) //nolint:staticcheck // matches dokku's storage plugin
			}
			fields.Subpath = true
			mount.Subpath = value
		case mountSpecKeyVolumeChown:
			if fields.Chown {
				return Mount{}, MountFields{}, fmt.Errorf("Mount spec %q specifies %s more than once", spec, key) //nolint:staticcheck // matches dokku's storage plugin
			}
			fields.Chown = true
			mount.Chown = value
		}
	}

	if sawReadonly && sawWritable {
		return Mount{}, MountFields{}, fmt.Errorf("Mount spec %q sets both ro and rw", spec) //nolint:staticcheck // matches dokku's storage plugin
	}

	mount.Readonly = sawReadonly
	fields.Readonly = sawReadonly || sawWritable
	mount.VolumeOptions = strings.Join(remaining, ",")
	fields.VolumeOptions = len(remaining) > 0

	return mount, fields, nil
}

// ValidateMount reports whether a mount is one docker will make a container
// with, judged from the mount alone.
//
// Checked before anything is written rather than left to docker, because docker
// only refuses a malformed mount when the container is made, and an upgrade
// takes the old container away before that.
func ValidateMount(m Mount) error {
	if !strings.HasPrefix(m.Source, "/") && !volumeNamePattern.MatchString(m.Source) {
		return fmt.Errorf("Invalid mount source %q: must be an absolute host path or a docker volume name of two characters or more, without invalid characters", m.Source) //nolint:staticcheck // matches dokku's storage plugin
	}

	if !strings.HasPrefix(m.ContainerPath, "/") {
		return fmt.Errorf("Container path %q must be absolute", m.ContainerPath) //nolint:staticcheck // matches dokku's storage plugin
	}

	// docker refuses to mount over the container's own root
	if path.Clean(m.ContainerPath) == "/" {
		return errors.New("Container path must not be /") //nolint:staticcheck // matches dokku's storage plugin
	}

	if err := validateVolumeOptions(m.VolumeOptions); err != nil {
		return err
	}

	if m.Subpath != "" {
		if strings.HasPrefix(m.Subpath, "/") {
			return fmt.Errorf("Volume subpath %q must be relative", m.Subpath) //nolint:staticcheck // matches dokku's storage plugin
		}

		for _, element := range strings.Split(m.Subpath, "/") {
			if element == ".." {
				return fmt.Errorf("Volume subpath %q must not leave the mount source", m.Subpath) //nolint:staticcheck // matches dokku's storage plugin
			}
		}
	}

	return ValidateChownOption(m.Chown)
}

// validateVolumeOptions reports whether a comma separated option list holds
// only options docker's -v takes, and no two that docker would refuse together.
//
// Stricter than dokku's storage plugin, which stores the list verbatim: an
// option docker does not take - noexec, say - is a service that cannot start,
// found out only once its old container is gone.
func validateVolumeOptions(options string) error {
	if options == "" {
		return nil
	}

	claimed := map[int]string{}
	for _, option := range strings.Split(options, ",") {
		if option == "" {
			return fmt.Errorf("Volume options %q have an empty mount option", options) //nolint:staticcheck // matches dokku's storage plugin
		}

		if option == "ro" || option == "rw" {
			return fmt.Errorf("Volume option %q is not a volume option; use --volume-readonly or the ro and rw tokens instead", option) //nolint:staticcheck // matches dokku's storage plugin
		}

		group := -1
		for index, members := range mountOptionGroups {
			for _, member := range members {
				if option == member {
					group = index
				}
			}
		}

		if group == -1 {
			return fmt.Errorf("Volume option %q is not one docker takes, must be one of [%s]", option, strings.Join(supportedVolumeOptions(), ", ")) //nolint:staticcheck // matches dokku's storage plugin
		}

		if previous, ok := claimed[group]; ok {
			return fmt.Errorf("Volume options %q and %q cannot be used together", previous, option) //nolint:staticcheck // matches dokku's storage plugin
		}
		claimed[group] = option
	}

	return nil
}

// supportedVolumeOptions lists every option validateVolumeOptions accepts
func supportedVolumeOptions() []string {
	options := []string{}
	for _, members := range mountOptionGroups {
		options = append(options, members...)
	}

	return options
}

// ValidateChownOption reports whether a chown option is one dokku's storage
// plugin understands. An empty value means no chown was asked for.
func ValidateChownOption(value string) error {
	switch value {
	case "", "herokuish", "heroku", "packeto", "paketo", "root", "false":
		return nil
	}

	if _, err := strconv.ParseUint(value, 10, 16); err != nil {
		return errors.New("Unsupported chown permissions") //nolint:staticcheck // matches dokku's storage plugin
	}

	return nil
}

// ReservedMountTargets are the container paths a definition already mounts
// something at: its volumes and its payload files. Docker refuses a container
// with two mounts at one path, so a mount cannot take one of these. A mount
// below one of them is fine, and is how a file is added to a directory a
// definition mounts.
func ReservedMountTargets(d definition.Definition) []string {
	targets := make([]string, 0, len(d.Service.Volumes)+len(d.Rootfs))
	for _, volume := range d.Service.Volumes {
		targets = append(targets, path.Clean(volume.Target))
	}

	for name := range d.Rootfs {
		targets = append(targets, path.Clean("/"+name))
	}

	return targets
}

// CheckMounts reports whether a set of mounts can be given to a container made
// from a definition: each one well formed, none at a path the definition
// already mounts, no two at one path, and every host path there to mount.
//
// The host path has to exist because docker would otherwise create it, empty
// and owned by root, and the service would start on that rather than on
// whatever was meant to be mounted.
func CheckMounts(d definition.Definition, mounts []Mount) error {
	reserved := map[string]bool{}
	for _, target := range ReservedMountTargets(d) {
		reserved[target] = true
	}

	seen := map[string]bool{}
	for _, mount := range mounts {
		if err := ValidateMount(mount); err != nil {
			return err
		}

		target := path.Clean(mount.ContainerPath)
		if reserved[target] {
			return fmt.Errorf("Container path %s is already mounted by the %s definition", mount.ContainerPath, d.Dokku.Plugin) //nolint:staticcheck // matches dokku's storage plugin
		}

		if seen[target] {
			return fmt.Errorf("Container path %s is specified more than once", mount.ContainerPath) //nolint:staticcheck // matches dokku's storage plugin
		}
		seen[target] = true

		if err := checkMountSource(mount); err != nil {
			return err
		}
	}

	return nil
}

// checkMountSource reports whether a mount's host path exists.
//
// A docker volume is not checked, since docker creates one that does not exist
// and that is what naming one is for. Nor is a host path on a docker-in-docker
// install, where the path dockerd resolves is not one this process can see.
func checkMountSource(mount Mount) error {
	if !strings.HasPrefix(mount.Source, "/") {
		return nil
	}

	if hostenv.LibHostRoot() != hostenv.LibRoot() {
		return nil
	}

	if _, err := os.Stat(mount.Source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("Host path %s does not exist", mount.Source) //nolint:staticcheck // matches dokku's storage plugin
		}

		return fmt.Errorf("unable to check host path %s: %w", mount.Source, err)
	}

	return nil
}

// ServiceMounts are the mounts a service was given, beyond its definition's
func ServiceMounts(s *Datastore, serviceName string) ([]Mount, error) {
	value := strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, MountsProperty))
	if value == "" {
		return nil, nil
	}

	mounts := []Mount{}
	if err := json.Unmarshal([]byte(value), &mounts); err != nil {
		return nil, fmt.Errorf("unable to read the %s property of %s: %w", MountsProperty, serviceName, err)
	}

	return mounts, nil
}

// WriteMounts records the mounts a service is given, and clears the property
// when there are none rather than leaving an empty list behind
func WriteMounts(s *Datastore, serviceName string, mounts []Mount) error {
	commandPrefix := s.Properties().CommandPrefix
	if len(mounts) == 0 {
		if err := common.PropertyDelete(commandPrefix, serviceName, MountsProperty); err != nil {
			return fmt.Errorf("failed to clear the %s property: %w", MountsProperty, err)
		}

		return nil
	}

	value, err := json.Marshal(mounts)
	if err != nil {
		return fmt.Errorf("unable to encode the %s property: %w", MountsProperty, err)
	}

	if err := common.PropertyWrite(commandPrefix, serviceName, MountsProperty, string(value)); err != nil {
		return fmt.Errorf("failed to write the %s property: %w", MountsProperty, err)
	}

	return nil
}

// MountVolumes are the docker -v arguments for a set of mounts, in order
func MountVolumes(mounts []Mount) []string {
	volumes := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		volumes = append(volumes, mount.Volume())
	}

	return volumes
}

// MountSpecs is a set of mounts written out as specs, space separated, the way
// info reports them
func MountSpecs(mounts []Mount) string {
	specs := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		specs = append(specs, mount.Spec())
	}

	return strings.Join(specs, " ")
}
