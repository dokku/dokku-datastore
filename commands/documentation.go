package commands

import "github.com/dokku/dokku-datastore/internal/definition"

// The metadata below was migrated out of the declare desc and #A / #E / #F comment
// annotations that the bash datastore plugins carried in each subcommand. Both the
// plugin help and the generated plugin readme render from it, so the two can no
// longer drift apart. Every string is a text/template evaluated against the
// datastore's properties, so one body serves every datastore.

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *AppLinksCommand) Description() string {
	return `list all {{.Title}} service links for a given app`
}

// Usage returns the argument sketch rendered after the command name
func (c *AppLinksCommand) Usage() string {
	return `[<app>]`
}

// Documentation returns the long form documentation for the command
func (c *AppLinksCommand) Documentation() string {
	return `list all {{.CommandPrefix}} services that are linked to the 'playground' app.
dokku {{.CommandPrefix}}:app-links playground`
}

// Group is the readme usage section the command is documented under
func (c *AppLinksCommand) Group() string {
	return definition.GroupServiceAutomation
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *BackupCommand) Description() string {
	return `create a backup of the {{.Title}} service to an existing s3 bucket`
}

// Usage returns the argument sketch rendered after the command name
func (c *BackupCommand) Usage() string {
	return `<service> <bucket-name> [-u|--use-iam]`
}

// Documentation returns the long form documentation for the command
func (c *BackupCommand) Documentation() string {
	return `backup the 'lollipop' service to the 'my-s3-bucket' bucket on AWS
dokku {{.CommandPrefix}}:backup lollipop my-s3-bucket --use-iam
restore a backup file (assuming it was extracted via 'tar -xf backup.tgz')
dokku {{.CommandPrefix}}:import lollipop < backup-folder/export`
}

// Group is the readme usage section the command is documented under
func (c *BackupCommand) Group() string {
	return definition.GroupBackups
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *BackupAuthCommand) Description() string {
	return `set up authentication for backups on the {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *BackupAuthCommand) Usage() string {
	return `<service> <aws-access-key-id> <aws-secret-access-key> <aws-default-region> <aws-signature-version> <endpoint-url>`
}

// Documentation returns the long form documentation for the command
func (c *BackupAuthCommand) Documentation() string {
	return `setup s3 backup authentication
dokku {{.CommandPrefix}}:backup-auth lollipop AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
setup s3 backup authentication with different region
dokku {{.CommandPrefix}}:backup-auth lollipop AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_REGION
setup s3 backup authentication with different signature version and endpoint
dokku {{.CommandPrefix}}:backup-auth lollipop AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_REGION AWS_SIGNATURE_VERSION ENDPOINT_URL
more specific example for minio auth
dokku {{.CommandPrefix}}:backup-auth lollipop MINIO_ACCESS_KEY_ID MINIO_SECRET_ACCESS_KEY us-east-1 s3v4 https://YOURMINIOSERVICE`
}

