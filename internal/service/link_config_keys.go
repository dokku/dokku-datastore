package service

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// LinkConfigKeysProperty records, for each app linked to a service, the config
// keys that hold the service url on that app.
//
// The keys used to be found only by the url they held, which stops working the
// moment the url on the app is changed in any way, such as its scheme. The
// record is what lets unlink and promote find a key that no longer holds the
// exact url, and one that was named by the user rather than generated.
const LinkConfigKeysProperty = "link-config-keys"

// LinkJDBCConfigKeysProperty records, for each app linked to a service with
// --jdbc, the config keys that hold the jdbc url, username and password.
//
// It is kept apart from LinkConfigKeysProperty because everything that reads
// that record expects each of its keys to hold the service url: unlink and
// promote find them by it, promote copies one onto the default alias, and a
// password change rewrites the url inside them.
const LinkJDBCConfigKeysProperty = "link-jdbc-config-keys"

// LinkConfigKeys returns the config keys recorded as holding the service url on
// an app, or nil when nothing was recorded, as for a link made by an earlier
// version of the plugin
func LinkConfigKeys(s *Datastore, serviceName string, appName string) []string {
	return recordedKeys(s, serviceName, appName, LinkConfigKeysProperty)
}

// LinkJDBCConfigKeys returns the config keys recorded as holding the jdbc
// values on an app, or nil when it was not linked with --jdbc
func LinkJDBCConfigKeys(s *Datastore, serviceName string, appName string) []string {
	return recordedKeys(s, serviceName, appName, LinkJDBCConfigKeysProperty)
}

// recordedKeys returns the config keys a property records for an app
func recordedKeys(s *Datastore, serviceName string, appName string, property string) []string {
	entries, err := common.PropertyMapGet(s.Properties().CommandPrefix, serviceName, property)
	if err != nil {
		return nil
	}

	value := strings.TrimSpace(entries[appName])
	if value == "" {
		return nil
	}

	keys := []string{}
	for _, key := range strings.Split(value, ",") {
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}

	return keys
}

// ErrCorruptLinkConfigKeys reports that the recorded config keys of a service
// could not be parsed. A write replaces them rather than failing, since a record
// that cannot be read would otherwise stop every app on the service from being
// linked or unlinked. It is returned alongside a successful write, so the caller
// can warn that the keys recorded for the other apps were lost.
var ErrCorruptLinkConfigKeys = errors.New("the recorded config keys could not be parsed, and were replaced")

// linkConfigKeysEntries reads every app's recorded config keys for a service.
// A record that is not valid json is reported as corrupt alongside an empty
// set of entries to write over it, while one that cannot be read at all, such
// as for its permissions, is an error, as writing over it would fail too.
func linkConfigKeysEntries(s *Datastore, serviceName string, property string) (map[string]string, bool, error) {
	entries, err := common.PropertyMapGet(s.Properties().CommandPrefix, serviceName, property)
	if err == nil {
		return entries, false, nil
	}

	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
		return map[string]string{}, true, nil
	}

	return nil, false, err
}

// writeLinkConfigKeys writes every app's recorded config keys for a service,
// reporting whether a corrupt record was replaced
func writeLinkConfigKeys(s *Datastore, serviceName string, property string, entries map[string]string, corrupt bool) error {
	if err := common.PropertyMapWrite(s.Properties().CommandPrefix, serviceName, property, entries); err != nil {
		return err
	}

	if corrupt {
		return ErrCorruptLinkConfigKeys
	}

	return nil
}

// SetLinkConfigKeys records the config keys holding the service url on an app,
// replacing whatever was recorded before. Recording no keys removes the entry.
// A corrupt record is replaced, and ErrCorruptLinkConfigKeys returned once the
// write has succeeded.
func SetLinkConfigKeys(s *Datastore, serviceName string, appName string, keys []string) error {
	return setRecordedKeys(s, serviceName, appName, LinkConfigKeysProperty, keys)
}

// SetLinkJDBCConfigKeys records the config keys holding the jdbc values on an
// app, the same way SetLinkConfigKeys records the ones holding the url
func SetLinkJDBCConfigKeys(s *Datastore, serviceName string, appName string, keys []string) error {
	return setRecordedKeys(s, serviceName, appName, LinkJDBCConfigKeysProperty, keys)
}

// setRecordedKeys records the config keys a property holds for an app
func setRecordedKeys(s *Datastore, serviceName string, appName string, property string, keys []string) error {
	keys = slices.DeleteFunc(slices.Clone(keys), func(key string) bool {
		return strings.TrimSpace(key) == ""
	})
	if len(keys) == 0 {
		return removeRecordedKeys(s, serviceName, appName, property)
	}

	slices.Sort(keys)
	keys = slices.Compact(keys)

	entries, corrupt, err := linkConfigKeysEntries(s, serviceName, property)
	if err != nil {
		return err
	}

	entries[appName] = strings.Join(keys, ",")

	return writeLinkConfigKeys(s, serviceName, property, entries, corrupt)
}

// RemoveLinkConfigKeys forgets the config keys recorded for an app. A corrupt
// record is replaced, and ErrCorruptLinkConfigKeys returned once the write has
// succeeded.
func RemoveLinkConfigKeys(s *Datastore, serviceName string, appName string) error {
	return removeRecordedKeys(s, serviceName, appName, LinkConfigKeysProperty)
}

// RemoveLinkJDBCConfigKeys forgets the jdbc config keys recorded for an app,
// the same way RemoveLinkConfigKeys forgets the ones holding the url
func RemoveLinkJDBCConfigKeys(s *Datastore, serviceName string, appName string) error {
	return removeRecordedKeys(s, serviceName, appName, LinkJDBCConfigKeysProperty)
}

// removeRecordedKeys forgets the config keys a property holds for an app
func removeRecordedKeys(s *Datastore, serviceName string, appName string, property string) error {
	if !common.PropertyExists(s.Properties().CommandPrefix, serviceName, property) {
		return nil
	}

	entries, corrupt, err := linkConfigKeysEntries(s, serviceName, property)
	if err != nil {
		return err
	}

	if _, ok := entries[appName]; !ok && !corrupt {
		return nil
	}

	delete(entries, appName)

	return writeLinkConfigKeys(s, serviceName, property, entries, corrupt)
}
