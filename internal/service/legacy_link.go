package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku/plugins/common"
	dockeroptions "github.com/dokku/dokku/plugins/docker-options"
)

// legacyLinkPhases are the docker option phases earlier versions of the plugin
// added the container link to
var legacyLinkPhases = []string{"build", "deploy", "run"}

// LegacyLinkOption is the docker option earlier versions of the plugin, and the
// bash plugins before them, linked an app to a service with. Docker deprecated
// links on the default bridge in 29.6 and removes them in 30.0, so a link made
// now runs over a network instead, and one made then keeps the option until it
// is moved with link-migrate.
func LegacyLinkOption(s *Datastore, serviceName string) string {
	return fmt.Sprintf("--link %s:%s", ContainerName(s, serviceName), DNSHostname(s, serviceName))
}

// legacyOptionsFile is where dokku kept the docker options of a phase before
// 0.38, one per line. 0.38 moves them into properties and deletes the file, so
// a file that is still there means the installed dokku reads from it.
func legacyOptionsFile(appName string, phase string) string {
	return filepath.Join(hostenv.Root(), appName, "DOCKER_OPTIONS_"+strings.ToUpper(phase))
}

// legacyOptionsFileExists reports whether dokku keeps the docker options of a
// phase in a file rather than in properties
func legacyOptionsFileExists(appName string, phase string) bool {
	_, err := os.Stat(legacyOptionsFile(appName, phase))
	return err == nil
}

// readLegacyOptionsFile returns the docker options a pre-0.38 file holds, bar
// blank lines and comments
func readLegacyOptionsFile(appName string, phase string) ([]string, error) {
	contents, err := os.ReadFile(legacyOptionsFile(appName, phase))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("unable to read the %s docker options of app %s: %w", phase, appName, err)
	}

	options := []string{}
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		options = append(options, line)
	}

	return options, nil
}

// appDockerOptions returns the docker options an app has for a phase, from
// wherever the installed dokku keeps them, without asking dokku
func appDockerOptions(appName string, phase string) ([]string, error) {
	if legacyOptionsFileExists(appName, phase) {
		return readLegacyOptionsFile(appName, phase)
	}

	return dockeroptions.GetDockerOptionsForPhase(appName, phase)
}

// isLegacyLinkOption reports whether a stored docker option is the container
// link to a service, written either as `--link a:b` or as `--link=a:b`
func isLegacyLinkOption(option string, s *Datastore, serviceName string) bool {
	target := fmt.Sprintf("%s:%s", ContainerName(s, serviceName), DNSHostname(s, serviceName))

	fields := strings.Fields(option)
	if len(fields) == 1 {
		value, ok := strings.CutPrefix(fields[0], "--link=")
		return ok && value == target
	}

	return len(fields) == 2 && fields[0] == "--link" && fields[1] == target
}

// legacyLinkOptions returns the stored entries of a phase that link an app to a
// service, exactly as they are stored
func legacyLinkOptions(s *Datastore, serviceName string, appName string, phase string) ([]string, error) {
	options, err := appDockerOptions(appName, phase)
	if err != nil {
		return nil, err
	}

	found := []string{}
	for _, option := range options {
		if isLegacyLinkOption(option, s, serviceName) {
			found = append(found, option)
		}
	}

	return found, nil
}

// HasLegacyLink reports whether an app still reaches a service through the
// container link earlier versions gave it, in any phase.
//
// It is the one place the link method is decided: the app's docker options are
// what its containers are made with, so they are the record of it. A link with
// the option keeps working as it always has and gets no network, and one
// without runs over its network. Docker options that cannot be read are taken
// as holding no link, which puts the app on its network, the only way it can
// reach the service once docker stops honouring links.
func HasLegacyLink(s *Datastore, serviceName string, appName string) bool {
	for _, phase := range legacyLinkPhases {
		found, err := legacyLinkOptions(s, serviceName, appName, phase)
		if err == nil && len(found) > 0 {
			return true
		}
	}

	return false
}

// RemoveLegacyLinkOption removes the container link an earlier version gave an
// app from every phase it was added to.
//
// The options are changed through dokku's own docker-options package, which
// takes the lock dokku does. A dokku from before 0.38 reads the options from a
// file the package no longer writes, so for that dokku is asked to remove them
// itself. An app without the option is left alone, so it is safe to repeat.
func RemoveLegacyLinkOption(ctx context.Context, s *Datastore, serviceName string, appName string) error {
	for _, phase := range legacyLinkPhases {
		found, err := legacyLinkOptions(s, serviceName, appName, phase)
		if err != nil {
			return fmt.Errorf("failed to remove the docker option for app %s: %w", appName, err)
		}

		if len(found) == 0 {
			continue
		}

		if legacyOptionsFileExists(appName, phase) {
			if _, err := execx.Run(ctx, common.ExecCommandInput{
				Command: "dokku",
				Args:    []string{"docker-options:remove", appName, phase, LegacyLinkOption(s, serviceName)},
			}); err != nil {
				return fmt.Errorf("failed to remove the docker option for app %s: %w", appName, err)
			}
			continue
		}

		// each entry is removed as it is stored, since one written as
		// --link=a:b is not the same shell words as --link a:b
		for _, option := range found {
			if err := dockeroptions.RemoveDockerOptionFromPhases(appName, []string{phase}, option); err != nil {
				return fmt.Errorf("failed to remove the docker option for app %s: %w", appName, err)
			}
		}
	}

	return nil
}