// Group is the readme usage section the command is documented under
func (c *BackupAuthCommand) Group() string {
	return definition.GroupBackups
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *BackupDeauthCommand) Description() string {
	return `remove backup authentication for the {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *BackupDeauthCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *BackupDeauthCommand) Documentation() string {
	return `remove s3 authentication
dokku {{.CommandPrefix}}:backup-deauth lollipop`
}

// Group is the readme usage section the command is documented under
func (c *BackupDeauthCommand) Group() string {
	return definition.GroupBackups
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *BackupScheduleCommand) Description() string {
	return `schedule a backup of the {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *BackupScheduleCommand) Usage() string {
	return `<service> <schedule> <bucket-name> [-u|--use-iam]`
}

// Documentation returns the long form documentation for the command
func (c *BackupScheduleCommand) Documentation() string {
	return `schedule a backup
> 'schedule' is a crontab expression, eg. "0 3 * * *" for each day at 3am
dokku {{.CommandPrefix}}:backup-schedule lollipop "0 3 * * *" my-s3-bucket
schedule a backup and authenticate via iam
dokku {{.CommandPrefix}}:backup-schedule lollipop "0 3 * * *" my-s3-bucket --use-iam`
}

// Group is the readme usage section the command is documented under
func (c *BackupScheduleCommand) Group() string {
	return definition.GroupBackups
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *BackupScheduleCatCommand) Description() string {
	return `cat the contents of the configured backup cronfile for the service`
}

// Usage returns the argument sketch rendered after the command name
func (c *BackupScheduleCatCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *BackupScheduleCatCommand) Documentation() string {
	return `cat the contents of the configured backup cronfile for the service
dokku {{.CommandPrefix}}:backup-schedule-cat lollipop`
}

// Group is the readme usage section the command is documented under
func (c *BackupScheduleCatCommand) Group() string {
	return definition.GroupBackups
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *BackupSetEncryptionCommand) Description() string {
	return `set encryption for all future backups of {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *BackupSetEncryptionCommand) Usage() string {
	return `<service> <passphrase>`
}

// Documentation returns the long form documentation for the command
func (c *BackupSetEncryptionCommand) Documentation() string {
	return `set the GPG-compatible passphrase for encrypting backups for backups
dokku {{.CommandPrefix}}:backup-set-encryption lollipop
public key encryption will take precendence over the passphrase encryption if both types are set.`
}

// Group is the readme usage section the command is documented under
func (c *BackupSetEncryptionCommand) Group() string {
	return definition.GroupBackups
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *BackupSetPublicKeyEncryptionCommand) Description() string {
	return `set GPG Public Key encryption for all future backups of {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *BackupSetPublicKeyEncryptionCommand) Usage() string {
	return `<service> <public-key-id>`
}

// Documentation returns the long form documentation for the command
func (c *BackupSetPublicKeyEncryptionCommand) Documentation() string {
	return `set the GPG Public Key for encrypting backups
dokku {{.CommandPrefix}}:backup-set-public-key-encryption lollipop
the <public-key-id> is fetched from 'keyserver.ubuntu.com', unless the service names another one with the backup-keyserver property
dokku {{.CommandPrefix}}:set lollipop backup-keyserver hkp://keys.example.com`
}

// Group is the readme usage section the command is documented under
func (c *BackupSetPublicKeyEncryptionCommand) Group() string {
	return definition.GroupBackups
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *BackupUnscheduleCommand) Description() string {
	return `unschedule the backup of the {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *BackupUnscheduleCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *BackupUnscheduleCommand) Documentation() string {
	return `remove the scheduled backup from cron
dokku {{.CommandPrefix}}:backup-unschedule lollipop`
}

// Group is the readme usage section the command is documented under
func (c *BackupUnscheduleCommand) Group() string {
	return definition.GroupBackups
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *BackupUnsetEncryptionCommand) Description() string {
	return `unset encryption for future backups of the {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *BackupUnsetEncryptionCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *BackupUnsetEncryptionCommand) Documentation() string {
	return `unset the GPG encryption passphrase for backups
dokku {{.CommandPrefix}}:backup-unset-encryption lollipop`
}

// Group is the readme usage section the command is documented under
func (c *BackupUnsetEncryptionCommand) Group() string {
	return definition.GroupBackups
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *BackupUnsetPublicKeyEncryptionCommand) Description() string {
	return `unset GPG Public Key encryption for future backups of the {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *BackupUnsetPublicKeyEncryptionCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *BackupUnsetPublicKeyEncryptionCommand) Documentation() string {
	return `unset the GPG Public Key encryption for backups
dokku {{.CommandPrefix}}:backup-unset-public-key-encryption lollipop`
}

// Group is the readme usage section the command is documented under
func (c *BackupUnsetPublicKeyEncryptionCommand) Group() string {
	return definition.GroupBackups
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *CloneCommand) Description() string {
	return `create container <new-name> then copy data from <name> into <new-name>`
}

// Usage returns the argument sketch rendered after the command name
func (c *CloneCommand) Usage() string {
	return `<service> <new-service> [--clone-flags...]`
}

// Documentation returns the long form documentation for the command
func (c *CloneCommand) Documentation() string {
	return `you can clone an existing service to a new one
dokku {{.CommandPrefix}}:clone lollipop lollipop-2`
}

// Group is the readme usage section the command is documented under
func (c *CloneCommand) Group() string {
	return definition.GroupServiceAutomation
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *ConnectCommand) Description() string {
	return `connect to the service via the {{.CommandPrefix}} connection tool`
}

// Usage returns the argument sketch rendered after the command name
func (c *ConnectCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *ConnectCommand) Documentation() string {
	return `connect to the service via the {{.CommandPrefix}} connection tool
> NOTE: disconnecting from ssh while running this command may leave zombie processes due to moby/moby#9098
dokku {{.CommandPrefix}}:connect lollipop`
}

// Group is the readme usage section the command is documented under
func (c *ConnectCommand) Group() string {
	return definition.GroupServiceLifecycle
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *CreateCommand) Description() string {
	return `create a {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *CreateCommand) Usage() string {
	return `<service> [--create-flags...]`
}

// Documentation returns the long form documentation for the command
func (c *CreateCommand) Documentation() string {
	return `create a {{.CommandPrefix}} service named lollipop
dokku {{.CommandPrefix}}:create lollipop
you can also specify the image and image version to use for the service.
it *must* be compatible with the {{.Image}} image.
export {{.PluginVariable}}_IMAGE="{{.Image}}"
export {{.PluginVariable}}_IMAGE_VERSION="{{.ImageVersion}}"
dokku {{.CommandPrefix}}:create lollipop
an image other than {{.Image}} has no version to fall back on, because the
version this plugin pins belongs to {{.Image}}, so name one alongside it.
dokku {{.CommandPrefix}}:create lollipop --image <image> --image-version <version>
you can also specify custom environment variables to start
the {{.CommandPrefix}} service in semicolon-separated form.
export {{.PluginVariable}}_CUSTOM_ENV="USER=alpha;HOST=beta"
dokku {{.CommandPrefix}}:create lollipop
the container log is bounded by whatever 'dokku logs:set --global max-size' says, and
by dokku's own default where it says nothing, which a service may override for itself.
dokku {{.CommandPrefix}}:create lollipop --log-opt max-size=20m,max-file=3
the container is restarted by docker whenever it stops, which a service may change for itself.
dokku {{.CommandPrefix}}:create lollipop --restart unless-stopped`
}

// Group is the readme usage section the command is documented under
func (c *CreateCommand) Group() string {
	return definition.GroupBasicUsage
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *DestroyCommand) Description() string {
	return `delete the {{.Title}} service/data/container if there are no links left`
}

// Usage returns the argument sketch rendered after the command name
func (c *DestroyCommand) Usage() string {
	return `<service> [-f|--force]`
}

// Documentation returns the long form documentation for the command
func (c *DestroyCommand) Documentation() string {
	return `destroy the service, it's data, and the running container
dokku {{.CommandPrefix}}:destroy lollipop`
}

// Group is the readme usage section the command is documented under
func (c *DestroyCommand) Group() string {
	return definition.GroupBasicUsage
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *EnterCommand) Description() string {
	return `enter or run a command in a running {{.Title}} service container`
}

// Usage returns the argument sketch rendered after the command name
func (c *EnterCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *EnterCommand) Documentation() string {
	return `a bash prompt can be opened against a running service.
filesystem changes will not be saved to disk.
> NOTE: disconnecting from ssh while running this command may leave zombie processes due to moby/moby#9098
dokku {{.CommandPrefix}}:enter lollipop
you may also run a command directly against the service.
filesystem changes will not be saved to disk.
dokku {{.CommandPrefix}}:enter lollipop touch /tmp/test`
}

// Group is the readme usage section the command is documented under
func (c *EnterCommand) Group() string {
	return definition.GroupServiceLifecycle
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *ExistsCommand) Description() string {
	return `check if the {{.Title}} service exists`
}

// Usage returns the argument sketch rendered after the command name
func (c *ExistsCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *ExistsCommand) Documentation() string {
	return `here we check if the lollipop {{.CommandPrefix}} service exists.
dokku {{.CommandPrefix}}:exists lollipop`
}

// Group is the readme usage section the command is documented under
func (c *ExistsCommand) Group() string {
	return definition.GroupServiceAutomation
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *ExportCommand) Description() string {
	return `export a dump of the {{.Title}} service database`
}

// Usage returns the argument sketch rendered after the command name
func (c *ExportCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *ExportCommand) Documentation() string {
	return `by default, datastore output is exported to stdout
dokku {{.CommandPrefix}}:export lollipop
you can redirect this output to a file
dokku {{.CommandPrefix}}:export lollipop > data.dump`
}

// Group is the readme usage section the command is documented under
func (c *ExportCommand) Group() string {
	return definition.GroupDataManagement
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *ExposeCommand) Description() string {
	return `expose a {{.Title}} service on custom host:port if provided (random port on the 0.0.0.0 interface if otherwise unspecified)`
}

// Usage returns the argument sketch rendered after the command name
func (c *ExposeCommand) Usage() string {
	return `<service> <ports...>`
}

// Documentation returns the long form documentation for the command
func (c *ExposeCommand) Documentation() string {
	return `expose the service on the service's normal ports, allowing access to it from the public interface (0.0.0.0)
dokku {{.CommandPrefix}}:expose lollipop {{.PortList}}
expose the service on the service's normal ports, with the first on a specified ip address (127.0.0.1)
dokku {{.CommandPrefix}}:expose lollipop 127.0.0.1:{{.PortList}}`
}

// Group is the readme usage section the command is documented under
func (c *ExposeCommand) Group() string {
	return definition.GroupServiceLifecycle
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *ImportCommand) Description() string {
	return `import a dump into the {{.Title}} service database`
}

// Usage returns the argument sketch rendered after the command name
func (c *ImportCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *ImportCommand) Documentation() string {
	return `import a datastore dump
dokku {{.CommandPrefix}}:import lollipop < data.dump`
}

// Group is the readme usage section the command is documented under
func (c *ImportCommand) Group() string {
	return definition.GroupDataManagement
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *InfoCommand) Description() string {
	return `print the service information`
}

// Usage returns the argument sketch rendered after the command name
func (c *InfoCommand) Usage() string {
	return `[<service>] [--info-flags...]`
}

// Documentation returns the long form documentation for the command
func (c *InfoCommand) Documentation() string {
	return `get connection information as follows:
dokku {{.CommandPrefix}}:info lollipop
alongside the connection information this reports the properties set on the service, the state it was created with, and its backup settings.
a property that was never set, or that was unset, reports as empty.
omit the service to report on every {{.CommandPrefix}} service:
dokku {{.CommandPrefix}}:info
the information can be read by machine, one json object per service:
dokku {{.CommandPrefix}}:info lollipop --format json
you can also retrieve a specific piece of service info via a flag, which prints it on its own:
dokku {{.CommandPrefix}}:info lollipop --dsn
dokku {{.CommandPrefix}}:info lollipop --status
dokku {{.CommandPrefix}}:info lollipop --initial-network
> NOTE: a flag cannot be combined with --format, and only one may be given
the properties {{.CommandPrefix}}:set writes are reported under the names it takes, so a value read here can be written back:
dokku {{.CommandPrefix}}:set lollipop initial-network my-network`
}

// Group is the readme usage section the command is documented under
func (c *InfoCommand) Group() string {
	return definition.GroupBasicUsage
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *LinkCommand) Description() string {
	return `link the {{.Title}} service to the app`
}

// Usage returns the argument sketch rendered after the command name
func (c *LinkCommand) Usage() string {
	return `<service> [<app>] [--link-flags...]`
}

// Documentation returns the long form documentation for the command
func (c *LinkCommand) Documentation() string {
	return `a {{.CommandPrefix}} service can be linked to a container.
this will use native docker links via the docker-options plugin.
here we link it to our 'playground' app.
> NOTE: this will restart your app
dokku {{.CommandPrefix}}:link lollipop playground
the following environment variables will be set automatically by docker
(not on the app itself, so they won’t be listed when calling dokku config):

    DOKKU_{{.PluginVariable}}_LOLLIPOP_NAME=/lollipop/DATABASE
    DOKKU_{{.PluginVariable}}_LOLLIPOP_PORT=tcp://172.17.0.1:{{.Port}}
    DOKKU_{{.PluginVariable}}_LOLLIPOP_PORT_{{.Port}}_TCP=tcp://172.17.0.1:{{.Port}}
    DOKKU_{{.PluginVariable}}_LOLLIPOP_PORT_{{.Port}}_TCP_PROTO=tcp
    DOKKU_{{.PluginVariable}}_LOLLIPOP_PORT_{{.Port}}_TCP_PORT={{.Port}}
    DOKKU_{{.PluginVariable}}_LOLLIPOP_PORT_{{.Port}}_TCP_ADDR=172.17.0.1

the following will be set on the linked application by default:

    {{.DefaultAlias}}_URL={{.Scheme}}://:SOME_PASSWORD@dokku-{{.CommandPrefix}}-lollipop:{{.Port}}

the host exposed here only works internally in docker containers.
if you want your container to be reachable from outside, you should
use the 'expose' subcommand. another service can be linked to your app:
dokku {{.CommandPrefix}}:link other_service playground
it is possible to change the protocol for {{.DefaultAlias}}_URL by setting the
environment variable {{.PluginVariable}}_DATABASE_SCHEME on the app. doing so will
after linking will cause the plugin to think the service is not
linked, and we advise you to unlink before proceeding.
dokku config:set playground {{.PluginVariable}}_DATABASE_SCHEME={{.Scheme}}2
dokku {{.CommandPrefix}}:link lollipop playground
this will cause {{.DefaultAlias}}_URL to be set as:

    {{.Scheme}}2://:SOME_PASSWORD@dokku-{{.CommandPrefix}}-lollipop:{{.Port}}`
}

// Group is the readme usage section the command is documented under
func (c *LinkCommand) Group() string {
	return definition.GroupBasicUsage
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *LinkedCommand) Description() string {
	return `check if the {{.Title}} service is linked to an app`
}

// Usage returns the argument sketch rendered after the command name
func (c *LinkedCommand) Usage() string {
	return `<service> [<app>]`
}

// Documentation returns the long form documentation for the command
func (c *LinkedCommand) Documentation() string {
	return `here we check if the lollipop {{.CommandPrefix}} service is linked to the 'playground' app.
dokku {{.CommandPrefix}}:linked lollipop playground`
}

// Group is the readme usage section the command is documented under
func (c *LinkedCommand) Group() string {
	return definition.GroupServiceAutomation
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *LinksCommand) Description() string {
	return `list all apps linked to the {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *LinksCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *LinksCommand) Documentation() string {
	return `list all apps linked to the 'lollipop' {{.CommandPrefix}} service.
dokku {{.CommandPrefix}}:links lollipop
renaming an app moves its link onto the new name, and cloning an app links the
clone as well as the original.`
}

// Group is the readme usage section the command is documented under
func (c *LinksCommand) Group() string {
	return definition.GroupServiceAutomation
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *ListCommand) Description() string {
	return `list all {{.Title}} services`
}

// Usage returns the argument sketch rendered after the command name
func (c *ListCommand) Usage() string {
	return ``
}

// Documentation returns the long form documentation for the command
func (c *ListCommand) Documentation() string {
	return `list all services
dokku {{.CommandPrefix}}:list`
}

// Group is the readme usage section the command is documented under
func (c *ListCommand) Group() string {
	return definition.GroupBasicUsage
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *LogsCommand) Description() string {
	return `print the most recent log(s) for this service`
}

// Usage returns the argument sketch rendered after the command name
func (c *LogsCommand) Usage() string {
	return `<service> [-t|--tail [<tail-num>]]`
}

// Documentation returns the long form documentation for the command
func (c *LogsCommand) Documentation() string {
	return `you can tail logs for a particular service:
dokku {{.CommandPrefix}}:logs lollipop
by default, logs will not be tailed, but you can do this with the --tail flag:
dokku {{.CommandPrefix}}:logs lollipop --tail
by default the last 100 lines are shown, but a different count can be specified
dokku {{.CommandPrefix}}:logs lollipop --tail=5`
}

// Group is the readme usage section the command is documented under
func (c *LogsCommand) Group() string {
	return definition.GroupBasicUsage
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *PauseCommand) Description() string {
	return `pause a running {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *PauseCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *PauseCommand) Documentation() string {
	return `pause the running container for the service
dokku {{.CommandPrefix}}:pause lollipop`
}

// Group is the readme usage section the command is documented under
func (c *PauseCommand) Group() string {
	return definition.GroupServiceLifecycle
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *PromoteCommand) Description() string {
	return `promote service <service> as {{.DefaultAlias}}_URL in <app>`
}

// Usage returns the argument sketch rendered after the command name
func (c *PromoteCommand) Usage() string {
	return `<service> [<app>]`
}

// Documentation returns the long form documentation for the command
func (c *PromoteCommand) Documentation() string {
	return `if you have a {{.CommandPrefix}} service linked to an app and try to link another {{.CommandPrefix}} service
another link environment variable will be generated automatically:

    DOKKU_{{.DefaultAlias}}_BLUE_URL={{.Scheme}}://:ANOTHER_PASSWORD@dokku-{{.CommandPrefix}}-other-service:{{.Port}}/other_service

you can promote the new service to be the primary one
> NOTE: this will restart your app
dokku {{.CommandPrefix}}:promote other_service playground
this will replace {{.DefaultAlias}}_URL with the url from other_service and generate
another environment variable to hold the previous value if necessary.
you could end up with the following for example:

    {{.DefaultAlias}}_URL={{.Scheme}}://:ANOTHER_PASSWORD@dokku-{{.CommandPrefix}}-other-service:{{.Port}}/other_service
    DOKKU_{{.DefaultAlias}}_BLUE_URL={{.Scheme}}://:ANOTHER_PASSWORD@dokku-{{.CommandPrefix}}-other-service:{{.Port}}/other_service
    DOKKU_{{.DefaultAlias}}_SILVER_URL={{.Scheme}}://:SOME_PASSWORD@dokku-{{.CommandPrefix}}-lollipop:{{.Port}}/lollipop`
}

// Group is the readme usage section the command is documented under
func (c *PromoteCommand) Group() string {
	return definition.GroupServiceLifecycle
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *RestartCommand) Description() string {
	return `graceful shutdown and restart of the {{.Title}} service container`
}

// Usage returns the argument sketch rendered after the command name
func (c *RestartCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *RestartCommand) Documentation() string {
	return `restart the service
dokku {{.CommandPrefix}}:restart lollipop`
}

// Group is the readme usage section the command is documented under
func (c *RestartCommand) Group() string {
	return definition.GroupServiceLifecycle
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *SetCommand) Description() string {
	return `set or clear a property for a service`
}

// Usage returns the argument sketch rendered after the command name
func (c *SetCommand) Usage() string {
	return `<service> <key> <value>`
}

// Documentation returns the long form documentation for the command
func (c *SetCommand) Documentation() string {
	return `set the network to attach after the service container is started
dokku {{.CommandPrefix}}:set lollipop post-create-network custom-network
set multiple networks
dokku {{.CommandPrefix}}:set lollipop post-create-network custom-network,other-network
unset the post-create-network value
dokku {{.CommandPrefix}}:set lollipop post-create-network
set the keyserver a public key for backup encryption is fetched from
dokku {{.CommandPrefix}}:set lollipop backup-keyserver hkp://keys.example.com
cap the container log at a size of your own rather than the one it inherits
dokku {{.CommandPrefix}}:set lollipop log-opt max-size=20m,max-file=3
keep the log unbounded, which is what a service had before there was anything to say here
dokku {{.CommandPrefix}}:set lollipop log-opt max-size=unlimited
send the container log somewhere other than the daemon's own driver
dokku {{.CommandPrefix}}:set lollipop log-driver journald
restart the container unless it was stopped on purpose, including across a docker restart
dokku {{.CommandPrefix}}:set lollipop restart-policy unless-stopped
go back to always restarting the container
dokku {{.CommandPrefix}}:set lollipop restart-policy
> NOTE: a log setting or a restart policy reaches the container the next time one is built. {{.CommandPrefix}}:restart keeps the container it has, so use {{.CommandPrefix}}:stop and then {{.CommandPrefix}}:start on a service that is already running.`
}

// Group is the readme usage section the command is documented under
func (c *SetCommand) Group() string {
	return definition.GroupBasicUsage
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *StartCommand) Description() string {
	return `start a previously stopped {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *StartCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *StartCommand) Documentation() string {
	return `start the service
dokku {{.CommandPrefix}}:start lollipop
A service comes back on the version it was created with, or was last upgraded to, whatever version the plugin ships now. The image is fetched if the host no longer has it.
A service that has never recorded a version and has no container left to read one from cannot be placed, and is reported rather than started on a guess. Use {{.CommandPrefix}}:upgrade to say which version it should run.`
}

// Group is the readme usage section the command is documented under
func (c *StartCommand) Group() string {
	return definition.GroupServiceLifecycle
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *StopCommand) Description() string {
	return `stop a running {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *StopCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *StopCommand) Documentation() string {
	return `stop the service and removes the running container
dokku {{.CommandPrefix}}:stop lollipop`
}

// Group is the readme usage section the command is documented under
func (c *StopCommand) Group() string {
	return definition.GroupServiceLifecycle
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *UnexposeCommand) Description() string {
	return `unexpose a previously exposed {{.Title}} service`
}

// Usage returns the argument sketch rendered after the command name
func (c *UnexposeCommand) Usage() string {
	return `<service>`
}

// Documentation returns the long form documentation for the command
func (c *UnexposeCommand) Documentation() string {
	return `unexpose the service, removing access to it from the public interface (0.0.0.0)
dokku {{.CommandPrefix}}:unexpose lollipop`
}

// Group is the readme usage section the command is documented under
func (c *UnexposeCommand) Group() string {
	return definition.GroupServiceLifecycle
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *UnlinkCommand) Description() string {
	return `unlink the {{.Title}} service from the app`
}

// Usage returns the argument sketch rendered after the command name
func (c *UnlinkCommand) Usage() string {
	return `<service> [<app>] [-n|--no-restart]`
}

// Documentation returns the long form documentation for the command
func (c *UnlinkCommand) Documentation() string {
	return `you can unlink a {{.CommandPrefix}} service
> NOTE: this will restart your app and unset related environment variables
dokku {{.CommandPrefix}}:unlink lollipop playground`
}

// Group is the readme usage section the command is documented under
func (c *UnlinkCommand) Group() string {
	return definition.GroupBasicUsage
}

// Description returns the one line description of the command, in the idiom of the
// plugin rather than of the binary
func (c *UpgradeCommand) Description() string {
	return `upgrade service <service> to the specified versions`
}

// Usage returns the argument sketch rendered after the command name
func (c *UpgradeCommand) Usage() string {
	return `<service> [--upgrade-flags...]`
}

// Documentation returns the long form documentation for the command
func (c *UpgradeCommand) Documentation() string {
	return `you can upgrade an existing service to a new image or image-version
dokku {{.CommandPrefix}}:upgrade lollipop
This is the only command that changes the version a service runs. With no version named it moves to the newest the service's own major version ships, which leaves the data where it is.
dokku {{.CommandPrefix}}:upgrade lollipop --image-version 1.2.3
Moving across a major version has to be asked for by name, because it is not a tag change: the data is mounted somewhere different under the new one, and pointing the version back does not undo it.`
}

// Group is the readme usage section the command is documented under
func (c *UpgradeCommand) Group() string {
	return definition.GroupServiceLifecycle
}
