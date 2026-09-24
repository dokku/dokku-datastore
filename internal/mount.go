package internal

import (
	"errors"
	"fmt"
	"path"

	"github.com/dokku/dokku-datastore/internal/service"
)

// MountServiceInput is the input for the MountService function
type MountServiceInput struct {
	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

	// ServiceName is the name of the service to mount into
	ServiceName string

	// Specs are the "<source>:<container-dir>[:<options>]" arguments. Exactly
	// one without Replace, and the whole set with it.
	Specs []string

	// Replace swaps the service's entire set of mounts for Specs
	Replace bool

	// Readonly mounts the one spec read only
	Readonly bool

	// VolumeOptions are docker mount options for the one spec
	VolumeOptions string

	// Subpath is recorded against the one spec
	Subpath string

	// Chown is recorded against the one spec
	Chown string
}

// MountService adds a mount to a service, updates one it already has, or
// replaces all of them, and says which it did.
//
// Every mount is checked before anything is written, so a refused one leaves
// the service's mounts as they were. What is written reaches the container the
// next time one is made.
func MountService(input MountServiceInput) (string, error) {
	current, err := service.ServiceMounts(input.Datastore, input.ServiceName)
	if err != nil {
		return "", err
	}

	mounts, message, err := mountedSet(current, input)
	if err != nil {
		return "", err
	}

	if err := service.CheckMounts(input.Datastore.Definition, mounts); err != nil {
		return "", err
	}

	if err := service.WriteMounts(input.Datastore, input.ServiceName, mounts); err != nil {
		return "", err
	}

	return message, nil
}

// mountedSet is the set of mounts a mount command leaves a service with.
//
// Pure, so what each form of the command does to a set is pinned by a test
// rather than by a property file. Whether the result can be given to a
// container is CheckMounts' to say.
func mountedSet(current []service.Mount, input MountServiceInput) ([]service.Mount, string, error) {
	commandPrefix := input.Datastore.Properties().CommandPrefix

	if input.Replace {
		// each setting has exactly one spelling in this form, the token in the
		// spec that declares it, rather than a call-level default a token
		// overrides
		for _, conflict := range []struct {
			flag string
			set  bool
			hint string
		}{
			{"--volume-subpath", input.Subpath != "", "volume-subpath=<path>"},
			{"--volume-readonly", input.Readonly, "ro"},
			{"--volume-chown", input.Chown != "", "volume-chown=<option>"},
			{"--volume-options", input.VolumeOptions != "", "the option as a bare token"},
		} {
			if conflict.set {
				return nil, "", fmt.Errorf("The %s flag cannot be used with --replace; set %s in the mount spec instead", conflict.flag, conflict.hint) //nolint:staticcheck // matches dokku's storage plugin
			}
		}

		// an empty set is refused rather than taken as a request to remove
		// every mount, so that a generated list which expands to nothing cannot
		// quietly unmount everything
		if len(input.Specs) == 0 {
			return nil, "", fmt.Errorf("Must specify at least one mount, use %s:unmount --all to remove all mounts", commandPrefix) //nolint:staticcheck // matches dokku's storage plugin
		}

		mounts := make([]service.Mount, 0, len(input.Specs))
		for _, spec := range input.Specs {
			mount, _, err := service.ParseMountSpec(spec)
			if err != nil {
				return nil, "", err
			}

			mounts = append(mounts, mount)
		}

		return mounts, fmt.Sprintf("Mounts replaced on %s", input.ServiceName), nil
	}

	if len(input.Specs) == 0 {
		return nil, "", errors.New("Must specify a mount") //nolint:staticcheck // matches dokku's storage plugin
	}

	if len(input.Specs) > 1 {
		return nil, "", errors.New("Only one mount can be specified without --replace") //nolint:staticcheck // matches dokku's storage plugin
	}

	mount, err := mountWithFlags(input)
	if err != nil {
		return nil, "", err
	}

	mounts := append([]service.Mount{}, current...)
	target := path.Clean(mount.ContainerPath)
	for index, existing := range mounts {
		if path.Clean(existing.ContainerPath) != target {
			continue
		}

		// the same mount again rewrites its settings rather than being
		// refused, so a setting can be changed without unmounting first.
		// Rewritten rather than merged: a flag left off clears what it set
		if existing.Source != mount.Source {
			return nil, "", fmt.Errorf("Container path %s is already mounted from %s", mount.ContainerPath, existing.Source) //nolint:staticcheck // matches dokku's storage plugin
		}

		mounts[index] = mount
		return mounts, fmt.Sprintf("Mount of %s at %s on %s updated", mount.Source, mount.ContainerPath, input.ServiceName), nil
	}

	mounts = append(mounts, mount)
	return mounts, fmt.Sprintf("%s mounted at %s on %s", mount.Source, mount.ContainerPath, input.ServiceName), nil
}

