// Package image builds the images definitions need. Most definitions do not
// need one: a Dockerfile that only declares its base is a no-op the loader
// detects, and the upstream image is pulled exactly as every datastore is
// today. Only a definition that bakes tooling into the image is built.
package image

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/execx"

	"github.com/dokku/dokku/plugins/common"
)

// executableMode is the mode a file below a bin directory is written with.
// An embedded file loses its executable bit, and a vendored script arriving in
// the image unable to run is a failure at export time rather than at build time.
const executableMode fs.FileMode = 0755

// fileMode is the mode every other file in the build context is written with.
const fileMode fs.FileMode = 0644

// Tag is the image a definition builds to. It is keyed on the definition name
// and the image version rather than on the service, because nothing in the image
// varies per service: building per service, as dokku-service does, would
// multiply build time and disk by the number of services for no gain.
//
// The definition name rather than the plugin name, because postgres-17 and
// postgres-18 share a plugin and must never produce the same tag.
func Tag(name string, version string) string {
	return fmt.Sprintf("dokku/datastore-%s:%s", name, version)
}

// BuildInput is the input for Build.
type BuildInput struct {
	// Definition is the definition to build.
	Definition definition.Definition

	// Image and ImageVersion are the base to build on, which is what lets
	// --image and --image-version keep working for a built definition. Empty
	// falls back to what the Dockerfile pins.
	Image        string
	ImageVersion string
}

// Base is the image reference a build is based on.
func (input BuildInput) Base() string {
	image := input.Image
	if image == "" {
		image = input.Definition.DefaultImage
	}

	version := input.ImageVersion
	if version == "" {
		version = input.Definition.DefaultImageVersion
	}

	return image + ":" + version
}

// Tag is the image this build produces.
func (input BuildInput) Tag() string {
	version := input.ImageVersion
	if version == "" {
		version = input.Definition.DefaultImageVersion
	}

	return Tag(input.Definition.Name, version)
}

// BuildArgs builds the argv for `docker image build` against a prepared context
// directory. It is pure, so what a definition turns into can be pinned by a test
// without a daemon.
func BuildArgs(input BuildInput, directory string) []string {
	return []string{
		"image", "build",
		// the ARG IMAGE / FROM ${IMAGE} idiom, which is what keeps --image and
		// --image-version meaningful for a definition that builds
		"--build-arg", "IMAGE=" + input.Base(),
		"--file", filepath.Join(directory, "Dockerfile"),
		"--tag", input.Tag(),
		directory,
	}
}

// Build builds a definition's image. A definition that does not need one is not
// an error: it reports the tag it would have used so the caller does not have to
// ask twice.
func Build(ctx context.Context, input BuildInput) (string, error) {
	if !input.Definition.Builds {
		return "", nil
	}

	directory, err := os.MkdirTemp("", "dokku-datastore-build-")
	if err != nil {
		return "", fmt.Errorf("unable to create a build context: %w", err)
	}
	defer os.RemoveAll(directory)

	if err := WriteContext(input.Definition, directory); err != nil {
		return "", err
	}

	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    BuildArgs(input, directory),
	}); err != nil {
		return "", fmt.Errorf("unable to build %s: %w", input.Tag(), err)
	}

	return input.Tag(), nil
}

// WriteContext materialises a definition's build context on disk: its Dockerfile
// and the rootfs payload the Dockerfile copies in.
func WriteContext(subject definition.Definition, directory string) error {
	dockerfile := filepath.Join(directory, "Dockerfile")
	if err := os.WriteFile(dockerfile, subject.Dockerfile, fileMode); err != nil {
		return fmt.Errorf("unable to write %s: %w", dockerfile, err)
	}

	// sorted, so that a failure names the same file every run
	names := make([]string, 0, len(subject.Rootfs))
	for name := range subject.Rootfs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		target := filepath.Join(directory, "rootfs", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("unable to create %s: %w", filepath.Dir(target), err)
		}

		if err := os.WriteFile(target, subject.Rootfs[name], ModeFor(name)); err != nil {
			return fmt.Errorf("unable to write %s: %w", target, err)
		}
	}

	return nil
}

// ModeFor is the mode a rootfs file is written with. A file below a bin
// directory is executable, because that is what a bin directory means and
// because an embedded file has no mode of its own to carry.
func ModeFor(name string) fs.FileMode {
	for _, segment := range strings.Split(path.Dir(path.Clean(name)), "/") {
		if segment == "bin" || segment == "sbin" {
			return executableMode
		}
	}

	return fileMode
}

// Exists reports whether an image is already present, so a build that would
// produce what is already there can be skipped.
func Exists(ctx context.Context, tag string) bool {
	_, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"image", "inspect", tag},
	})

	return err == nil
}
