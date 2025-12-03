package internal

import (
	"os"
	"path/filepath"
)

var (
	PluginDataRoot string
	PluginPath     string
	DokkuLibRoot   string
)

func init() {
	DokkuLibRoot = os.Getenv("DOKKU_LIB_ROOT")
	PluginPath = filepath.Join(DokkuLibRoot, "plugins")
	PluginDataRoot = filepath.Join(DokkuLibRoot, "services")
}
