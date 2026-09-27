package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dokku/dokku-datastore/internal/backend"
	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"

	"github.com/dokku/dokku/plugins/common"
)

// VolumeSubpathAPIVersion is the first docker api to take volume-subpath in a
// --mount, which Docker Engine 26.0 introduced.
const VolumeSubpathAPIVersion = "1.45"

// dockerAPIVersion asks the daemon which api it speaks. A variable so that a
// test can stand in for a daemon.
var dockerAPIVersion = func(ctx context.Context) (string, error) {
	result, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"version", "--format", "{{ .Server.APIVersion }}"},
	})
	if err != nil {
		return "", fmt.Errorf("unable to ask docker for its api version: %w", err)
	}

	return strings.TrimSpace(result.StdoutContents()), nil
}

// ResolveChownID is the uid a chown option names, the way dokku's storage plugin
// resolves it. The second value is false for "false" and for no option at all,
// which both mean nothing is chowned.
//
// Unlike dokku's, no user namespace offset is added: the chown runs inside a
// container, which sees the same ids the service container does.
func ResolveChownID(value string) (int, bool, error) {
	switch value {
	case "", "false":
		return 0, false, nil
	case "herokuish":
		return 32767, true, nil
	case "heroku":
		return 1000, true, nil
	case "packeto", "paketo":
		return 2000, true, nil
	case "root":
		return 0, true, nil
	}

	id, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0, false, errors.New("Unsupported chown permissions") //nolint:staticcheck // matches dokku's storage plugin
	}

	return int(id), true, nil
}

// CheckMountsOnHost reports whether a set of mounts can be applied on this host
// for the service whose directory is hostRoot, as dockerd sees it.
//
// A chown is only taken for a host path inside the service's own directory:
// anything else belongs to somebody other than the service, which is the line
// dokku's storage plugin draws for the directories it will chown. And a docker
// volume mounted from a subpath needs a daemon new enough to take one, which is
// asked here rather than found out once the old container is gone.
func CheckMountsOnHost(ctx context.Context, hostRoot string, mounts []Mount) error {
	needsSubpath := false
	for _, mount := range mounts {
		if err := checkMountChown(hostRoot, mount); err != nil {
			return err
		}

		if mount.NeedsMountFlag() {
			needsSubpath = true
		}
	}

	if !needsSubpath {
		return nil
	}

	version, err := dockerAPIVersion(ctx)
	if err != nil {
		return err
	}

	if !apiVersionAtLeast(version, VolumeSubpathAPIVersion) {
		return fmt.Errorf("a docker volume mounted from a subpath needs docker api %s or newer (Docker Engine 26.0), and this daemon speaks %s", VolumeSubpathAPIVersion, version)
	}

	return nil
}

// checkMountChown reports whether a mount's chown can be applied.
func checkMountChown(hostRoot string, mount Mount) error {
	_, chown, err := ResolveChownID(mount.Chown)
	if err != nil {
		return err
	}

	if !chown {
		return nil
	}

	if !mount.IsHostPath() {
		return fmt.Errorf("Chown is not supported on docker volume %s, only on a host path inside %s", mount.Source, hostRoot) //nolint:staticcheck // matches dokku's storage plugin
	}

	relative, err := filepath.Rel(filepath.Clean(hostRoot), filepath.Clean(mount.HostSource()))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, "../") {
		return fmt.Errorf("Chown is only supported on a host path inside %s, not on %s", hostRoot, mount.HostSource()) //nolint:staticcheck // matches dokku's storage plugin
	}

	return nil
}

// apiVersionAtLeast compares two docker api versions, major.minor.
func apiVersionAtLeast(version string, minimum string) bool {
	parse := func(value string) (int, int, bool) {
		major, minor, found := strings.Cut(strings.TrimSpace(value), ".")
		if !found {
			return 0, 0, false
		}

		majorNumber, err := strconv.Atoi(major)
		if err != nil {
			return 0, 0, false
		}

		minorNumber, err := strconv.Atoi(minor)
		if err != nil {
			return 0, 0, false
		}

		return majorNumber, minorNumber, true
	}

	major, minor, ok := parse(version)
	if !ok {
		return false
	}

	wantMajor, wantMinor, _ := parse(minimum)
	if major != wantMajor {
		return major > wantMajor
	}

	return minor >= wantMinor
}

// ChownMounts hands each mount that asks for it to the uid its chown names,
// before the container that mounts it is made.
//
// It runs in a throwaway container as root rather than on the host, the way the
// definitions that need a directory owned by the datastore's user do it: the
// dokku user needs no grant, and busybox is there whatever image the datastore
// runs, some of which ship no chown at all.
func ChownMounts(ctx context.Context, datastore *Datastore, serviceName string, mounts []Mount) error {
	for _, mount := range mounts {
		uid, chown, err := ResolveChownID(mount.Chown)
		if err != nil {
			return err
		}

		if !chown {
			continue
		}

		if err := EnsureTaggedImage(ctx, EnsureTaggedImageInput{
			Action:      "chown",
			Datastore:   datastore,
			ServiceName: serviceName,
			TaggedImage: hostenv.BusyboxImage,
		}); err != nil {
			return err
		}

		owner := fmt.Sprintf("%d:%d", uid, uid)
		if err := backend.Run(ctx, backend.RunInput{
			Image:   hostenv.BusyboxImage,
			Argv:    []string{"chown", "-R", owner, "/target"},
			Volumes: []string{mount.HostSource() + ":/target"},
			User:    "0",
		}); err != nil {
			return fmt.Errorf("unable to chown %s to %s: %w", mount.HostSource(), owner, err)
		}
	}

	return nil
}
