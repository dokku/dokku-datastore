package hostenv

import "testing"

func TestRoots(t *testing.T) {
	tests := []struct {
		name             string
		libRoot          string
		libHostRoot      string
		expectedRoot     string
		expectedHostRoot string
	}{
		{
			name:             "nothing set",
			expectedRoot:     "/var/lib/dokku",
			expectedHostRoot: "/var/lib/dokku",
		},
		{
			name:             "a relocated lib root",
			libRoot:          "/opt/dokku",
			expectedRoot:     "/opt/dokku",
			expectedHostRoot: "/opt/dokku",
		},
		{
			// the two differ on a docker-in-docker install, where the path the
			// tool reads is not the path dockerd binds
			name:             "a separate host root",
			libRoot:          "/var/lib/dokku",
			libHostRoot:      "/host/var/lib/dokku",
			expectedRoot:     "/var/lib/dokku",
			expectedHostRoot: "/host/var/lib/dokku",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DOKKU_LIB_ROOT", test.libRoot)
			t.Setenv("DOKKU_LIB_HOST_ROOT", test.libHostRoot)

			if actual := LibRoot(); actual != test.expectedRoot {
				t.Errorf("expected lib root %q, got %q", test.expectedRoot, actual)
			}

			if actual := LibHostRoot(); actual != test.expectedHostRoot {
				t.Errorf("expected host root %q, got %q", test.expectedHostRoot, actual)
			}

			if actual := DataRoot(); actual != test.expectedRoot+"/services" {
				t.Errorf("expected data root %q, got %q", test.expectedRoot+"/services", actual)
			}

			if actual := HostDataRoot(); actual != test.expectedHostRoot+"/services" {
				t.Errorf("expected host data root %q, got %q", test.expectedHostRoot+"/services", actual)
			}
		})
	}
}

func TestSystemUserAndGroup(t *testing.T) {
	tests := []struct {
		name          string
		user          string
		group         string
		expectedUser  string
		expectedGroup string
	}{
		{name: "nothing set", expectedUser: "dokku", expectedGroup: "dokku"},
		{name: "both set", user: "deploy", group: "staff", expectedUser: "deploy", expectedGroup: "staff"},
		{name: "only the user set", user: "deploy", expectedUser: "deploy", expectedGroup: "dokku"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DOKKU_SYSTEM_USER", test.user)
			t.Setenv("DOKKU_SYSTEM_GROUP", test.group)

			if actual := SystemUser(); actual != test.expectedUser {
				t.Errorf("expected user %q, got %q", test.expectedUser, actual)
			}

			if actual := SystemGroup(); actual != test.expectedGroup {
				t.Errorf("expected group %q, got %q", test.expectedGroup, actual)
			}
		})
	}
}

// The base path dokku exports is the directory every plugin sits in, so a
// plugin's own directory is that path plus its name. Without the name this
// pointed at a sibling called datastore, where no plugin installs anything, and
// an override was never found at runtime even though generating a readme saw it.
func TestPluginCheckout(t *testing.T) {
	t.Setenv("PLUGIN_BASE_PATH", "/var/lib/dokku/plugins/enabled")
	t.Setenv("PLUGIN_COMMAND_PREFIX", "redis")

	if actual := PluginCheckout(); actual != "/var/lib/dokku/plugins/enabled/redis" {
		t.Errorf("expected the plugin's own directory, got %q", actual)
	}
}

// Outside a dokku install there is nowhere to look, and an empty result is what
// tells the registry to use only what it was compiled with.
func TestPluginCheckoutIsEmptyWithoutBoth(t *testing.T) {
	tests := []struct {
		name   string
		base   string
		prefix string
	}{
		{name: "no base path", base: "", prefix: "redis"},
		{name: "no command prefix", base: "/var/lib/dokku/plugins/enabled", prefix: ""},
		{name: "neither", base: "", prefix: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PLUGIN_BASE_PATH", test.base)
			t.Setenv("PLUGIN_COMMAND_PREFIX", test.prefix)

			if actual := PluginCheckout(); actual != "" {
				t.Errorf("expected nowhere to look, got %q", actual)
			}
		})
	}
}
