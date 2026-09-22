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
