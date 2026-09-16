package internal

import (
	"github.com/dokku/dokku-datastore/internal/service"

	"github.com/josegonzalez/cli-skeleton/command"
)

// serviceNameArgument is the name of the argument holding the service name
const serviceNameArgument = "service-name"

// ParseArguments parses command arguments, reporting a missing service name with
// the message the bash datastore plugins emit rather than a generic arity error.
func ParseArguments(args []string, arguments []command.Argument) (map[string]command.Argument, error) {
	parsedArguments, err := command.ParseArguments(args, arguments)
	if err != nil && missingArgument(args, arguments) == serviceNameArgument {
		return parsedArguments, service.ErrMissingServiceName
	}

	return parsedArguments, err
}

// missingArgument returns the name of the first required argument that was not
// specified, or an empty string when every required argument is present
func missingArgument(args []string, arguments []command.Argument) string {
	for i, argument := range arguments {
		if argument.Optional {
			continue
		}

		if i >= len(args) {
			return argument.Name
		}
	}

	return ""
}
