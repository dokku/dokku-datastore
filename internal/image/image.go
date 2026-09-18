// Package image builds the image a definition needs when it needs one.
//
// No definition shipped today does. A definition that vendors a script mounts
// it into the container instead, which keeps every datastore on the pull path:
// building would turn each trigger-install into a build, slower and failing
// differently on a host with a constrained builder.
//
// This is deliberately kept for the payload a mount cannot deliver: a compiled
// tool, a package the base image lacks, or anything needing an interpreter that
// is not already there. A Dockerfile that only declares its base is detected as
// a no-op and pulled, so adding that payload is a Dockerfile change rather than
// a Go one.
package image

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/execx"

	"github.com/dokku/dokku/plugins/common"
)

// fileMode is the mode the Dockerfile is written with in the build context.
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
	if err := os.WriteFile(dockerfile, buildDockerfile(subject), fileMode); err != nil {
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

// buildDockerfile is the definition's Dockerfile as the build context needs it,
// with the base declared as a build argument.
//
// A definition pins its image on a plain FROM line, because that is the only form
// dependabot reads and one line is the only way two of them cannot disagree. A
// build needs the opposite: the base has to be replaceable, so that a service
// created at a version other than the default is built on the version it asked
// for rather than on the pinned one.
//
// So the argument is introduced here, in a directory that exists for the length
// of one build, rather than committed. BuildArgs passes the matching
// --build-arg IMAGE=, and a Dockerfile without the ARG would silently ignore it.
func buildDockerfile(subject definition.Definition) []byte {
	image, version, _, err := definition.ImageFromDockerfile(subject.Dockerfile)
	if err != nil {
		// a definition that does not parse never reaches a build: the registry
		// refuses to load it. Leaving it alone keeps this from inventing a
		// Dockerfile for something it could not read.
		return subject.Dockerfile
	}

	rewritten := []string{fmt.Sprintf("ARG IMAGE=%s:%s", image, version)}
	for _, line := range strings.Split(string(subject.Dockerfile), "\n") {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), "FROM ") {
			rewritten = append(rewritten, "FROM ${IMAGE}")
			continue
		}

		rewritten = append(rewritten, line)
	}

	return []byte(strings.Join(rewritten, "\n"))
}

// ModeFor is the mode a rootfs file is written with. The rule lives on the
// definition, because the mount path needs exactly the same answer.
func ModeFor(name string) fs.FileMode {
	return definition.RootfsMode(name)
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
