package internal

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// maxAncestorDepth is how far up the process tree to look before giving up
const maxAncestorDepth = 4

// AncestorInvokedWith reports whether the process, or one of the processes that
// led to it, was invoked with a given argument. Dokku asks every plugin for its
// command list the same way whether or not the user typed `dokku help --all`,
// so looking at who called is the only way for a plugin to tell the two apart.
// The bash datastore plugins did this with ps against their parent process.
func AncestorInvokedWith(argument string) bool {
	pid := os.Getpid()
	for depth := 0; depth < maxAncestorDepth && pid > 1; depth++ {
		arguments, err := processArguments(pid)
		if err != nil {
			return false
		}

		for _, candidate := range arguments {
			if candidate == argument {
				return true
			}
		}

		pid, err = parentProcess(pid)
		if err != nil {
			return false
		}
	}

	return false
}

// processArguments returns the arguments a process was invoked with
func processArguments(pid int) ([]string, error) {
	contents, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, err
	}

	return strings.Split(strings.TrimRight(string(contents), "\x00"), "\x00"), nil
}

// parentProcess returns the process that started a process
func parentProcess(pid int) (int, error) {
	contents, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}

	// the second field is the executable name in parentheses, and it may itself
	// contain spaces and parentheses, so the fields are read from after it
	_, rest, found := strings.Cut(string(contents), ")")
	if !found {
		return 0, fmt.Errorf("unable to read the parent of process %d", pid)
	}

	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return 0, fmt.Errorf("unable to read the parent of process %d", pid)
	}

	return strconv.Atoi(fields[1])
}
