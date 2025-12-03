package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// FilterServices filters out services that are not allowed by the user-auth-service trigger
func FilterServices(datastoreType string, services []string, trace bool) ([]string, error) {
	if len(services) == 0 {
		return services, nil
	}

	// check if there are plugins with the user-auth-service trigger
	triggers, err := filepath.Glob(filepath.Join(PluginPath, "enabled", "*", "user-auth-service"))
	if err != nil {
		if os.IsNotExist(err) {
			return services, nil
		}
		return services, fmt.Errorf("failed to glob plugins with user-auth-service trigger: %w", err)
	}

	if len(triggers) == 0 {
		return services, nil
	}

	// check if there is only one trigger and if the file  `PLUGIN_PATH/enabled/20_events/user-auth-service` exists
	if len(triggers) == 1 {
		if _, err := os.Stat(filepath.Join(PluginPath, "enabled", "20_events", "user-auth-service")); err == nil {
			return services, nil
		}
	}

	// the output of this trigger should be all the services a user has access to
	defaultSShUser := os.Getenv("SSH_USER")
	defaultSShName := os.Getenv("SSH_NAME")
	if defaultSShUser == "" {
		defaultSShUser = os.Getenv("USER")
	}
	if defaultSShName == "" {
		defaultSShName = "default"
	}

	// todo: map datastoreType to plugin command prefix
	pluginCommandPrefix := datastoreType

	// call the user-auth-service trigger
	results, err := common.CallPlugnTrigger(common.PlugnTriggerInput{
		Trigger: "user-auth-app",
		Args:    append([]string{defaultSShUser, defaultSShName, pluginCommandPrefix}, services...),
		Env: map[string]string{
			"SSH_NAME": defaultSShName,
			"SSH_USER": defaultSShUser,
			"TRACE":    strconv.FormatBool(trace),
		},
	})
	if err != nil {
		return services, fmt.Errorf("failed to call user-auth-service trigger: %w", err)
	}

	filteredServices := make([]string, 0)
	for line := range strings.SplitSeq(results.StderrContents(), "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}

		filteredServices = append(filteredServices, trimmedLine)
	}

	return filteredServices, nil
}
