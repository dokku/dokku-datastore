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

## An image the plugin does not ship

A service may run an image other than the one its definition pins, which is how redis runs `redis/redis-stack-server` and postgres runs `postgis/postgis`. The version that image runs at has to be named alongside it, because a tag belongs to the repository that published it and the definition ships no tag for somebody else's: pasting its own on named `redis/redis-stack-server` at whatever version plain `redis` is on, which is a reference nobody ever built, and the command then failed saying that image was missing.

```shell
# refused, and says which image has no version rather than inventing one
dokku-datastore create redis lollipop --image redis/redis-stack-server

# and the same for an upgrade that moves a service onto one
dokku-datastore create redis lollipop --image redis/redis-stack-server --image-version 7.2.0-v10
```

A service that records only a version still takes the definition's image, because that is the image it has been running all along - it predates the `IMAGE` file rather than having moved off it. Only the other half is refused.

A reference is cut on its tag rather than on its first colon, so an image held in a private registry keeps the port that registry answers on: `registry.example.com:5000/redis:8.10` is that repository at `8.10` and not the host `registry.example.com` at a version of `5000/redis:8.10`.

`<VARIABLE>_IMAGE` and `<VARIABLE>_IMAGE_VERSION` - `REDIS_IMAGE` for redis - name the same two things for a host that would rather not pass them on every create. These are the names the plugin readme documents, and now the names `create` reads; the bash plugins derived an internal `PLUGIN_IMAGE` from them, and that pair is still read, after them, for a host carried over.

## The images a plugin needs

A service's own image is not the only one a datastore plugin runs. Four more are pinned by the plugin itself - an ambassador to publish a service's ports, a probe to wait until it answers, busybox to take its data back off it, and the tool that ships a dump to s3 - and a definition may name one of its own on a command, which is how couchdb dumps with a tool its image does not have.

Installing a plugin fetches all of them, and that is the only time they were ever fetched. Every one is now checked again at the moment it is about to be run, so a host that has been pruned since gets it back rather than a command failing on an image nothing put there.

```shell
# comes back even on a host that has been emptied of images
docker system prune --all
dokku-datastore start redis lollipop
```

`<PLUGIN>_DISABLE_PULL=true` turns fetching off, and now turns it off for all of them rather than for the service image alone - docker used to pull the rest regardless of what it had been told. A command that needs an image it may not fetch says which `docker image pull` would give it one, and stops before it changes anything: an expose that cannot get the ambassador leaves the service unexposed, and a destroy that cannot get busybox leaves the service whole.

## Container log retention

A datastore container wrote its log with nothing to say how large it was allowed to get, so on a host using docker's default `json-file` driver it grew until something else on the machine ran out of room. Dokku has capped its own app containers for years - `dokku logs:set --global max-size 20m` - and a datastore container is a container dokku makes, so it is capped by the same answer now. Where nothing has been set at all, the cap is dokku's own default of `10m`.

```shell
# what every app on the host already respects, now respected here too
dokku logs:set --global max-size 20m
```

A service may say something of its own, in docker's own vocabulary: `log-driver` is the driver its container is logged by, and `log-opt` is a comma separated list of the options that driver takes.

```shell
# a service that talks more than the rest of them
dokku redis:set lollipop log-opt max-size=100m,max-file=3

# or one whose log belongs somewhere other than a file on this host
dokku redis:set lollipop log-driver journald
```

An option a service names is passed to docker as it stands. What is inherited is only the one it did not name, and that is held back from a driver with no `max-size` to give - docker refuses an option a driver does not understand, and a container that cannot be made is worse than a log that grows. `max-size=unlimited` is how a service asks not to be capped, since that is dokku's word for it and docker has no value that says the same thing. It stops dokku asking for a cap rather than guaranteeing there is none: a daemon configured with log options of its own still applies them.

```shell
# back to the way every datastore container behaved before this existed
dokku redis:set lollipop log-opt max-size=unlimited
```

Both are settable at `create`, `clone` and `upgrade` as well, and both are reported by `info` as what was set rather than as what was inherited. A change reaches a container the next time one is built: `restart` keeps the container it has, so a service already running takes a `stop` and then a `start`.

## Plugin documentation

`trigger-help` and `readme` render the documentation a dokku datastore plugin ships, so that the plugin help and its readme cannot drift apart. Both read the description, argument sketch, long form prose and readme section that every command declares, alongside the arguments and flags the command already accepts.

```shell
# the help a plugin's commands script is asked for
dokku-datastore trigger-help redis redis:help
dokku-datastore trigger-help redis redis:help create

# the plugin readme, generated from the plugin checkout in the working directory
dokku-datastore readme redis
```
