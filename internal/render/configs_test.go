package render

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
)

func TestConfigsForRedis(t *testing.T) {
	configs, err := Configs(redisInput(t))
	if err != nil {
		t.Fatalf("unable to resolve configs: %s", err)
	}

	// redis.conf, and the acl file holding the default user
	if len(configs) != 2 {
		t.Fatalf("expected two configs, got %d", len(configs))
	}

	config := configs[0]
	if strings.HasSuffix(config.Path, "users.acl") {
		config = configs[1]
	}

	// the tool side path, not the bind mount source: the two differ on a docker
	// in docker install and only one of them is writable from here
	if expected := "/var/lib/dokku/services/redis/lollipop/config/redis.conf"; config.Path != expected {
		t.Errorf("expected %q, got %q", expected, config.Path)
	}

	if expected := "requirepass hunter2\n"; config.Content != expected {
		t.Errorf("expected %q, got %q", expected, config.Content)
	}

	if config.Mode != fs.FileMode(0644) {
		t.Errorf("expected mode 0644, got %v", config.Mode)
	}
}

func TestConfigsAppliesADeclaredMode(t *testing.T) {
	input := Input{
		Definition: definition.Definition{
			Configs: map[string]definition.Config{
				"certs": {Content: "{{ .ServiceName }}"},
			},
			Service: definition.Service{
				Volumes: []definition.Volume{
					{Type: "bind", Source: definition.HostRootTemplate + "/config", Target: "/etc/postgres"},
				},
				Configs: []definition.ServiceConfig{
					{Source: "certs", Target: "/etc/postgres/server.key", Mode: "0600", UID: "999", GID: "999"},
				},
			},
		},
		Scope: definition.Scope{ServiceName: "lollipop", ServiceRoot: "/var/lib/dokku/services/postgres/lollipop"},
	}

	configs, err := Configs(input)
	if err != nil {
		t.Fatalf("unable to resolve configs: %s", err)
	}

	config := configs[0]
	if config.Mode != fs.FileMode(0600) {
		t.Errorf("expected mode 0600, got %v", config.Mode)
	}

	if config.UID != "999" || config.GID != "999" {
		t.Errorf("expected 999:999, got %s:%s", config.UID, config.GID)
	}

	if expected := "lollipop"; config.Content != expected {
		t.Errorf("expected %q, got %q", expected, config.Content)
	}
}

// redis is told to read its users from the acl file, which replaces the default
// user with the one it names, so the file is seeded with the default user and
// its password, and readable by no one but dokku and the datastore
func TestConfigsSeedRedisDefaultUser(t *testing.T) {
	configs, err := Configs(redisInput(t))
	if err != nil {
		t.Fatalf("unable to resolve configs: %s", err)
	}

	for _, config := range configs {
		if !strings.HasSuffix(config.Path, "users.acl") {
			continue
		}

		if expected := "/var/lib/dokku/services/redis/lollipop/data/users.acl"; config.Path != expected {
			t.Errorf("expected %q, got %q", expected, config.Path)
		}

		if expected := "user default on >hunter2 ~* &* +@all\n"; config.Content != expected {
			t.Errorf("expected %q, got %q", expected, config.Content)
		}

		if config.Mode != fs.FileMode(0640) {
			t.Errorf("expected mode 0640, got %v", config.Mode)
		}

		return
	}

	t.Fatal("expected the acl file to be seeded")
}
