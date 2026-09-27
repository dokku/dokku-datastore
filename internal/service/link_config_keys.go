package service

import (
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

// LinkConfigKeys returns the config keys recorded as holding the service url on
// an app, or nil when nothing was recorded, as for a link made by an earlier
// version of the plugin
func LinkConfigKeys(s *Datastore, serviceName string, appName string) []string {
	entries, err := common.PropertyMapGet(s.Properties().CommandPrefix, serviceName, LinkConfigKeysProperty)
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

// SetLinkConfigKeys records the config keys holding the service url on an app,
// replacing whatever was recorded before. Recording no keys removes the entry.
func SetLinkConfigKeys(s *Datastore, serviceName string, appName string, keys []string) error {
	keys = slices.DeleteFunc(slices.Clone(keys), func(key string) bool {
		return strings.TrimSpace(key) == ""
	})
	if len(keys) == 0 {
		return RemoveLinkConfigKeys(s, serviceName, appName)
	}

	slices.Sort(keys)
	keys = slices.Compact(keys)

	return common.PropertyMapSet(s.Properties().CommandPrefix, serviceName, LinkConfigKeysProperty, appName, strings.Join(keys, ","))
}

// RemoveLinkConfigKeys forgets the config keys recorded for an app
func RemoveLinkConfigKeys(s *Datastore, serviceName string, appName string) error {
	return common.PropertyMapDelete(s.Properties().CommandPrefix, serviceName, LinkConfigKeysProperty, appName)
}
