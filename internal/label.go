package internal

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
)

// LabelServiceInput is the input for the LabelService function
type LabelServiceInput struct {
	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

	// ServiceName is the name of the service to label
	ServiceName string

	// Specs are the "<key>=<value>" arguments
	Specs []string
}

// LabelService adds labels to a service, replacing the value of any it already
// has, and says what it did.
//
// Every label is checked before anything is written, so a refused one leaves
// the service's labels as they were. What is written reaches the container the
// next time one is made.
func LabelService(input LabelServiceInput) (string, error) {
	current, err := service.ServiceLabels(input.Datastore, input.ServiceName)
	if err != nil {
		return "", err
	}

	labels, message, err := labeledSet(current, input)
	if err != nil {
		return "", err
	}

	if err := service.WriteLabels(input.Datastore, input.ServiceName, labels); err != nil {
		return "", err
	}

	return message, nil
}

// labeledSet is the set of labels a label command leaves a service with.
//
// Pure, so what the command does to a set is pinned by a test rather than by a
// property file.
func labeledSet(current map[string]string, input LabelServiceInput) (map[string]string, string, error) {
	given, err := service.ParseLabels(input.Specs)
	if err != nil {
		return nil, "", err
	}

	if len(given) == 0 {
		return nil, "", errors.New("Must specify at least one label, as <key>=<value>") //nolint:staticcheck // matches dokku's storage plugin
	}

	labels := make(map[string]string, len(current)+len(given))
	for key, value := range current {
		labels[key] = value
	}

	for key, value := range given {
		labels[key] = value
	}

	return labels, fmt.Sprintf("Set %d label(s) on %s", len(given), input.ServiceName), nil
}

// UnlabelServiceInput is the input for the UnlabelService function
type UnlabelServiceInput struct {
	// Datastore is the datastore the service belongs to
	Datastore *service.Datastore

	// ServiceName is the name of the service to remove labels from
	ServiceName string

	// Keys name the labels to remove
	Keys []string

	// All removes every label
	All bool
}

// UnlabelService removes labels from a service, and says what it did. What is
// written reaches the container the next time one is made.
func UnlabelService(input UnlabelServiceInput) (string, error) {
	current, err := service.ServiceLabels(input.Datastore, input.ServiceName)
	if err != nil {
		return "", err
	}

	labels, message, err := unlabeledSet(current, input)
	if err != nil {
		return "", err
	}

	if err := service.WriteLabels(input.Datastore, input.ServiceName, labels); err != nil {
		return "", err
	}

	return message, nil
}

// unlabeledSet is the set of labels an unlabel command leaves a service with.
//
// Every key is matched before anything is removed, so one naming a label the
// service does not have leaves the rest in place rather than removing some of
// them.
func unlabeledSet(current map[string]string, input UnlabelServiceInput) (map[string]string, string, error) {
	commandPrefix := input.Datastore.Properties().CommandPrefix

	if input.All {
		if len(input.Keys) > 0 {
			return nil, "", errors.New("A label cannot be specified with --all") //nolint:staticcheck // matches dokku's storage plugin
		}

		// nothing to remove is not an error, so the command can be run again
		return nil, fmt.Sprintf("Removed all labels on %s", input.ServiceName), nil
	}

	if len(input.Keys) == 0 {
		return nil, "", fmt.Errorf("Must specify at least one label, use %s --all to remove all labels", hostenv.Invocation(commandPrefix, "unlabel")) //nolint:staticcheck // matches dokku's storage plugin
	}

	labels := make(map[string]string, len(current))
	for key, value := range current {
		labels[key] = value
	}

	for _, key := range input.Keys {
		if strings.Contains(key, "=") {
			name, _, _ := strings.Cut(key, "=")
			return nil, "", fmt.Errorf("Label %q is removed by its key alone, as %s", key, name) //nolint:staticcheck // matches dokku's storage plugin
		}

		if _, ok := labels[key]; !ok {
			return nil, "", fmt.Errorf("Label %s does not exist on %s", key, input.ServiceName) //nolint:staticcheck // matches dokku's storage plugin
		}

		delete(labels, key)
	}

	return labels, fmt.Sprintf("Removed %d label(s) from %s", len(current)-len(labels), input.ServiceName), nil
}
