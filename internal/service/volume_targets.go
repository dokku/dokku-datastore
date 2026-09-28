package service

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku/plugins/common"
)

// VolumeTargetsProperty holds the container paths a service's volumes are
// mounted at in place of the ones its definition declares, as space separated
// key=path pairs keyed by each volume's source relative to the service root.
// Empty mounts every volume where the definition says.
//
// Read at the moment a container is made, like the mounts, so a change lands on
// the next container rather than on the running one.
const VolumeTargetsProperty = "volume-targets"

// ParseVolumeTargets reads a volume-targets value: key=path pairs separated by
// whitespace, each path absolute. It checks the value alone, so it says nothing
// of whether the keys are volumes a definition has; CheckVolumeTargets does. An
// empty value is valid and moves nothing.
func ParseVolumeTargets(value string) (map[string]string, error) {
	targets := map[string]string{}
	for _, pair := range strings.Fields(value) {
		key, target, found := strings.Cut(pair, "=")
		if !found || key == "" || target == "" {
			return nil, fmt.Errorf("invalid %s value %q, each entry must be written as <volume>=<container-path>", VolumeTargetsProperty, pair)
		}

		if _, ok := targets[key]; ok {
			return nil, fmt.Errorf("invalid %s value, the volume %s is moved more than once", VolumeTargetsProperty, key)
		}

		if !strings.HasPrefix(target, "/") {
			return nil, fmt.Errorf("invalid %s value, container path %q for the volume %s must be absolute", VolumeTargetsProperty, target, key)
		}

		// docker's -v and compose's short volume syntax both split on these, so
		// a path holding one would be read as something else
		if strings.ContainsAny(target, ":,") {
			return nil, fmt.Errorf("invalid %s value, container path %q for the volume %s must not contain a colon or a comma", VolumeTargetsProperty, target, key)
		}

		target = path.Clean(target)
		if target == "/" {
			return nil, fmt.Errorf("invalid %s value, the volume %s must not be mounted at /", VolumeTargetsProperty, key)
		}

		targets[key] = target
	}

	return targets, nil
}

// FormatVolumeTargets writes a set of targets the way the property holds them
// and info reports them: sorted by key, so the same targets always read the
// same.
func FormatVolumeTargets(targets map[string]string) string {
	keys := make([]string, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+targets[key])
	}

	return strings.Join(pairs, " ")
}

// CheckVolumeTargets reports whether a service running a definition can have its
// volumes moved this way: each key a volume the definition declares, no two
// volumes landing at one path, and none at or above a path the payload is
// mounted at.
//
// A volume inside another is allowed, as it is for the definition's own, since
// docker mounts the deeper one over the shallower.
func CheckVolumeTargets(d definition.Definition, overrides map[string]string) error {
	if len(overrides) == 0 {
		return nil
	}

	keys := d.VolumeKeys()
	declared := map[string]bool{}
	for _, key := range keys {
		declared[key] = true
	}

	moved := make([]string, 0, len(overrides))
	for key := range overrides {
		moved = append(moved, key)
	}
	sort.Strings(moved)

	for _, key := range moved {
		if declared[key] {
			continue
		}

		if len(keys) == 0 {
			return fmt.Errorf("the %s definition mounts no volumes, so %s cannot be moved", d.Name, key)
		}

		return fmt.Errorf("the %s definition has no volume %s, must be one of [%s]", d.Name, key, strings.Join(keys, ", "))
	}

	targets := d.VolumeTargets(overrides)
	claimed := map[string]string{}
	for _, key := range keys {
		target := path.Clean(targets[key])
		if other, ok := claimed[target]; ok {
			return fmt.Errorf("the volumes %s and %s would both be mounted at %s", other, key, target)
		}
		claimed[target] = key
	}

	// a payload file below a volume would have its mount point made inside
	// that volume on the host, and one at a volume's path would clash with it
	payload := make([]string, 0, len(d.Rootfs))
	for name := range d.Rootfs {
		payload = append(payload, path.Clean("/"+name))
	}
	sort.Strings(payload)

	for _, key := range moved {
		target := path.Clean(targets[key])
		for _, file := range payload {
			if file == target || strings.HasPrefix(file, target+"/") {
				return fmt.Errorf("the volume %s cannot be mounted at %s, which holds the %s the %s definition mounts", key, target, file, d.Name)
			}
		}
	}

	return nil
}

// ServiceVolumeTargets are the volume targets a service was given, as it was
// given them, nil when it was given none.
func ServiceVolumeTargets(s *Datastore, serviceName string) (map[string]string, error) {
	value := strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, VolumeTargetsProperty))
	if value == "" {
		return nil, nil
	}

	targets, err := ParseVolumeTargets(value)
	if err != nil {
		return nil, fmt.Errorf("unable to read the %s property of %s: %w", VolumeTargetsProperty, serviceName, err)
	}

	return targets, nil
}

// scopeVolumeTargets are a service's volume targets as a template is rendered
// against them, never failing.
//
// A scope is assembled to render a connection string as much as to make a
// container, and the first must not need a dokku to read properties from: with
// none configured, or a value that cannot be read, the definition's own targets
// are used. Making a container reads the property strictly, so a value that
// cannot be read never reaches one.
func scopeVolumeTargets(s *Datastore, serviceName string) map[string]string {
	if os.Getenv("DOKKU_LIB_ROOT") == "" {
		return nil
	}

	targets, err := ServiceVolumeTargets(s, serviceName)
	if err != nil {
		return nil
	}

	return targets
}

// WriteVolumeTargets records the volume targets a service is given, and clears
// the property when there are none rather than leaving an empty value behind
func WriteVolumeTargets(s *Datastore, serviceName string, targets map[string]string) error {
	commandPrefix := s.Properties().CommandPrefix
	if len(targets) == 0 {
		if err := common.PropertyDelete(commandPrefix, serviceName, VolumeTargetsProperty); err != nil {
			return fmt.Errorf("failed to clear the %s property: %w", VolumeTargetsProperty, err)
		}

		return nil
	}

	if err := common.PropertyWrite(commandPrefix, serviceName, VolumeTargetsProperty, FormatVolumeTargets(targets)); err != nil {
		return fmt.Errorf("failed to write the %s property: %w", VolumeTargetsProperty, err)
	}

	return nil
}
