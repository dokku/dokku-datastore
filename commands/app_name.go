package commands

import "os"

// appNameOrCurrent returns the app a command was given, or the one dokku is
// already working on when it was given none.
//
// dokku exports DOKKU_APP_NAME when a command is run with --app or from inside
// an app, but injects it into the arguments only for its own core plugins, so a
// plugin outside that set has to read it for itself.
//
// The bash these commands replaced meant to do exactly this: it worked the
// default out and then passed along the arguments it was given, so the default
// it had just computed never arrived anywhere.
func appNameOrCurrent(appName string) string {
	if appName != "" {
		return appName
	}

	return os.Getenv("DOKKU_APP_NAME")
}
