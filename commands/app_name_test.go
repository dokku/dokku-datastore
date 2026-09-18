package commands

import "testing"

// The app a command was given always wins: dokku exports DOKKU_APP_NAME
// whenever it is working inside an app, so an explicit argument has to mean
// that app rather than the one the shell happens to be in.
func TestAppNameOrCurrentPrefersTheArgument(t *testing.T) {
	t.Setenv("DOKKU_APP_NAME", "the-current-app")

	if actual := appNameOrCurrent("named-app"); actual != "named-app" {
		t.Errorf("expected the named app, got %q", actual)
	}
}

// And with no argument it falls back to the app dokku is working on, which is
// what running the command inside an app is supposed to mean.
func TestAppNameOrCurrentFallsBackToTheCurrentApp(t *testing.T) {
	t.Setenv("DOKKU_APP_NAME", "the-current-app")

	if actual := appNameOrCurrent(""); actual != "the-current-app" {
		t.Errorf("expected the current app, got %q", actual)
	}
}

// Outside an app there is nothing to fall back to, and the commands report a
// missing app name rather than acting on a guess.
func TestAppNameOrCurrentIsEmptyOutsideAnApp(t *testing.T) {
	t.Setenv("DOKKU_APP_NAME", "")

	if actual := appNameOrCurrent(""); actual != "" {
		t.Errorf("expected nothing to fall back to, got %q", actual)
	}
}
