package internal

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/dokku/dokku-datastore/internal/definition"
)

// sysctlReader reads a kernel parameter. It is a variable so that a test can
// decide what the machine says rather than depending on what it happens to say,
// which differs between the machines this is developed on and run on.
var sysctlReader = readSysctl

// CheckRequirements reports whether the machine can run a datastore.
//
// Checked before anything is created, because a host that cannot run a
// datastore should say so once, in terms an operator can act on, rather than
// leaving a container to fail with a message about the thing it could not do.
func CheckRequirements(requirements []definition.Requirement) error {
	for _, requirement := range requirements {
		if requirement.Sysctl == "" {
			continue
		}

		actual, err := sysctlReader(requirement.Sysctl)
		if err != nil {
			// a machine that will not report the value is not a machine that
			// fails the requirement, and refusing to create a service over a
			// reading that could not be taken would be worse than trying
			continue
		}

		if actual >= requirement.Minimum {
			continue
		}

		message := requirement.Message
		if message == "" {
			message = fmt.Sprintf("%s is %d, and %d is needed", requirement.Sysctl, actual, requirement.Minimum)
		}

		return fmt.Errorf("%s", message)
	}

	return nil
}

// readSysctl reads a kernel parameter.
func readSysctl(name string) (int, error) {
	output, err := exec.Command("sysctl", "-n", name).Output()
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(strings.TrimSpace(string(output)))
}
