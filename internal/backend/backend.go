// Package backend runs containers. Everything here addresses a container by name
// or by id rather than by datastore, which is what lets one implementation serve
// both execution paths: a service pins its container_name, so the docker CLI and
// docker compose produce the same container and every read path below works
// against either.
package backend

import (
	"context"
	"fmt"

	"github.com/dokku/dokku-datastore/internal/execx"

	"github.com/dokku/dokku/plugins/common"
)

// Names are the containers that make up one service. The ambassador is separate
// because it is dokku's, not the datastore's: it publishes ports so that the
// service container never has to.
type Names struct {
	// Container is the service container, dokku.<type>.<service>
	Container string

	// Ambassador is the port publishing sidecar, <container>.ambassador
	Ambassador string
}

// LiveContainerIDInput is the input for LiveContainerID.
type LiveContainerIDInput struct {
	// ContainerName is the name to look for
	ContainerName string

	// Filter narrows the search, e.g. to running containers only
	Filter string
}

// LiveContainerID returns the id of the container with a given name, or empty
// when there is none.
func LiveContainerID(ctx context.Context, input LiveContainerIDInput) string {
	arguments := []string{"container", "ps", "-aq", "--no-trunc", "--filter", fmt.Sprintf("name=^/%s$", input.ContainerName)}
	if input.Filter != "" {
		arguments = append(arguments, "--filter", input.Filter)
	}

	result, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    arguments,
	})
	if err != nil {
		return ""
	}

	id := result.StdoutContents()
	if id == "true" {
		// carried over verbatim from the implementation this replaces, where it
		// was already uncommented. It guards against a response that is not an
		// id at all, and until the case that produced one is understood,
		// removing it would be a guess
		return ""
	}

	return id
}

// Exists reports whether a container exists, running or not.
func Exists(ctx context.Context, containerID string) bool {
	result, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "inspect", containerID},
	})
	if err != nil {
		return false
	}

	return result.ExitCode == 0
}

// IP returns a container's address on the default bridge.
func IP(ctx context.Context, containerID string) string {
	address, _ := common.DockerInspect(containerID, "{{ .NetworkSettings.IPAddress }}")
	return address
}

// Status returns a container's state, or "missing" when there is no such
// container. Callers report it to the user, so the absence of a container is a
// status rather than an error.
func Status(ctx context.Context, containerID string) string {
	status, _ := common.DockerInspect(containerID, "{{ .State.Status }}")
	if status == "" {
		return "missing"
	}

	return status
}

// Image returns the tagged image a container was created from.
func Image(ctx context.Context, containerID string) string {
	image, _ := common.DockerInspect(containerID, "{{ .Config.Image }}")
	return image
}

// Remove deletes a container, running or not.
func Remove(ctx context.Context, containerID string) error {
	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "rm", "-f", containerID},
	}); err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	return nil
}

// Stop stops a container by name or id.
func Stop(ctx context.Context, container string) error {
	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "stop", container},
	}); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}

	return nil
}

// Start starts a container that already exists. It is not create: a stopped
// service is brought back by starting the container it already has, which is
// what keeps its id, its mounts and its network attachments.
func Start(ctx context.Context, container string) error {
	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "start", container},
	}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	return nil
}

// Unpause thaws a container docker was told to freeze.
//
// This is docker's own unpause and not the reverse of this package's Pause,
// which stops a service rather than freezing one. Nothing here ever freezes a
// container; this exists because an operator can, by hand, and docker refuses to
// start a container in that state.
func Unpause(ctx context.Context, container string) error {
	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "unpause", container},
	}); err != nil {
		return fmt.Errorf("failed to unpause container: %w", err)
	}

	return nil
}

// PauseInput is the input for Pause.
type PauseInput struct {
	// Names are the service's containers
	Names Names

	// ContainerID is the service container, looked up when empty
	ContainerID string
}

// Pause stops a service without removing it. The ambassador goes down first, so
// that a client reaching the published port gets a refused connection rather than
// a connection to a datastore that is on its way out.
func Pause(ctx context.Context, input PauseInput) error {
	if Exists(ctx, input.Names.Ambassador) {
		if err := Stop(ctx, input.Names.Ambassador); err != nil {
			return fmt.Errorf("failed to stop ambassador container: %w", err)
		}
	}

	containerID := input.ContainerID
	if containerID == "" {
		containerID = LiveContainerID(ctx, LiveContainerIDInput{ContainerName: input.Names.Container})
	}

	return Stop(ctx, containerID)
}

// Resume starts a paused service. The order is the reverse of Pause: the
// datastore comes up before the ambassador starts forwarding to it, so a client
// reaching the published port never lands on a datastore that is not there yet.
func Resume(ctx context.Context, names Names) error {
	if err := Start(ctx, names.Container); err != nil {
		return err
	}

	if !Exists(ctx, names.Ambassador) {
		return nil
	}

	if err := Start(ctx, names.Ambassador); err != nil {
		return fmt.Errorf("failed to start ambassador container: %w", err)
	}

	return nil
}

// Down stops and removes a service's containers, leaving its data on the host.
// Nothing happens when the service container is already gone.
func Down(ctx context.Context, names Names) error {
	containerID := LiveContainerID(ctx, LiveContainerIDInput{ContainerName: names.Container})
	if containerID == "" {
		return nil
	}

	if err := Pause(ctx, PauseInput{Names: names, ContainerID: containerID}); err != nil {
		return err
	}

	if Exists(ctx, names.Ambassador) {
		if err := Remove(ctx, names.Ambassador); err != nil {
			return err
		}
	}

	// the restart policy is cleared before the removal so that docker cannot
	// bring the container back between the two
	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "update", "--restart=no", containerID},
	}); err != nil {
		return fmt.Errorf("failed to update container restart policy: %w", err)
	}

	return Remove(ctx, containerID)
}
