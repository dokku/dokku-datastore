package internal

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// ErrLinkedService is returned when a service still has linked apps. The
// capitalization is deliberate and matches the bash datastore plugins.
var ErrLinkedService = errors.New("Cannot delete linked service")

// RemoveDataArgsInput is the input for RemoveDataArgs.
type RemoveDataArgsInput struct {
	// Directories are the host paths the definition binds, which are the only
	// places under the service root a container could have written
	Directories []string

	// Image is the image the widening runs in
	Image string
}

// RemoveDataArgs builds the argv for the container that makes a service's data
// removable again.
//
// A datastore writes as whatever user its image runs as, so it leaves a tree
// belonging to somebody the dokku user is not, with directories the dokku user
// cannot unlink inside. The mode is widened from a container that is already
// root, which needs no sudo grant on the host.
//
// Only what the definition binds is touched. The credentials beside it keep
// the modes they were written with, and a definition that binds nothing gets no
// container at all.
func RemoveDataArgs(input RemoveDataArgsInput) []string {
	if len(input.Directories) == 0 {
		return nil
	}

	args := []string{"container", "run", "--rm"}
	targets := make([]string, 0, len(input.Directories))

	for index, directory := range input.Directories {
		target := fmt.Sprintf("/mnt/%d", index)
		args = append(args, "-v", directory+":"+target)
		targets = append(targets, target)
	}

	args = append(args, input.Image, "chmod", "777", "-R")

	return append(args, targets...)
}

// DestroyServiceInput is the input for the DestroyService function
type DestroyServiceInput struct {
	// Datastore is the service to destroy
	Datastore *service.Datastore

	// ServiceName is the name of the service to destroy
	ServiceName string
}

// DestroyService destroys a service
func DestroyService(ctx context.Context, input DestroyServiceInput) error {
	_, err := execx.PlugnTrigger(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"pre-delete", input.Datastore.ServiceType(), input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action pre-delete trigger: %w", err)
	}

	err = service.RemoveBackupSchedule(ctx, service.RemoveBackupScheduleInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to remove backup schedule: %w", err)
	}

	// the argv is built, and the image it names fetched, before the container
	// goes: a destroy that cannot widen the data would otherwise stop with the
	// service already gone and its files still owned by somebody the dokku user
	// is not. A definition that binds nothing needs no container and so needs no
	// image
	serviceFolders := service.Folders(input.Datastore, input.ServiceName)
	arguments := RemoveDataArgs(RemoveDataArgsInput{
		Directories: input.Datastore.BindHostDirectories(input.ServiceName),
		Image:       hostenv.BusyboxImage,
	})

	if len(arguments) > 0 {
		if err := service.EnsureTaggedImage(ctx, service.EnsureTaggedImageInput{
			Action:      "destroy",
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
			TaggedImage: hostenv.BusyboxImage,
		}); err != nil {
			return err
		}
	}

	err = service.RemoveServiceContainer(ctx, service.RemoveServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	if len(arguments) > 0 {
		_, err = execx.Run(ctx, common.ExecCommandInput{
			Command: common.DockerBin(),
			Args:    arguments,
		})
		if err != nil {
			return fmt.Errorf("failed to remove data: %w", err)
		}
	}

	if err := os.RemoveAll(serviceFolders.Root); err != nil {
		return fmt.Errorf("failed to remove service root: %w", err)
	}

	err = common.PropertyDestroy(input.Datastore.Properties().CommandPrefix, input.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to destroy properties: %w", err)
	}

	_, err = execx.PlugnTrigger(ctx, common.PlugnTriggerInput{
		Trigger:      "service-action",
		Args:         []string{"post-delete", input.Datastore.ServiceType(), input.ServiceName},
		StreamStderr: true,
		StreamStdout: true,
	})
	if err != nil {
		return fmt.Errorf("failed to call service-action pre-delete trigger: %w", err)
	}

	return nil
}
