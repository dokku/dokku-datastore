package render

import (
	"io/fs"
	"path"
	"path/filepath"
	"sort"

	"github.com/dokku/dokku-datastore/internal/definition"
)

// RootfsDir is where a definition's payload is written inside a service's
// directory before being mounted into the container.
const RootfsDir = "rootfs"

// RootfsFile is one payload file: where it is written, and where it appears
// inside the container.
type RootfsFile struct {
	// Path is the absolute path on the tool side, which is where it is written.
	Path string

	// Mount is the docker volume argument that puts it in the container. It is
	// built from the host root rather than the service root, because that is the
	// path dockerd resolves, and read only so that a container cannot rewrite a
	// script the host also trusts.
	Mount string

	// Target is the path inside the container.
	Target string

	// Contents is the file body.
	Contents []byte

	// Mode is the file mode. A file below a bin directory is executable.
	Mode fs.FileMode
}

// RootfsFiles resolves a definition's payload into files to write and mounts to
// add.
//
// Mounting rather than baking these into an image keeps every definition on the
// pull path: building would turn each trigger-install into a build, which is
// slower and fails differently on a host with a constrained builder. Each file
// is mounted on its own rather than mounting the directory that holds it, since
// mounting a directory over /usr/local/bin would hide the datastore's own
// binaries and leave nothing able to start.
func RootfsFiles(input Input) []RootfsFile {
	files := make([]RootfsFile, 0, len(input.Definition.Rootfs))

	for name := range input.Definition.Rootfs {
		target := "/" + name
		files = append(files, RootfsFile{
			Path:     filepath.Join(input.Scope.ServiceRoot, RootfsDir, filepath.FromSlash(name)),
			Mount:    path.Join(input.Scope.HostRoot, RootfsDir, name) + ":" + target + ":ro",
			Target:   target,
			Contents: input.Definition.Rootfs[name],
			Mode:     definition.RootfsMode(name),
		})
	}

	// sorted, because a map would otherwise emit the mounts in a different
	// order each run and nothing could be pinned
	sort.Slice(files, func(i int, j int) bool {
		return files[i].Target < files[j].Target
	})

	return files
}