// mountWithFlags reads the one spec of the single form and applies the flags to
// it. A setting the spec already names is refused as a flag too, rather than
// one quietly winning over the other.
func mountWithFlags(input MountServiceInput) (service.Mount, error) {
	mount, fields, err := service.ParseMountSpec(input.Specs[0])
	if err != nil {
		return service.Mount{}, err
	}

	if input.Readonly {
		if fields.Readonly {
			return service.Mount{}, errors.New("The --volume-readonly flag cannot be used with a ro or rw option in the mount spec") //nolint:staticcheck // matches dokku's storage plugin
		}
		mount.Readonly = true
	}

	if input.VolumeOptions != "" {
		if fields.VolumeOptions {
			return service.Mount{}, errors.New("The --volume-options flag cannot be used with mount options in the mount spec") //nolint:staticcheck // matches dokku's storage plugin
		}
		mount.VolumeOptions = input.VolumeOptions
	}

	if input.Subpath != "" {
		if fields.Subpath {
			return service.Mount{}, errors.New("The --volume-subpath flag cannot be used with volume-subpath in the mount spec") //nolint:staticcheck // matches dokku's storage plugin
		}
		mount.Subpath = input.Subpath
	}

	if input.Chown != "" {
		if fields.Chown {
			return service.Mount{}, errors.New("The --volume-chown flag cannot be used with volume-chown in the mount spec") //nolint:staticcheck // matches dokku's storage plugin
		}
		mount.Chown = input.Chown
	}

	return mount, nil
}

// ParseMountSpecs reads the specs a --volume flag was given, each one with its
// settings named in its own option list since that flag has no others. An
// empty spec is skipped rather than refused, so --volume "" asks for none.
func ParseMountSpecs(specs []string) ([]service.Mount, error) {
	mounts := make([]service.Mount, 0, len(specs))
	for _, spec := range specs {
		if spec == "" {
			continue
		}

		mount, _, err := service.ParseMountSpec(spec)
		if err != nil {
			return nil, err
		}

		mounts = append(mounts, mount)
	}

	return mounts, nil
}

// UnmountServiceInput is the input for the UnmountService function
type UnmountServiceInput struct {
	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

	// ServiceName is the name of the service to unmount from
	ServiceName string

	// Specs name the mounts to remove as "<source>:<container-dir>", with any
	// options ignored
	Specs []string

	// All removes every mount
	All bool
}

// UnmountService removes mounts from a service, and says what it did.
//
// Nothing is checked against the host, so a mount whose host path has gone can
// always be removed. What is written reaches the container the next time one is
// made.
func UnmountService(input UnmountServiceInput) (string, error) {
	current, err := service.ServiceMounts(input.Datastore, input.ServiceName)
	if err != nil {
		return "", err
	}

	mounts, message, err := unmountedSet(current, input)
	if err != nil {
		return "", err
	}

	if err := service.WriteMounts(input.Datastore, input.ServiceName, mounts); err != nil {
		return "", err
	}

	return message, nil
}

// unmountedSet is the set of mounts an unmount command leaves a service with.
//
// Every argument is matched before anything is removed, so one naming a mount
// the service does not have leaves the rest in place rather than removing a
// prefix of them.
func unmountedSet(current []service.Mount, input UnmountServiceInput) ([]service.Mount, string, error) {
	commandPrefix := input.Datastore.Properties().CommandPrefix

	if input.All {
		if len(input.Specs) > 0 {
			return nil, "", errors.New("A mount cannot be specified with --all") //nolint:staticcheck // matches dokku's storage plugin
		}

		// nothing to remove is not an error, so the command can be run again
		return nil, fmt.Sprintf("Removed all mounts on %s", input.ServiceName), nil
	}

	if len(input.Specs) == 0 {
		return nil, "", fmt.Errorf("Must specify at least one mount, use %s:unmount --all to remove all mounts", commandPrefix) //nolint:staticcheck // matches dokku's storage plugin
	}

	mounts := append([]service.Mount{}, current...)
	for _, spec := range input.Specs {
		wanted, _, err := service.ParseMountSpec(spec)
		if err != nil {
			return nil, "", err
		}

		index := -1
		for candidate, existing := range mounts {
			if existing.Source == wanted.Source && path.Clean(existing.ContainerPath) == path.Clean(wanted.ContainerPath) {
				index = candidate
				break
			}
		}

		if index == -1 {
			return nil, "", errors.New("Mount path does not exist.") //nolint:staticcheck // matches dokku's storage plugin
		}

		mounts = append(mounts[:index], mounts[index+1:]...)
	}

	return mounts, fmt.Sprintf("Removed %d mount(s) from %s", len(current)-len(mounts), input.ServiceName), nil
}
