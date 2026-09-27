package service

import (
	"errors"
	"os"
	"path/filepath"
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

// corruptLinkConfigKeys leaves a record for a service that is not valid json,
// as a write cut short or an edit by hand would
func corruptLinkConfigKeys(t *testing.T, s *Datastore, serviceName string) {
	t.Helper()

	path := filepath.Join(os.Getenv("DOKKU_LIB_ROOT"), "config", s.Properties().CommandPrefix, serviceName, LinkConfigKeysProperty)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create the property directory: %s", err)
	}
	if err := os.WriteFile(path, []byte(`{"other-app": "REDIS_URL"`), 0600); err != nil {
		t.Fatalf("failed to write the corrupt record: %s", err)
	}
}

// A record that cannot be parsed is read as recording nothing, and replaced by
// the next write rather than stopping every app on the service from being
// linked or unlinked. The write reports the replacement, so it can be warned
// about.
func TestSetLinkConfigKeysReplacesACorruptRecord(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)
	corruptLinkConfigKeys(t, redis, "lollipop")

	if actual := LinkConfigKeys(redis, "lollipop", "other-app"); actual != nil {
		t.Errorf("expected a corrupt record to record nothing, got %v", actual)
	}

	err := SetLinkConfigKeys(redis, "lollipop", "my-app", []string{"MB_DB_CONNECTION_URI"})
	if !errors.Is(err, ErrCorruptLinkConfigKeys) {
		t.Fatalf("expected the replacement to be reported, got %v", err)
	}

	entries, err := common.PropertyMapGet(redis.Properties().CommandPrefix, "lollipop", LinkConfigKeysProperty)
	if err != nil {
		t.Fatalf("expected the record to be readable once replaced, got %s", err)
	}
	if len(entries) != 1 || entries["my-app"] != "MB_DB_CONNECTION_URI" {
		t.Errorf("expected only the key just recorded, got %v", entries)
	}

	// and once replaced, writes go back to reporting nothing
	if err := SetLinkConfigKeys(redis, "lollipop", "other-app", []string{"REDIS_URL"}); err != nil {
		t.Errorf("expected no error once replaced, got %s", err)
	}
}

func TestRemoveLinkConfigKeysReplacesACorruptRecord(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)
	corruptLinkConfigKeys(t, redis, "lollipop")

	// the app has no entry to remove, but the record is replaced all the same,
	// so the next write does not trip over it
	err := RemoveLinkConfigKeys(redis, "lollipop", "my-app")
	if !errors.Is(err, ErrCorruptLinkConfigKeys) {
		t.Fatalf("expected the replacement to be reported, got %v", err)
	}

	entries, err := common.PropertyMapGet(redis.Properties().CommandPrefix, "lollipop", LinkConfigKeysProperty)
	if err != nil {
		t.Fatalf("expected the record to be readable once replaced, got %s", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected an empty record, got %v", entries)
	}
}
