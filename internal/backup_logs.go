package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// BackupLogsInput is the input for the BackupLogs function
type BackupLogsInput struct {
	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

	// ServiceName is the name of the service to show the backup log of
	ServiceName string

	// Num is the number of lines to display
	Num int

	// Tail is whether to follow the log
	Tail bool
}

// backupLogsArgs builds the arguments tail is run with. A followed log is
// followed by name rather than by descriptor, so that it survives logrotate
// truncating or replacing the file, and a log not yet written is waited for.
func backupLogsArgs(logFile string, num int, tail bool) []string {
	args := []string{"-n", strconv.Itoa(num)}
	if tail {
		args = append(args, "-F")
	}

	return append(args, logFile)
}

// BackupLogs prints the log a service's scheduled backups append their output
// to
func BackupLogs(ctx context.Context, input BackupLogsInput) error {
	logFile := BackupLogFile(input.Datastore.Properties().CommandPrefix, input.ServiceName)
	if !input.Tail && !common.FileExists(logFile) {
		return fmt.Errorf("no scheduled backup of %s has been logged yet, expected %s", input.ServiceName, logFile)
	}

	_, err := execx.Run(ctx, common.ExecCommandInput{
		Command:      "tail",
		Args:         backupLogsArgs(logFile, input.Num, input.Tail),
		StdoutWriter: os.Stdout,
		StderrWriter: os.Stderr,
	})

	if err != nil {
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
