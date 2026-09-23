package internal

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// InfoKey is one entry the info command answers, and the description its flag
// carries
type InfoKey struct {
	// Name is the key as info names it, and the flag that selects it
	Name string

	// Description is what the key holds, as the flag documents it
	Description string
}

// InfoKeys are every key the info command answers, the ones service.Info
// supplies included.
//
// The command generates its flags from this list rather than declaring them one
// by one, so a key can never end up without a flag - which is the hole info
// carried for as long as it returned config-options without accepting
// --config-options. Every property the set command writes is named here, so
// anything that can be set can be read back.
var InfoKeys = []InfoKey{
	{Name: "backend", Description: "show the execution backend the service was created with"},
	{Name: "backup-authenticated", Description: "show whether backup credentials are stored for the service"},
	{Name: "backup-bucket", Description: "show the bucket scheduled backups are shipped to"},
	{Name: "backup-encrypted", Description: "show whether scheduled backups are encrypted with a passphrase"},
	{Name: "backup-keyserver", Description: "show the keyserver backup public keys are fetched from"},
	{Name: "backup-public-key-id", Description: "show the gpg public key id backups are encrypted with"},
	{Name: "backup-schedule", Description: "show the cron schedule backups run on"},
	{Name: "backup-use-iam", Description: "show whether scheduled backups authenticate with an instance role"},
	{Name: "config-dir", Description: "show the service configuration directory"},
	{Name: "config-options", Description: "show the config options the service container is run with"},
	{Name: "custom-env", Description: "show the custom environment the service container is run with"},
	{Name: "data-dir", Description: "show the service data directory"},
	{Name: "database-name", Description: "show the name of the database inside the service"},
	{Name: "definition", Description: "show the definition the service was created with"},
	{Name: "dsn", Description: "show the service DSN"},
	{Name: "exposed-ports", Description: "show service exposed ports"},
	{Name: "id", Description: "show the service container id"},
	{Name: "image", Description: "show the image the service runs"},
	{Name: "image-version", Description: "show the image version the service was created with"},
	{Name: "initial-network", Description: "show the initial network being connected to"},
	{Name: "internal-ip", Description: "show the service internal ip"},
	{Name: "links", Description: "show the service app links"},
	{Name: "log-driver", Description: "show the docker logging driver the service container is run with"},
	{Name: "log-opt", Description: "show the docker log options the service container is run with"},
	{Name: "memory", Description: "show the memory limit the service container is run with"},
	{Name: "post-create-network", Description: "show the networks to attach to after service container creation"},
	{Name: "post-start-network", Description: "show the networks to attach to after service container start"},
	{Name: "service", Description: "show the name of the service"},
	{Name: "service-root", Description: "show the service root directory"},
	{Name: "shm-size", Description: "show the shared memory size the service container is run with"},
	{Name: "status", Description: "show the service running status"},
	{Name: "version", Description: "show the service image version"},
}

// InfoKeyNames returns the keys info answers, in the order they are declared
func InfoKeyNames() []string {
	names := make([]string, 0, len(InfoKeys))
	for _, key := range InfoKeys {
		names = append(names, key.Name)
	}

	return names
}

// InfoInput is the input for the Info function
type InfoInput struct {
	// Datastore is the service to get the information for
	Datastore *service.Datastore

	// ServiceName is the name of the service to get the information for
	ServiceName string
}

// Info returns the state of a service.
//
// It builds on service.Info, which answers the connection oriented keys and is
// all the service package can answer without knowing how backups are stored,
// and adds what that has no way of saying: every property the set command
// writes, the state recorded when the service was created, and the backup
// settings. Nothing secret is reported - stored credentials and the backup
// passphrase are reported as being present rather than as their values.
//
// It returns no error. A service whose container is gone, or whose files were
// never written, reports an empty value rather than failing, because a report
// of a service in a bad state is what says it is in one.
func Info(ctx context.Context, input InfoInput) map[string]string {
	info := service.Info(ctx, service.InfoInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})

	serviceFiles := service.Files(input.Datastore, input.ServiceName)
	serviceFolders := service.Folders(input.Datastore, input.ServiceName)
	commandPrefix := input.Datastore.Properties().CommandPrefix

	info["service"] = input.ServiceName
	info["backend"] = common.ReadFirstLine(serviceFiles.Backend)
	info[service.KeyserverProperty] = service.Keyserver(input.Datastore, input.ServiceName)
	info["custom-env"] = customEnv(serviceFiles.Env)
	info["database-name"] = common.ReadFirstLine(serviceFiles.DatabaseName)
	info["definition"] = common.ReadFirstLine(serviceFiles.Definition)
	info["image"] = common.ReadFirstLine(serviceFiles.Image)
	info["image-version"] = common.ReadFirstLine(serviceFiles.ImageVersion)
	// what was set rather than what the container ended up with: settling that
	// needs a daemon and a plugn trigger, and this is the one report that has to
	// answer for a service whose container is gone. The readme says what an
	// unset value inherits
	info[service.LogDriverProperty] = common.PropertyGet(commandPrefix, input.ServiceName, service.LogDriverProperty)
	info[service.LogOptProperty] = common.PropertyGet(commandPrefix, input.ServiceName, service.LogOptProperty)
	info["memory"] = common.ReadFirstLine(serviceFiles.Memory)
	info["shm-size"] = common.ReadFirstLine(serviceFiles.ShmSize)

	info["backup-authenticated"] = strconv.FormatBool(common.FileExists(filepath.Join(serviceFolders.Backup, accessKeyIDFile)))
	info["backup-encrypted"] = strconv.FormatBool(common.FileExists(filepath.Join(serviceFolders.BackupEncryption, encryptionKeyFile)))
	info["backup-public-key-id"] = common.ReadFirstLine(filepath.Join(serviceFolders.BackupEncryption, publicKeyIDFile))

	schedule, _ := ParseCronEntry(input.Datastore.Properties().CommandPrefix, common.ReadFirstLine(serviceFiles.CronFile))
	info["backup-schedule"] = schedule.Schedule
	info["backup-bucket"] = schedule.BucketName
	info["backup-use-iam"] = strconv.FormatBool(schedule.UseIAM)

	return info
}

// customEnv renders the environment a service was created with the way the
// create command takes it, since the table info prints cannot carry the
// newlines the file is written with
func customEnv(filename string) string {
	lines, err := common.FileToSlice(filename)
	if err != nil {
		return ""
	}

	return strings.Join(lines, ";")
}
