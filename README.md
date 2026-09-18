# dokku-datastore

A re-implementation of the datastore codebases in golang.

## Building

```shell
# substitute the version number as desired
go build -ldflags "-X main.Version=0.1.0" .
```

## Usage

```text
Usage: dokku-datastore [--version] [--help] <command> [<args>]

Available commands are:
    app-links                             Lists all app links for a given app
    backup                                Backs a service up to an s3 bucket
    backup-auth                           Stores the credentials backups are shipped with
    backup-deauth                         Removes the stored backup credentials for a service
    backup-schedule                       Schedules a recurring backup of a service to an s3 bucket
    backup-schedule-cat                   Prints the backup cron file for a service
    backup-set-encryption                 Encrypts future backups of a service with a passphrase
    backup-set-public-key-encryption      Encrypts future backups of a service with a gpg public key
    backup-unschedule                     Removes the backup schedule for a service
    backup-unset-encryption               Removes the backup passphrase for a service
    backup-unset-public-key-encryption    Removes the backup public key for a service
    clone                                 Clones a service onto a new one
    connect                               Connects to a service with its native client
    create                                Creates a new datastore service
    destroy                               Destroys a datastore service
    enter                                 Enters a service
    exists                                Checks if a service exists
    export                                Exports a service's data to stdout
    expose                                Exposes a service
    import                                Imports data into a service from stdin
    info                                  Gets information about a service
    link                                  Links a service to an app
    linked                                Checks if a service is linked to an app
    links                                 Lists all apps that are linked to a given service
    list                                  Lists all services of a given datastore type
    logs                                  Gets the logs of a service
    pause                                 Pauses a service
    promote                               Promotes a linked service to the default config variable for an app
    readme                                Writes a datastore plugin's readme to stdout
    restart                               Restarts a service
    set                                   Sets or clears a property for a service
    start                                 Starts a service
    stop                                  Stops a service and removes the container
    trigger-help                          Prints the help a dokku plugin's commands script is asked for
    trigger-install                       Prepares the host for a datastore plugin
    trigger-post-app-clone-setup          Copies an app's service links onto its clone
    trigger-post-app-rename-setup         Carries an app's service links across a rename
    trigger-pre-delete                    Unlinks an app from every service before it is deleted
    trigger-pre-restore                   Starts the services an app is linked to before it is restored
    trigger-pre-start                     Starts the services an app is linked to before it starts
    trigger-service-list                  Lists the services other dokku plugins can see
    unexpose                              Unexposes a service
    unlink                                Unlinks a service from an app
    upgrade                               Upgrades a service to a different image version
    version                               Return the version of the binary
```

## Datastores split by major version

A datastore whose data format changes between major versions has more than one definition - `postgres-17` and `postgres-18`, `solr-7` and `solr-8` - because the two are not interchangeable: postgres moved its data directory up one level in eighteen, so the same bind mount points at an empty directory under the other definition.

A service records which one it was created with, in a `DEFINITION` file beside its `IMAGE` and `IMAGE_VERSION`, and keeps it. A release that adds a newer definition does not move services that already exist onto it, and a service created before this file existed takes the definition its recorded image version resolves to.

```shell
# creates a service on the postgres-17 definition
dokku-datastore create postgres db --image-version 17.8

# and on postgres-18, which is the newest
dokku-datastore create postgres db --image-version 18.4
```

`upgrade` across a major version moves the service onto the other definition, which moves where its data is mounted along with it. That is the upgrade a major version asks for rather than something to work around, but it is not a tag change and it is not reversible by pointing the version back.

## Plugin documentation

`trigger-help` and `readme` render the documentation a dokku datastore plugin ships, so that the plugin help and its readme cannot drift apart. Both read the description, argument sketch, long form prose and readme section that every command declares, alongside the arguments and flags the command already accepts.

```shell
# the help a plugin's commands script is asked for
dokku-datastore trigger-help redis redis:help
dokku-datastore trigger-help redis redis:help create

# the plugin readme, generated from the plugin checkout in the working directory
dokku-datastore readme redis
```
