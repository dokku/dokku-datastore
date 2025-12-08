package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/dokku/dokku-datastore/internal/datastores"
	"github.com/dokku/dokku/plugins/common"
)

// LogsInput is the input for the Logs function
type LogsInput struct {
	// Datastore is the datastore to get the logs for
	Datastore datastores.Datastore

	// ServiceName is the name of the service to get the logs for
	ServiceName string

	// Num is the number of lines to display
	Num int

	// Tail is whether to tail the logs
	Tail bool
}

// Logs gets the logs for a service
func Logs(ctx context.Context, input LogsInput) error {
	containerID := datastores.LiveContainerID(ctx, datastores.LiveContainerIDInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if containerID == "" {
		return fmt.Errorf("container %s does not exist", input.ServiceName)
	}

	args := []string{"container", "logs", containerID}
	if input.Num > 0 {
		args = append(args, "--tail", strconv.Itoa(input.Num))
	}
	if input.Tail {
		args = append(args, "--follow")
	}

	_, err := datastores.CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command:      common.DockerBin(),
		Args:         args,
		StdoutWriter: os.Stdout,
		StderrWriter: os.Stderr,
	})

	if err != nil {
		// check if the context was cancelled
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return ctx.Err()
		}
		return err
	}

	return nil
}
