# dokku-datastore

A re-implementation of the datastore codebases in golang.

## Building

```shell
# substitute the version number as desired
go build -ldflags "-X main.Version=0.1.0" .
```

## Testing

The unit tests need nothing but go. The integration tests are written in [bats](https://github.com/bats-core/bats-core) and run a definition's services against a real docker daemon, with no dokku installed. They load [bats-support](https://github.com/bats-core/bats-support) and [bats-assert](https://github.com/bats-core/bats-assert) from `BATS_LIB_PATH`, and are run one definition at a time.

```shell
go test ./...

# the binary the integration tests run
go build -o dokku-datastore .

# one definition through the backend the host defaults to
DEFINITION=redis bats tests/definition

# or through the compose backend
DEFINITION=redis DOKKU_DATASTORE_BACKEND=compose bats tests/definition

# the same definition through both backends, compared as docker sees it
DEFINITION=redis bats tests/backends.bats
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
    backup-schedule-cat                   Prints the crontab line of a service's scheduled backup
    backup-set-encryption                 Encrypts future backups of a service with a passphrase
    backup-set-public-key-encryption      Encrypts future backups of a service with a gpg public key
    backup-unschedule                     Removes the backup schedule for a service
    backup-unset-encryption               Removes the backup passphrase for a service
    backup-unset-public-key-encryption    Removes the backup public key for a service
    clone                                 Clones a service onto a new one
    connect                               Connects to a service with its native client
    create                                Creates a new datastore service
    destroy                               Destroys a datastore service
    enter                                 Enters a service or runs a command in it
    exists                                Checks if a service exists
    export                                Exports a service's data to stdout or a file
    expose                                Exposes a service
    import                                Imports data into a service from stdin or a file
    info                                  Gets information about a service
    link                                  Links a service to an app
    linked                                Checks if a service is linked to an app
    links                                 Lists all apps that are linked to a given service
    list                                  Lists all services of a given datastore type
    logs                                  Gets the logs of a service
    mount                                 Mounts a host path or docker volume into a service
    pause                                 Pauses a service
    promote                               Promotes a linked service to the default config variable for an app
    readme                                Writes a datastore plugin's readme to stdout
    reexpose                              Applies a service's expose settings to its exposed ports
    restart                               Restarts a service
    set                                   Sets or clears a property for a service
    start                                 Starts a service
    stop                                  Stops a service and removes the container
    trigger-cron-entries                  Lists the scheduled backups dokku writes into its crontab
    trigger-help                          Prints the help a dokku plugin's commands script is asked for
    trigger-install                       Prepares the host for a datastore plugin
    trigger-post-app-clone-setup          Copies an app's service links onto its clone
    trigger-post-app-rename-setup         Carries an app's service links across a rename
    trigger-pre-build                     Starts the services an app is linked to before it is built
    trigger-pre-delete                    Unlinks an app from every service before it is deleted
    trigger-pre-release-builder           Starts the services an app is linked to before it is released
    trigger-pre-restore                   Starts every service linked to an app before apps are restored
    trigger-pre-start                     Starts the services an app is linked to before it starts
    trigger-service-list                  Lists the services other dokku plugins can see
    unexpose                              Unexposes a service
    unlink                                Unlinks a service from an app
    unmount                               Removes one or all mounts from a service
    upgrade                               Upgrades a service to a different image version
    version                               Return the version of the binary
```

## Definitions a plugin ships

A plugin may carry the definitions for its own datastore, in `datastore/<name>/`, one directory per definition laid out exactly as the embedded tree is. `generate` writes them, and a plugin that ships any of them supplies all of them: the embedded definitions for that datastore are replaced rather than merged, so a service pinned to an older major still has a definition to run. Every definition under one plugin must name the same datastore in its `plugin:` field.

`generate` also writes the file dokku runs for each trigger at the plugin root: the ones this binary implements for every datastore, such as `pre-start`, `pre-build` and `cron-entries`, and the ones a definition declares for itself, such as solr's `post-extract`. A trigger this binary starts implementing reaches a plugin the next time it is regenerated. `install` and `update` are not written, as a plugin's own files for those do more than dispatch, and a definition may not declare a trigger under the name of one this binary implements.

It writes the script dokku runs for each of the plugin's commands in `subcommands/` too: every command this binary implements, such as `create`, `expose` and `enter`, and every one a definition declares for itself, such as mongo's `connect-admin`. These are the scripts the plugins used to keep by hand, so a command this binary starts implementing reaches a plugin the next time it is regenerated, as a trigger does. Every datastore gets a script for every command, as the plugins had, and one its datastore does not implement exits the way dokku expects of a command a plugin does not handle. The `enter` script passes the command to run in the container after a `--`, so a flag of its own, such as `mariabackup --backup`, is not read as one of the binary's. A definition may not declare a command under the name of one this binary implements, which `generate` refuses rather than writing one over the other.

```shell
# writes datastore/postgres-17/, datastore/postgres-18/, one directory per
# flavor and major such as datastore/postgres-pgvector-pg17/, and subcommands/
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

The definition can also be named outright with `--definition`, or with `<VARIABLE>_DEFINITION` - `POSTGRES_DEFINITION` for postgres - when the flag is not given. It is for an image whose tags do not carry the major version: `--image myorg/postgres --image-version custom-3` resolves to nothing in particular and so lands on the newest definition, which for a build of postgres 17 is the wrong data directory. The named definition supplies the image and version it ships, and `--image` and `--image-version` are laid over them. A definition that belongs to another datastore is refused.

```shell
# creates a service on the postgres-17 definition, at the image and version it pins
dokku-datastore create postgres db --definition postgres-17

# and on postgres-17 with an image of your own, whatever its tags say
dokku-datastore create postgres db --definition postgres-17 --image myorg/postgres --image-version custom-3
```

If a service names a definition the plugin no longer ships, commands that would run it refuse rather than quietly placing it on another one, since that is the failure the pin exists to prevent. The service stays inspectable and can still be destroyed, so it can be reported on and cleaned up; reinstalling a plugin that carries the definition makes it runnable again.

`upgrade` across a major version moves the service onto the other definition, which moves where its data is mounted along with it. That is the upgrade a major version asks for rather than something to work around, but it is not a tag change and it is not reversible by pointing the version back. A bare `upgrade` never crosses one: with no version named it moves to the newest tag the service's own major version ships, and leaves the data where it is.

`upgrade --definition` moves the service onto the named definition, with the image and version it ships unless `--image` and `--image-version` say otherwise. That moves where its data is mounted in the same way, even when the image stays the same. `<VARIABLE>_DEFINITION` is only read by `create`. `clone` places the new service on the definition the source is pinned to, rather than on the one the source's image resolves to.

## Flavors

A flavor is a datastore on an image other than its own, shipped as definitions of its own - one per major version, named `<plugin>-<flavor>-<major>` - so that it is placed on the right data directory and followed by dependabot like any other. Postgres has three:

| Definition | Image | Tags |
|---|---|---|
| `postgres-pgvector-pg17` | `pgvector/pgvector` | `*-pg17` |
| `postgres-pgvector-pg18` | `pgvector/pgvector` | `*-pg18` |
| `postgres-postgis-pg17` | `postgis/postgis` | `17-*` |
| `postgres-postgis-pg18` | `postgis/postgis` | `18-*` |
| `postgres-timescaledb-pg17` | `timescale/timescaledb` | `*-pg17` |
| `postgres-timescaledb-pg18` | `timescale/timescaledb` | `*-pg18` |

The image picks the flavor and the version picks the major within it, however that image writes its tags: pgvector and timescaledb carry the postgres major as a `-pg17` suffix after their own version, and postgis leads with it. A version that only named the major by its leading number used to place `pgvector/pgvector:pg17` on `postgres-18`, which mounts its data one directory above where postgres 17 keeps it.

```shell
# creates a service on the postgres-pgvector-pg17 definition
dokku-datastore create postgres db --image pgvector/pgvector --image-version pg17

# and on postgres-pgvector-pg18, the newest pgvector, at the version it pins
dokku-datastore create postgres db --image pgvector/pgvector
```

An image no definition ships still runs on the datastore's own definitions, as it did before flavors existed. A service pinned to one of those keeps its pin: a service created with `pgvector/pgvector:pg17` before pgvector had definitions of its own was placed on `postgres-18`, and its data is where that definition mounts it. `upgrade` only moves a service onto a flavor's definition when the image it ran and the one it is moved to resolve to different definitions, so an upgrade inside a major leaves such a service where it is, and one across a major or onto another image moves it. An image of your own built on a flavor can be kept on that flavor's definition with `--definition`, such as `--definition postgres-pgvector-pg17`.

The images are pinned in each definition's `Dockerfile`, and dependabot holds an older major inside it according to how the image writes its tags. A tag carrying the major as a suffix, such as `0.8.6-pg17`, is only ever moved to one with the same suffix, so it needs nothing more; a tag leading with the major, such as `17-3.5`, needs its semver-major updates ignored, as `postgres-17` does. `go test` checks both, and that every definition has an entry.

postgis publishes images for amd64 only, so its definitions do not run on an arm64 host. The timescaledb image is alpine rather than debian, so its postgres user is `70` rather than `999`, and it ships no `openssl`: its certificate is made in the plain postgres image of the same major, at the tag that definition pins.

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

A service may run an image other than the one its definition pins, which is how redis runs `redis/redis-stack-server`. An image a datastore ships as a [flavor](#flavors), such as `postgis/postgis`, has definitions of its own and so a version to fall back on. The version that image runs at has to be named alongside it, because a tag belongs to the repository that published it and the definition ships no tag for somebody else's: pasting its own on named `redis/redis-stack-server` at whatever version plain `redis` is on, which is a reference nobody ever built, and the command then failed saying that image was missing.

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

Both are settable at `create`, `clone` and `upgrade` as well, and both are reported by `info` as what was set rather than as what was inherited. A `clone` passed neither takes the source's. A change reaches a container the next time one is built: `restart` keeps the container it has, so a service already running takes a `stop` and then a `start`.

## Container restart policy

A datastore container is made with a restart policy of `always`, which is the right default for a datastore an app depends on but was also the only value there was. Changing it by hand with `docker container update --restart` lasted only until the container was next built. A service may now name a policy of its own through the `restart-policy` property, the same name `dokku ps:set` uses for an app.

```shell
# docker leaves the container down once it is stopped on purpose
dokku redis:set lollipop restart-policy unless-stopped

# and back to always
dokku redis:set lollipop restart-policy
```

The accepted values are docker's own - `no`, `always`, `unless-stopped`, `on-failure` and `on-failure:<max-retries>` - and anything else is refused before it is written, since docker refuses to make a container with it. The ambassador an exposed service runs takes the same policy as the service.

It is settable at `create`, `clone` and `upgrade` as well, with `--restart`, the flag `docker container create` takes. A `clone` not passed it takes the source's. `info` reports what was set, empty when nothing was, which means `always`. A change reaches a container the next time one is built, the same as the log settings: `restart` keeps the container it has, so a service already running takes a `stop` and then a `start`.

```shell
dokku-datastore create redis lollipop --restart on-failure:5
```

A definition still cannot set `restart:` itself. The policy belongs to the service rather than to the datastore it runs.

## Readiness wait timeout

A service is waited on with the `dokku/wait` probe until it answers on its port, after `create`, `clone`, `start`, `restart`, `upgrade` and `expose` and before an app's deploy starts it. The probe gave up after its own default of 30 seconds unless the definition said otherwise, and only elasticsearch did, at 60. On a slow host, or with an image that does more on its first boot, `create` failed with `ERROR: unable to connect` before the datastore was up, and nothing could raise the limit.

A service may now name a timeout of its own, in seconds, through the `wait-timeout` property.

```shell
# wait up to two minutes for the service to answer
dokku redis:set lollipop wait-timeout 120

# and back to the host's or the datastore's default
dokku redis:set lollipop wait-timeout
```

It is settable at `create`, `clone` and `upgrade` as well, with `--wait-timeout`, and `create` itself is waited on for that long. A `clone` not passed it takes the source's. A host that is slow for every service of a datastore sets `<VARIABLE>_WAIT_TIMEOUT` instead, `REDIS_WAIT_TIMEOUT` for redis.

```shell
dokku-datastore create postgres lollipop --wait-timeout 120
```

The service's own setting is used first, then the environment variable, then the definition's `x-dokku.wait_timeout`, and otherwise the probe's default. The value must be a whole number of seconds greater than zero, and anything else is refused, including a malformed environment variable. `info` reports what the service set, empty when nothing was. Unlike the container settings above, a change takes effect the next time the service is waited on, with no rebuild. The wait header names the timeout when one is in effect.

## Container memory limit

A service's memory limit, in megabytes, is settable at `create`, `clone` and `upgrade` with `--memory`, and `0` means no limit. An `upgrade` not passed it keeps the limit the service has, and a `clone` not passed it takes the source's. `info --memory` reports it.

```shell
dokku-datastore upgrade redis lollipop --memory 512
```

## Mounted host paths and volumes

`--config-options` is handed to the process a service container runs, not to docker, so a docker flag passed through it reaches the datastore's own command line. A `--volume` given that way broke the container's entrypoint and left the service unable to start, and there was no other way to mount anything into a service. A service may now be given mounts of its own with `mount` and `unmount`, which take what `dokku storage:mount` and `dokku storage:unmount` take for a host path or a docker volume.

```shell
# a host directory, read only, inside a directory the definition already mounts
dokku elasticsearch:mount lollipop /srv/hunspell:/usr/share/elasticsearch/config/hunspell:ro

# the same, with flags rather than options
dokku elasticsearch:mount lollipop /srv/hunspell:/usr/share/elasticsearch/config/hunspell --volume-readonly

# every mount the service has, replaced in one go
dokku elasticsearch:mount --replace lollipop /srv/hunspell:/opt/hunspell:ro my-volume:/opt/extra

# one mount removed, or all of them
dokku elasticsearch:unmount lollipop /srv/hunspell:/opt/hunspell
dokku elasticsearch:unmount --all lollipop
```

A mount is `<source>:<container-dir>[:<options>]`. The source is an absolute host path or the name of a docker volume, and the container dir is an absolute path. The options are the ones `storage:mount --replace` reads: `ro` or `rw`, docker's own mount options, and `volume-subpath=<path>` and `volume-chown=<option>`. Without `--replace` one mount is given, and `--volume-readonly`, `--volume-options`, `--volume-subpath` and `--volume-chown` may say the same things as flags, though not both ways at once. Mounting the same source at the same directory again rewrites its options rather than being refused.

Some mounts are refused before anything is written, where docker would only find out when the container is made:

- a host path that does not exist, which docker would create empty and owned by root, leaving the service to start on that
- a directory the definition already mounts something at, since docker refuses to mount two things at one path. A directory inside one of them is fine, which is how a file is added to a directory a datastore reads from
- a mount option docker's `-v` does not take, such as `noexec`, which `storage:mount` stores as it is given

On a docker-in-docker install the host path is one dockerd resolves rather than one this process can see, so whether it exists is not checked there.

A subpath mounts a directory within the source rather than the source itself. For a host path it is joined onto the source, which is then the path that has to exist. A docker volume can only be mounted from a subpath through docker's `--mount`, or compose's long volume syntax, which needs Docker Engine 26.0 (api 1.45) or newer: an older daemon is refused before anything is written, and so is any mount option but `nocopy`, since the others belong to bind mounts. Docker requires the subpath to already exist inside the volume.

```shell
dokku elasticsearch:mount lollipop my-volume:/opt/extra:volume-subpath=uploads
```

A chown hands the mounted directory, and everything in it, to a user before the container is made, each time one is: `herokuish` (32767), `heroku` (1000), `paketo` (2000), `root` (0) or a uid, and `false` for none, as `storage:mount` names them. It runs as root in a throwaway busybox container, so the dokku user needs no grant. It is only taken for a host path inside the service's own directory, since anything else belongs to somebody other than the service, so it is refused for a docker volume and for a path anywhere else.

```shell
mkdir /var/lib/dokku/services/elasticsearch/lollipop/extra
dokku elasticsearch:mount lollipop /var/lib/dokku/services/elasticsearch/lollipop/extra:/opt/extra:volume-chown=1000
```

Unlike `storage:mount` on the docker-local scheduler, which records both and applies neither, a service applies them. `--phase` and `--process-type` are not taken, since a service is one process in one container, and neither is a storage entry made with `storage:create`.

Mounts may be given at `create`, `clone` and `upgrade` as well, with `--volume`, repeated for each. A `clone` not passed it takes the source's, and `--volume ""` gives it none. `info --mounts` reports every mount with all of its options, space separated. A change reaches a container the next time one is built: `restart` keeps the container it has, so a service already running takes a `stop` and then a `start`.

```shell
dokku-datastore create elasticsearch lollipop --volume /srv/hunspell:/usr/share/elasticsearch/config/hunspell:ro
```

The mounts go into the service container only, not into the containers `connect`, `enter`, `export`, `import` and the hooks run in, nor into the ambassador an exposed service runs.

## Importing a file on the dokku host

`import` read only stdin, so a dump had to be piped in from wherever the command was run. A dump already on the dokku host could not be imported over ssh: `ssh dokku@dokku.me "postgres:import lollipop < /path/to/data.dump"` hands the whole string to dokku, which splits it into words without a shell, so the `<` and the path arrive as arguments rather than as a redirection.

`import` now takes `--file`, which names a file to read instead of stdin. The path is on the dokku host, not on the machine running ssh, and must be a regular file.

```shell
# a dump on the dokku host
dokku postgres:import lollipop --file /var/lib/dokku/data/storage/data.dump

# a dump on this machine, redirected here rather than on the dokku host
ssh dokku@dokku.me postgres:import lollipop < data.dump
```

## Exporting to a file on the dokku host

`export` wrote only to stdout, so a dump could not be written to the dokku host over ssh: `ssh dokku@dokku.me "postgres:export lollipop > /path/to/data.dump"` hands the whole string to dokku, which splits it into words without a shell, so the `>` and the path arrive as arguments and the dump comes back through the ssh session instead.

`export` now takes `--file`, which names a file to write instead of stdout. The path is on the dokku host, not on the machine running ssh, and its directory must be writable by the dokku user. That is checked before the export starts, so a path that cannot be written fails without exporting anything. A file that already exists at the path is not overwritten unless `--force` is given, and then it is only replaced once the export succeeds, so a failed export leaves the previous dump in place. The dump is written beside the path and only moved there once it is complete, so a file that appears at the path during the export is not overwritten either. The file is readable by the dokku user and group only.

```shell
# a dump written on the dokku host
dokku postgres:export lollipop --file /var/lib/dokku/data/storage/data.dump

# replacing a dump already at that path
dokku postgres:export lollipop --file /var/lib/dokku/data/storage/data.dump --force

# a dump written on this machine, redirected here rather than on the dokku host
ssh dokku@dokku.me postgres:export lollipop > data.dump
```

## Passing extra arguments to export and import

`export` and `import` ran each datastore's dump and load tools with a fixed set of arguments, so a dump could not be made any other way. A mysql table with binary columns was dumped as raw bytes, where `mysqldump --hex-blob` would have written it correctly, and there was nowhere to pass that. Scheduled backups and clones made their dumps the same way.

`export` and `import` now pass whatever follows `--` to the tool, after its own arguments. Flags before the `--`, such as `--file`, are still read as the command's own.

```shell
# a dump with binary columns written as hex
dokku mysql:export lollipop -- --hex-blob > data.dump

# a dump with rows larger than the client allows by default
dokku mysql:import lollipop --file /var/lib/dokku/data/storage/data.dump -- --max-allowed-packet=1G
```

Backups and clones are never run by hand, so a service can keep arguments for them in its `export-args` and `import-args` properties. Every export of the service uses `export-args`, including the ones `backup` and `clone` make, and every import into it uses `import-args`, including the one a `clone` makes. Arguments given after `--` replace the property for that run rather than adding to it, and a bare `--` leaves the property in place. A clone is given the source's properties. The value is given after `--`, since `set` would otherwise read its leading dash as one of its own flags. It is split the way a shell would split it, so an argument with a space in it is quoted, and a variable or command substitution in it is refused rather than expanded. `info --export-args` and `info --import-args` report them.

```shell
dokku mysql:set lollipop export-args -- "--hex-blob --routines"
dokku mysql:set lollipop import-args -- "--max-allowed-packet=1G"

# back to the datastore's own arguments
dokku mysql:set lollipop export-args
```

A datastore declares that its tool takes them with `extra_args: true` on its `export` or `import` command. mysql, mariadb, postgres and mongo do. redis, couchdb and clickhouse dump and load with scripts that never read their arguments, so they refuse extra arguments, and the properties, rather than make a dump without them.

## Clickhouse export and import

The bash clickhouse plugin had no `export` or `import`, so a clickhouse service could not be cloned or backed up either. clickhouse now exports and imports, and with that gains `clone` and the `backup` commands.

The dump is clickhouse's own `BACKUP` archive, a zip file that holds the service's database, its tables, views and dictionaries included. It stores the database under a fixed name, so an export from one service imports into any other, but only archives written by `export` can be imported. An import restores the archive beside the live database and swaps it in only once the restore succeeds, so an archive that fails to restore leaves the data as it was.

```shell
dokku clickhouse:export lollipop > lollipop.zip
dokku clickhouse:import lollipop-2 < lollipop.zip
```

The server writes and reads the archive in the backup directory its config allows, which the image's own `config.xml` puts under the data directory. A config that allows none, such as one from an older image, is given one through `config.d/dokku-backups.xml` on the first export or import.

## Connecting without a terminal

`ssh dokku@dokku.me mysql:connect lollipop` gives `connect` no terminal, since ssh only allocates one when asked with `-t`. Without one, the client shows no prompt and reads statements from stdin, and `mysql` also held every result until it exited. A statement typed into such a session ran, but nothing came back until the session ended, which looked like a blank screen that stopped responding.

`mysql` and `mariadb` are now told to print each result as it runs, so a session without a terminal answers every statement as it is sent, and statements piped in behave as they did. The other clients already did this, except `clickhouse client`, which reads all of stdin before it runs anything and has no option that changes that. For a prompt, ask ssh for a terminal.

```shell
# an interactive session, with a prompt
ssh -t dokku@dokku.me mysql:connect lollipop

# statements piped in, each result printed as it runs
echo 'SHOW TABLES;' | ssh dokku@dokku.me mysql:connect lollipop
```

## Backup credentials in info

`info` reports every setting `backup-auth` and `backup-set-encryption` store, so tooling can tell whether what it would apply is already in place. The region, signature version and endpoint are reported as they are, under `--backup-default-region`, `--backup-signature-version` and `--backup-endpoint-url`. The access key id and secret, and the backup passphrase, are secrets and are never printed. Each is reported as a lowercase hex sha256 fingerprint of the stored value with surrounding whitespace trimmed, under `--backup-auth-fingerprint` and `--backup-encryption-fingerprint`, and is empty when nothing is stored. `--backup-authenticated` is only `true` when both the access key id and the secret are stored, since a backup refuses to run with either one missing. Each `backup-auth` call replaces every stored setting, so a region, signature version or endpoint omitted from the call is removed and reported as empty.

A fingerprint is compared against one computed from a copy of the values:

```shell
# the access key id and the secret, joined by a newline
printf '%s\n%s' "$AWS_ACCESS_KEY_ID" "$AWS_SECRET_ACCESS_KEY" | sha256sum

# the passphrase
printf '%s' "$PASSPHRASE" | sha256sum
```

## Backup storage class

Backups were always uploaded with the bucket's default storage class, which on AWS is `STANDARD`. A service may now name the class its backups are uploaded with through the `backup-storage-class` property, so backups that are rarely read can land on a cheaper class.

```shell
# upload backups to infrequent access storage
dokku redis:set lollipop backup-storage-class STANDARD_IA

# and back to the bucket's default
dokku redis:set lollipop backup-storage-class
```

The accepted values are the ones `aws s3 cp --storage-class` takes in the backup image - `STANDARD`, `REDUCED_REDUNDANCY`, `STANDARD_IA`, `ONEZONE_IA`, `INTELLIGENT_TIERING`, `GLACIER`, `DEEP_ARCHIVE` and `GLACIER_IR` - and anything else is refused before it is written, since the backup image would refuse it only after the service had been exported. An S3 compatible service that does not support a class may reject the upload.

It is read when a backup runs, so it applies to the next backup, scheduled ones included, without a rebuild. `info` reports it under `--backup-storage-class`, empty when nothing was set, and `clone` copies it. A backup stored as `GLACIER` or `DEEP_ARCHIVE` has to be restored in S3 before it can be downloaded and imported.

## Backups when dokku runs in a container

`backup` exported a service into a temporary directory and mounted it into the container that ships it to s3. The mount is resolved by dockerd, and when dokku is installed in docker that directory is inside the dokku container, where dockerd cannot see it. Docker mounted an empty directory in its place, and an archive holding nothing but an empty `backup` directory was uploaded and reported as a success.

The dump is now streamed into that container over stdin, so nothing is mounted and a backup ships the same dump wherever dokku runs. The uploaded archive is laid out as before, with the dump at `backup/export`, so a backup is restored the same way whichever version made it.

## Reserved service names

A service's database is named after the service, with hyphens replaced by underscores, so a service named after a database the datastore keeps for itself would hand its app that database. `create mysql mysql` made a service whose app was given MySQL's own `mysql` database. A definition lists the names its datastore keeps in `x-dokku.reserved_names`, and `create` and `clone` refuse a name that is one of them, or whose database would be, in any case.

```shell
# refused, and lists every name mysql reserves
dokku-datastore create mysql mysql

# refused too, since its database would be information_schema
dokku-datastore create mysql information-schema
```

Only the databases a datastore uses for itself are reserved. A default database that works as a service's own, such as postgres' `postgres` or clickhouse's `default`, is not. A service that already has a reserved name is left as it is, and every command but `create` and `clone` works on it as before.

## Service passwords

The passwords a definition declares under `secrets` are generated when a service is created, unless `--password` or `--root-password` gives one. Each flag sets the secret whose `env` is `SERVICE_PASSWORD` or `SERVICE_ROOT_PASSWORD`, and that environment variable is read when the flag is not given.

```shell
dokku-datastore create mysql lollipop --password <password> --root-password <root-password>
```

A datastore with no secret for a flag refuses it before anything is created, rather than starting on a generated password nobody was told about. Postgres and redis, for instance, have no root password, and memcached has no password at all.

## Cloned services

A clone was made on the source's image and given its data, but nothing else about the source carried over: every other setting came from the flags passed to `clone`, so a clone made without repeating all of them landed on the defaults rather than on what the source runs with.

A clone now starts from the source's settings - its config options, custom env, memory, shm size, initial, post-create and post-start networks, log driver, log options, restart policy, mounts, backup keyserver, backup storage class and export and import arguments. A flag passed to `clone` overrides that one setting, and a flag passed empty clears it, the same as on `upgrade`.

```shell
# the same settings as lollipop
dokku-datastore clone redis lollipop lollipop-2

# the same settings as lollipop, except these two
dokku-datastore clone redis lollipop lollipop-3 --restart no --custom-env ""
```

The `<VARIABLE>_CONFIG_OPTIONS` and `<VARIABLE>_CUSTOM_ENV` environment variables are not read by `clone`. They fill in a new service, and the source already says what its clone should have. The networks are copied as well, since a container joins a network under its own service name and a clone next to its source does not clash with it.

The password, the exposed ports and the `port-bind-address`, `port-source-range` and `expose-host` that go with them, the app links and the backup credentials, schedule and encryption are not copied. The password is generated for each service unless `--password` or `--root-password` gives one, an exposed port would clash with the source's on the host, links belong to the apps, and a copied backup schedule would ship a second set of backups to the source's bucket.

## Exposed services

An exposed service publishes its ports through a second container, the ambassador. It used to be linked to the service container and to find the service through the environment docker hands a linked container. Docker 29 stopped handing that environment over unless the daemon is run with `DOCKER_KEEP_DEPRECATED_LEGACY_LINKS_ENV_VARS=1`, so an ambassador made there restarted forever - `Failed to autodetect target host/container and port using --link environment` - and published nothing.

The ambassador is now made by [docker-port-forward](https://github.com/dokku/docker-port-forward) and is not linked to anything. It joins a network the service container is on and forwards to the service there: by container name on a network of its own, and by address on docker's default bridge, which has no names to look up. It works on docker 29 without the daemon setting, and keeps working on the versions before it.

The ambassador holds nothing of its own, so it is treated as disposable. It is kept only while it is running, fronts the container the service has now, and can still reach it, and is otherwise replaced. That includes an ambassador made by an older version of the plugin, which is replaced the next time the service is started, exposed or restarted rather than all at once when the plugin is installed. A `stop` takes it away even when the service container is already gone, and a `start` puts it back even when the service itself is already running.

A port may be published on one address rather than on every interface. A port that docker could not publish - one out of range, or on a hostname rather than an address - is refused before the service is marked as exposed. Only the ambassador has moved off legacy links: `link` still links an app to the service container.

```shell
# the port the service was exposed on comes back with it
dokku redis:expose lollipop 6380
dokku redis:stop lollipop
dokku redis:start lollipop

# and a running service whose ambassador went away gets it back
dokku redis:start lollipop

# published on the loopback interface alone
dokku redis:unexpose lollipop
dokku redis:expose lollipop 127.0.0.1:6380
```

### Limiting where and to whom a service is exposed

Two properties limit an exposed service without naming an address in every port:

- `port-bind-address` is the address a port with no address of its own is published on, rather than every interface. A port exposed on an address of its own, such as `127.0.0.1:6380`, keeps it. It has to be an IPv4 or IPv6 address, written without brackets. Random ports, from an `expose` with no ports, are picked from the ones free on that address.
- `port-source-range` is the only range of client addresses the exposed ports accept connections from, as one IP address or CIDR. A bare address is a range of one. The ambassador refuses a client outside it before anything reaches the service.

`port-bind-address` and `port-source-range` used to be named `expose-address` and `expose-source-range`. They were renamed because they only apply to the exposed ports, not to the address clients connect to. A value set under an old name is not read, so it has to be set again under the new one.

Both are read when the ambassador is made, and the ambassador records the ones it was made with. `reexpose` replaces it on the ports the service is already exposed on, which is how a change reaches a running service without restarting it. A `start`, `restart` or `upgrade` replaces an ambassador made with other settings as well, and a service that is not exposed takes them up on its next `expose`. `unexpose` leaves both set.

```shell
# only clients on the private network, on the private interface
dokku redis:set lollipop port-bind-address 10.0.0.5
dokku redis:set lollipop port-source-range 10.0.0.0/8
dokku redis:reexpose lollipop
dokku redis:info lollipop --exposed-ports

# every client again
dokku redis:set lollipop port-source-range
dokku redis:reexpose lollipop
```

The range is enforced by socat in the ambassador, which brings its limits with it:

- Only one range can be given, since socat honors a single range for each port it listens on. Networks that do not share a prefix have to be restricted with a host firewall instead, such as rules in docker's `DOCKER-USER` chain.
- The address checked is the one the connection reaches the ambassador from. A client on another host that docker delivers by NAT keeps its own address, but a connection to the exposed port on the loopback interface, or an IPv6 connection to an ambassador without IPv6, goes through docker's userland proxy and arrives from the network's gateway, such as `172.17.0.1`. With a range that leaves the gateway out, connecting to `127.0.0.1:<port>` from the dokku host itself is refused.
- An IPv6 range makes the ambassador listen on IPv6 alone, so on a network without IPv6 it refuses every connection.

### Connecting to an exposed service from outside the host

`info` reports the `dsn` a linked app is handed, which names the service container and the port inside it. Neither can be reached from anywhere but the dokku host's docker networks. `exposed-dsn` is the same dsn with the exposed ports in place of the container's and a public host in place of the container's name, so a client elsewhere can connect with it. It carries the same credentials as `dsn`.

The host is picked in this order:

1. The service's `expose-host` property, a hostname or an IP address written without brackets.
2. The first global domain, as set with `dokku domains:set-global`.

When neither gives a host, or the service is not exposed, `exposed-dsn` is empty. The `port-bind-address`, and an address given with a port such as `127.0.0.1:6380`, are never used as the host. They are where the ports are bound, which for every interface names nothing and for a private or loopback address is not where a client elsewhere connects. `expose-host` only changes what is reported, not where the ports are bound.

```shell
dokku postgres:expose lollipop
pgcli "$(ssh dokku@dokku.me postgres:info lollipop --exposed-dsn)"

# clients reach the server by a name other than its global domain
dokku postgres:set lollipop expose-host db.example.com
dokku postgres:info lollipop --exposed-dsn

# back to the global domain
dokku postgres:set lollipop expose-host
```

## Starting linked services before an app

A service an app is linked to is started before the app needs it, rather than the app failing on a container link to something that does not exist. That happens at every point dokku offers a trigger for:

- `ps:start` and `ps:restore` start each app through `pre-start`.
- A deploy, including `ps:rebuild`, builds through `pre-build` and releases through `pre-release-builder`.
- `ps:restore` fires `pre-restore` once, before it restores any app, and every service linked to an app is started there, one at a time, so apps restored in parallel find their services already running.

A service with no container is made again on the version it recorded, and its image is fetched if the host does not have it. That covers a host restored from a backup, which has the services' data and records but none of their containers or images:

```shell
# brings every linked service back before the app it is linked to builds
dokku ps:rebuild --all
```

A service that cannot be started stops the app from being started, built or released, and the error names the service and the app. `pre-restore` only warns, so one service that cannot be started does not stop every other app from being restored; the `pre-start` of each app using it is still where that app is stopped.

Dokku has no trigger before it runs the containers of an image it has already built and released, so `ps:restart` of an app with a deployed image, `ps:scale`, and the restart after `config:set` do not start linked services.

## Linked apps

Whether a service was linked to an app was decided in two places. `destroy`, `linked` and `links` read the service's list of linked apps, while `unlink` looked for a config variable on the app holding the service's url. An app whose `DATABASE_URL` was changed to point at another datastore was linked according to the first and not according to the second: `destroy` refused with `Cannot delete linked service`, `unlink` failed with `Not linked to app`, and yet after that failed `unlink` the `destroy` went through.

The list of linked apps now decides for every command. An app on it is unlinked the same way whether or not its config still points at the service. The variable it holds is left alone when it names something else, nothing is unset, the app is not restarted, and a warning says so. `unlink` still fails with `Not linked to app` when the app is neither on the list nor has the url, and then changes nothing.

`destroy` names the apps a service is still linked to, rather than only refusing.

```shell
dokku postgres:link lollipop playground
dokku config:set playground DATABASE_URL=postgres://elsewhere:5432/db

# refuses, and names playground
dokku postgres:destroy lollipop

# unlinks, warning that DATABASE_URL is left as it is
dokku postgres:unlink lollipop playground
dokku postgres:destroy lollipop
```

`link` goes by the same list. An app whose config already held the service's url, set by hand rather than by `link`, was refused with `Already linked as DATABASE_URL`, and was left without its container link and off the list, so it could not reach the service and `destroy` did not know it was in use. Such an app is now added to the list and given its container link. Its config is left as it is, so `--alias`, `--env-var` and `--querystring` do not apply and the app is not restarted, and a warning says it has no container link until it is. An app already on the list is still refused, including one whose url was repointed, which used to be linked a second time under a `DOKKU_POSTGRES_AQUA_URL`-style alias.

The bash plugins also took a key that merely contained the alias, such as `EXTERNAL_DATABASE_URL`, for the alias itself, and linked the app under a `DOKKU_POSTGRES_AQUA_URL`-style alias instead, or refused `--alias DATABASE` as already in use. Only a key named exactly `DATABASE_URL` counts.

```shell
dokku config:set playground DATABASE_URL=postgres://postgres:password@dokku-postgres-lollipop:5432/lollipop

# adds playground to the list and gives it the container link,
# warning that the app is not restarted
dokku postgres:link lollipop playground
dokku ps:restart playground
```

### The variable a link sets

`link` always appended `_URL` to the variable it set, so an app that reads its url from a name like `MB_DB_CONNECTION_URI` had to have it copied over by hand with `config:set`. `--env-var` names the variable in full, and cannot be combined with `--alias`, which is still suffixed with `_URL`. A name already set on the app is refused.

The variable holding the url used to be found only by the url it held, so once the scheme on it changed, through `POSTGRES_DATABASE_SCHEME` or by hand, `unlink` no longer found it and left it in place. `link` now records the variables it sets on each app in the service's `link-config-keys` property, and `unlink` and `promote` find a recorded variable as long as it still names the service's credentials, host, port and database, whatever its scheme or querystring. A variable pointed at another datastore is still left alone. A link made by an earlier version has nothing recorded and is found by its exact url, as before, until `link` or `promote` next runs for it and records it. A `link-config-keys` property that cannot be parsed is read as recording nothing, and the next write replaces it with a warning, so the other apps it recorded fall back to their exact url in the same way. A cloned service starts with no record, as it starts with no linked apps.

```shell
# sets MB_DB_CONNECTION_URI rather than DATABASE_URL
dokku postgres:link lollipop metabase --env-var MB_DB_CONNECTION_URI

# still unsets MB_DB_CONNECTION_URI, though the scheme on it changed
dokku config:set metabase MB_DB_CONNECTION_URI=postgresql://postgres:password@dokku-postgres-lollipop:5432/lollipop
dokku postgres:unlink lollipop metabase
```

## Hiding services from users

A plugin may hide services from some users by implementing the `user-auth-service` trigger, the one the bash plugins fired. It is handed the ssh user, the name of the ssh key they connected with, the datastore's command prefix and every service, and prints the services that user may see, one per line on stdout. Anything it prints on stderr is ignored, and a service it names that was not asked about is dropped. `list`, `info` with no service named, `app-links` and the triggers that list services all see only what it prints.

```shell
#!/usr/bin/env bash
# hides any service whose name starts with admin- from everybody but root
main() {
  declare SSH_USER="$1" SSH_NAME="$2" COMMAND_PREFIX="$3"
  for service in "${@:4}"; do
    [[ "$service" == admin-* ]] && [[ "$SSH_USER" != "root" ]] && [[ "$SSH_NAME" != "root" ]] && continue
    echo "$service"
  done
}

main "$@"
```

The user is `SSH_USER`, or `USER` without one, and the key's name is `SSH_NAME`, or the `NAME` sshcommand sets for the key, or `default`. Nothing is asked when no plugin implements it, and one in dokku's own `20_events` plugin is not counted, as the bash plugins did not count it.

## Quiet, trace and json output

Every command takes `--quiet`, `--trace` and `--format`, and dokku's own `--quiet` and `--trace` reach it as `DOKKU_QUIET_OUTPUT` and `DOKKU_TRACE`.

`--quiet` leaves out what a command says it is doing - its headers and progress messages - and keeps its errors, its warnings and whatever it was asked to print. `--trace` echoes every command run on the way, to stderr. Both are handed on as `DOKKU_QUIET_OUTPUT=1` and `DOKKU_TRACE=1`, so the triggers and `dokku` commands a command runs are quiet or traced the way the bash plugins' were.

`--format json` prints a command's result as json on stdout: `create`, `info`, `list`, `links`, `app-links`, `backup-schedule-cat`, `trigger-cron-entries` and `trigger-service-list` have one. Everything else - progress, warnings and errors as json objects, and whatever a trigger or a docker command prints on the way - goes to stderr, so stdout can be handed straight to a json reader. The text a trigger prints without it is unchanged, since dokku reads it a line at a time. `readme` and `trigger-help` only print text, and refuse `--format json`.

```shell
dokku-datastore backup-schedule-cat redis lollipop --format json
```

## Plugin documentation

`trigger-help` and `readme` render the documentation a dokku datastore plugin ships, so that the plugin help and its readme cannot drift apart. Both read the description, argument sketch, long form prose and readme section that every command declares, alongside the arguments and flags the command already accepts.

```shell
# the help a plugin's commands script is asked for
dokku-datastore trigger-help redis redis:help
dokku-datastore trigger-help redis redis:help create

# the plugin readme, generated from the plugin checkout in the working directory
dokku-datastore readme redis
```
