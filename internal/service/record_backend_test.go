package service

import (
	"os"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/backend"
)

// A backend a create asked for is recorded before the container is made, and is
// what the service is driven with from then on
func TestRecordBackend(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")

	if err := os.MkdirAll(Folders(redis, "lollipop").Root, 0755); err != nil {
		t.Fatalf("failed to make the service root: %v", err)
	}

	if err := redis.RecordBackend("lollipop", ""); err != nil {
		t.Fatalf("expected an empty backend to be left alone, got %v", err)
	}

	if recorded := redis.RecordedBackend("lollipop"); recorded != "" {
		t.Errorf("expected nothing to be recorded for an empty backend, got %q", recorded)
	}

	if selected := redis.backend("lollipop"); selected != backend.Docker {
		t.Errorf("expected a service with nothing recorded to be driven with docker, got %q", selected)
	}

	if err := redis.RecordBackend("lollipop", backend.Compose); err != nil {
		t.Fatalf("expected compose to be recorded, got %v", err)
	}

	if recorded := redis.RecordedBackend("lollipop"); recorded != backend.Compose {
		t.Errorf("expected compose to be recorded, got %q", recorded)
	}

	if selected := redis.backend("lollipop"); selected != backend.Compose {
		t.Errorf("expected the service to be driven with compose, got %q", selected)
	}

	err := redis.RecordBackend("lollipop", "podman")
	if err == nil || !strings.Contains(err.Error(), `invalid backend "podman"`) {
		t.Errorf("expected an unknown backend to be refused, got %v", err)
	}

	if recorded := redis.RecordedBackend("lollipop"); recorded != backend.Compose {
		t.Errorf("expected a refused backend to leave the record alone, got %q", recorded)
	}
}
