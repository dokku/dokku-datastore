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

## Definitions a plugin ships

A plugin may carry the definitions for its own datastore, in `datastore/<name>/`, one directory per definition laid out exactly as the embedded tree is. `generate` writes them, and a plugin that ships any of them supplies all of them: the embedded definitions for that datastore are replaced rather than merged, so a service pinned to an older major still has a definition to run. Every definition under one plugin must name the same datastore in its `plugin:` field.

```shell
# writes datastore/postgres-17/ and datastore/postgres-18/
dokku-datastore generate --plugin-dir . postgres
```

A definition a plugin ships is trusted exactly as far as the plugin carrying it. Installing a dokku plugin is a root action, and afterwards dokku owns the whole plugin tree as the `dokku` user while running that plugin's install script as root - so a definition sits alongside scripts that already have more reach than it does. There is nothing a definition can ask for that the plugin could not do directly, which is why a shipped definition may declare a host-mode command or install a privileged script.

The practical consequence is the ordinary one for plugins: install only plugins you trust, and treat write access to a plugin's directory as equivalent to membership of the `dokku` group.

## Datastores split by major version

A datastore whose data format changes between major versions has more than one definition - `postgres-17` and `postgres-18`, `solr-7` and `solr-8` - because the two are not interchangeable: postgres moved its data directory up one level in eighteen, so the same bind mount points at an empty directory under the other definition.

A service records which one it was created with, in a `DEFINITION` file beside its `IMAGE` and `IMAGE_VERSION`, and keeps it. A release that adds a newer definition does not move services that already exist onto it, and a service created before this file existed takes the definition its recorded image version resolves to.

```shell
# creates a service on the postgres-17 definition
dokku-datastore create postgres db --image-version 17.8

# and on postgres-18, which is the newest
dokku-datastore create postgres db --image-version 18.4
```

If a service names a definition the plugin no longer ships, commands that would run it refuse rather than quietly placing it on another one, since that is the failure the pin exists to prevent. The service stays inspectable and can still be destroyed, so it can be reported on and cleaned up; reinstalling a plugin that carries the definition makes it runnable again.

`upgrade` across a major version moves the service onto the other definition, which moves where its data is mounted along with it. That is the upgrade a major version asks for rather than something to work around, but it is not a tag change and it is not reversible by pointing the version back. A bare `upgrade` never crosses one: with no version named it moves to the newest tag the service's own major version ships, and leaves the data where it is.

## The version a service runs

A service records the image it runs in `IMAGE` and `IMAGE_VERSION` beside its data, and that record is what it is placed by every time its container has to be made again. A release that ships a newer image does not move a service that already exists onto it - only `upgrade` changes the version a service runs.

```shell
# comes back on the version it was created with, not on whatever is newest
dokku-datastore stop redis lollipop
dokku-datastore start redis lollipop
```

`stop` removes the container, so the record is the only thing left that knows the version. A service that never wrote one - because it predates the file, or lost it - has it recovered from its own container, at the moment a plugin is installed, before a container is removed, and before one is started. The image is fetched if the host no longer has it, so a service pinned to an old tag can always come back on that tag rather than being pushed into an upgrade to run at all.

Where there is neither a record nor a container to recover one from, there is no version to respect, and `start` says so rather than choosing one:

```shell
dokku-datastore upgrade redis lollipop --image-version 8.9.0
```

## The images a plugin needs

A service's own image is not the only one a datastore plugin runs. Four more are pinned by the plugin itself - an ambassador to publish a service's ports, a probe to wait until it answers, busybox to take its data back off it, and the tool that ships a dump to s3 - and a definition may name one of its own on a command, which is how couchdb dumps with a tool its image does not have.

Installing a plugin fetches all of them, and that is the only time they were ever fetched. Every one is now checked again at the moment it is about to be run, so a host that has been pruned since gets it back rather than a command failing on an image nothing put there.

```shell
# comes back even on a host that has been emptied of images
docker system prune --all
dokku-datastore start redis lollipop
```

`<PLUGIN>_DISABLE_PULL=true` turns fetching off, and now turns it off for all of them rather than for the service image alone - docker used to pull the rest regardless of what it had been told. A command that needs an image it may not fetch says which `docker image pull` would give it one, and stops before it changes anything: an expose that cannot get the ambassador leaves the service unexposed, and a destroy that cannot get busybox leaves the service whole.

## Plugin documentation

`trigger-help` and `readme` render the documentation a dokku datastore plugin ships, so that the plugin help and its readme cannot drift apart. Both read the description, argument sketch, long form prose and readme section that every command declares, alongside the arguments and flags the command already accepts.

```shell
# the help a plugin's commands script is asked for
dokku-datastore trigger-help redis redis:help
dokku-datastore trigger-help redis redis:help create

# the plugin readme, generated from the plugin checkout in the working directory
dokku-datastore readme redis
```
