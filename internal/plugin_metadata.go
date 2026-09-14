package internal

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// PluginSponsors reads the github accounts credited in a plugin's plugin.toml.
// A plugin without the key, or without a plugin.toml at all, has no sponsors.
func PluginSponsors(pluginDir string) ([]string, error) {
	file, err := os.Open(filepath.Join(pluginDir, "plugin.toml"))
	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "sponsors") {
			continue
		}

		_, rest, found := strings.Cut(line, "[")
		if !found {
			continue
		}

		list, _, found := strings.Cut(rest, "]")
		if !found {
			continue
		}

		sponsors := []string{}
		for _, sponsor := range strings.Split(list, ",") {
			sponsor = strings.Trim(strings.TrimSpace(sponsor), `"`)
			if sponsor != "" {
				sponsors = append(sponsors, sponsor)
			}
		}

		return sponsors, nil
	}

	return nil, scanner.Err()
}
