package service

import (
	"github.com/dokku/dokku/plugins/common"
)

// keyserverEnv is what the backup image reads to decide where to fetch a public
// key from, and KeyserverProperty is the service property that sets it. It is a
// property rather than a file beside the other backup settings because it is
// set the way every other property is, through the set command.
//
// The name lives here rather than beside the backup code that passes it on,
// because Info reports it and internal/service cannot import the package the
// backup code is in.
const KeyserverProperty = "backup-keyserver"

// Keyserver gets the keyserver a service fetches backup public keys from
func Keyserver(s *Datastore, serviceName string) string {
	return common.PropertyGet(s.Properties().CommandPrefix, serviceName, KeyserverProperty)
}

// GlobalPropertyScope is where a property set for every service of a datastore
// is kept, the name dokku itself keeps its own global properties under. It can
// never be a service, since a service name cannot start with a dash.
const GlobalPropertyScope = "--global"

// DisablePullProperty is the global property that stops the plugin from
// fetching any image it does not already have. It replaces the
// <PLUGIN>_DISABLE_PULL environment variable, which does not survive sudo on a
// host whose sudo drops the caller's environment.
const DisablePullProperty = "disable-pull"

// GlobalProperty gets a property set for every service of a datastore
func GlobalProperty(s *Datastore, key string) string {
	return common.PropertyGet(s.Properties().CommandPrefix, GlobalPropertyScope, key)
}

// WriteGlobalProperty sets a property for every service of a datastore, or
// clears it when the value is empty
func WriteGlobalProperty(s *Datastore, key string, value string) error {
	prefix := s.Properties().CommandPrefix
	if value == "" {
		return common.PropertyDelete(prefix, GlobalPropertyScope, key)
	}

	return common.PropertyWrite(prefix, GlobalPropertyScope, key, value)
}
