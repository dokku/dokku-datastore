// Package execx runs the commands the binary shells out to. It exists so that a
// non-zero exit is an error rather than something every caller has to remember
// to check, and so that the rest of the binary can run a command without
// depending on the datastore abstraction.
package execx

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/dokku/dokku/plugins/common"
)

// stdoutToStderr sends what a command would stream to stdout to stderr instead.
// It is set when a command was asked for json, whose stdout has to hold the
// document it prints and nothing a trigger or a docker run said on the way.
var stdoutToStderr bool

// StreamStdoutToStderr sends the stdout every command streams to stderr, for the
// rest of the process.
func StreamStdoutToStderr(enabled bool) {
	stdoutToStderr = enabled
}

// Tracing reports whether dokku's trace is on, which is what --trace sets. Only
// "1" counts, since that is the value dokku exports and common checks for.
func Tracing() bool {
	return os.Getenv("DOKKU_TRACE") == "1"
}

// Run calls a command, turning a non-zero exit into an error. dokku's own helper
// reports the exit code and leaves the caller to interpret it, which is a check
// that is easy to forget and silent when it is.
func Run(ctx context.Context, input common.ExecCommandInput) (common.ExecCommandResponse, error) {
	if Tracing() {
		input.PrintCommand = true
	}

	if stdoutToStderr && input.StreamStdout {
		input.StreamStdout = false
		input.StdoutWriter = os.Stderr
	}

	result, err := common.CallExecCommandWithContext(ctx, input)
	if err != nil {
		return result, err
	}

	if result.ExitCode != 0 {
		if input.StreamStderr {
			// stderr already went to the terminal, so repeating it here would
			// print it twice
			return result, errors.New("command exited non-zero")
		}

		return result, fmt.Errorf("command exited non-zero: %s", result.StderrContents())
	}

	return result, nil
}

// PlugnTrigger fires a dokku plugin trigger. Outside a dokku install there are no
// plugins to trigger, so it succeeds without doing anything rather than failing
// and making every caller special-case the test environment.
func PlugnTrigger(ctx context.Context, input common.PlugnTriggerInput) (common.ExecCommandResponse, error) {
	if os.Getenv("PLUGIN_PATH") == "" {
		return common.ExecCommandResponse{ExitCode: 0}, nil
	}

	// a trigger's stdout cannot be pointed anywhere but stdout, so it is held
	// and written to stderr once the trigger is done
	relay := stdoutToStderr && input.StreamStdout
	if relay {
		input.StreamStdout = false
	}

	result, err := common.CallPlugnTriggerWithContext(ctx, input)
	if relay {
		fmt.Fprint(os.Stderr, result.StdoutContents())
	}

	return result, err
}
