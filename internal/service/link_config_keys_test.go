package service

import (
	"slices"
	"testing"

	"github.com/dokku/dokku/plugins/common"
)

func TestLinkConfigKeys(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	// a link made before the keys were recorded has nothing to report
	if actual := LinkConfigKeys(redis, "lollipop", "my-app"); actual != nil {
		t.Errorf("expected nothing recorded, got %v", actual)
	}

	if err := SetLinkConfigKeys(redis, "lollipop", "my-app", []string{"REDIS_URL", "MB_DB_CONNECTION_URI", "REDIS_URL"}); err != nil {
		t.Fatalf("failed to record the keys: %s", err)
	}
	if err := SetLinkConfigKeys(redis, "lollipop", "other-app", []string{"DOKKU_REDIS_AQUA_URL"}); err != nil {
		t.Fatalf("failed to record the keys: %s", err)
	}

	// sorted and without duplicates, and kept apart for each app
	if actual := LinkConfigKeys(redis, "lollipop", "my-app"); !slices.Equal(actual, []string{"MB_DB_CONNECTION_URI", "REDIS_URL"}) {
		t.Errorf("expected the recorded keys, got %v", actual)
	}
	if actual := LinkConfigKeys(redis, "lollipop", "other-app"); !slices.Equal(actual, []string{"DOKKU_REDIS_AQUA_URL"}) {
		t.Errorf("expected the other app's keys, got %v", actual)
	}

	if err := RemoveLinkConfigKeys(redis, "lollipop", "my-app"); err != nil {
		t.Fatalf("failed to remove the keys: %s", err)
	}
	if actual := LinkConfigKeys(redis, "lollipop", "my-app"); actual != nil {
		t.Errorf("expected nothing recorded once removed, got %v", actual)
	}
	if actual := LinkConfigKeys(redis, "lollipop", "other-app"); !slices.Equal(actual, []string{"DOKKU_REDIS_AQUA_URL"}) {
		t.Errorf("expected the other app's keys to be kept, got %v", actual)
	}
}

// Recording no keys forgets the app rather than leaving an empty entry behind
func TestSetLinkConfigKeysWithNoKeys(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	if err := SetLinkConfigKeys(redis, "lollipop", "my-app", []string{"REDIS_URL"}); err != nil {
		t.Fatalf("failed to record the keys: %s", err)
	}

	if err := SetLinkConfigKeys(redis, "lollipop", "my-app", []string{}); err != nil {
		t.Fatalf("failed to record no keys: %s", err)
	}

	entries, err := common.PropertyMapGet(redis.Properties().CommandPrefix, "lollipop", LinkConfigKeysProperty)
	if err != nil {
		t.Fatalf("failed to read the property: %s", err)
	}
	if _, ok := entries["my-app"]; ok {
		t.Errorf("expected no entry for the app, got %v", entries)
	}
}

// Removing the keys of an app that has none recorded is not an error, since
// unlink and app deletion do it for links made before the keys were recorded
func TestRemoveLinkConfigKeysWithNothingRecorded(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	if err := RemoveLinkConfigKeys(redis, "lollipop", "my-app"); err != nil {
		t.Errorf("expected no error, got %s", err)
	}
}
