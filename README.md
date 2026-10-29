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
DEFINITION=redis BACKEND=compose bats tests/definition

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
    backup-logs                           Shows the log of a service's scheduled backups
    backup-schedule                       Schedules a recurring backup of a service to an s3 bucket, optionally under --id
    backup-schedule-cat                   Prints the crontab line of a service's scheduled backups, or one named with --id
    backup-set-encryption                 Encrypts future backups of a service with a passphrase
    backup-set-public-key-encryption      Encrypts future backups of a service with a gpg public key
    backup-unschedule                     Removes a scheduled backup, or every one with --all
    backup-unset-encryption               Removes the backup passphrase for a service
    backup-unset-public-key-encryption    Removes the backup public key for a service
    clone                                 Clones a service onto a new one
    connect                               Connects to a service with its native client
    create                                Creates a new datastore service
    create-database                       Creates a database in a service
    create-user                           Creates a user in a service
    destroy                               Destroys a datastore service
    destroy-database                      Destroys a database created in a service
    destroy-user                          Destroys a user created in a service
    enter                                 Enters a service or runs a command in it
    exists                                Checks if a service exists
    export                                Exports a service's data to stdout or a file
    expose                                Exposes a service
    import                                Imports data into a service from stdin or a file
    info                                  Gets information about a service
    label                                 Adds docker labels to a service
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
    set-user-password                     Changes the password of a user created in a service
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
    unlabel                               Removes one or all labels from a service
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

A datastore whose data format changes between major versions has more than one definition - `postgres-14` through `postgres-18`, `solr-7` and `solr-8` - because they are not interchangeable: no postgres major reads another's cluster, and postgres moved its data directory up one level in eighteen, so the same bind mount points at an empty directory under the other definition.

A service records which one it was created with, in a `DEFINITION` file beside its `IMAGE` and `IMAGE_VERSION`, and keeps it. A release that adds a newer definition does not move services that already exist onto it, and a service created before this file existed takes the definition its recorded image version resolves to.

```shell
# creates a service on the postgres-17 definition
dokku-datastore create postgres db --image-version 17.8

# and on postgres-18, which is the newest
dokku-datastore create postgres db --image-version 18.4
```

A release that adds a definition for an older major can move a service onto it, once: before `postgres-14`, `postgres-15` and `postgres-16` existed, a service running one of those majors resolved to the newest definition, and was pinned to it the next time the plugin was installed. When the plugin is installed, a service whose recorded image is the one its pinned definition ships, at a version naming the major of another of its definitions outright, is pinned to that definition instead, with a warning naming both. Where the two definitions mount the data in different places, the service's container has to mount it where the definition it is moved onto would, which is the case for a service still running the container the bash plugin made. A service with no container, or whose container mounts the data where its pin does, keeps its pin, since its server may have made its cluster there. Nothing about the container or the data is changed.

A service pinned to the wrong definition that install leaves alone, such as one on an image of its own, is moved with `upgrade --definition <definition> --no-migrate`. That makes the container again on the named definition and leaves the data where it is, rather than carrying it across the way an upgrade onto another definition otherwise does. Nothing checks that the data is where the named definition looks for it, so naming the wrong one starts the service on an empty directory with its data still beside it. A postgres server started on a directory it did not find its cluster in makes a new one, so a service that ran under the wrong pin can have a second cluster in a `data` directory inside its own; it is left there for the operator to remove once they are satisfied nothing written to it is needed.

```shell
# put a postgres 15 service pinned to postgres-18 back on postgres-15
dokku-datastore upgrade postgres db --definition postgres-15 --no-migrate
```

The definition can also be named outright with `--definition`. It is for an image whose tags do not carry the major version: `--image myorg/postgres --image-version custom-3` resolves to nothing in particular and so lands on the newest definition, which for a build of postgres 17 is the wrong data directory. The named definition supplies the image and version it ships, and `--image` and `--image-version` are laid over them. A definition that belongs to another datastore is refused.

```shell
# creates a service on the postgres-17 definition, at the image and version it pins
dokku-datastore create postgres db --definition postgres-17

# and on postgres-17 with an image of your own, whatever its tags say
dokku-datastore create postgres db --definition postgres-17 --image myorg/postgres --image-version custom-3
```

If a service names a definition the plugin no longer ships, commands that would run it refuse rather than quietly placing it on another one, since that is the failure the pin exists to prevent. The service stays inspectable and can still be destroyed, so it can be reported on and cleaned up; reinstalling a plugin that carries the definition makes it runnable again.

`upgrade` across a major version moves the service onto the other definition, which moves where its data is mounted along with it. That is the upgrade a major version asks for rather than something to work around, but it is not a tag change and it is not reversible by pointing the version back. A bare `upgrade` never crosses one: with no version named it moves to the newest tag the service's own major version ships, and leaves the data where it is.

`upgrade --definition` moves the service onto the named definition, with the image and version it ships unless `--image` and `--image-version` say otherwise. That moves where its data is mounted in the same way, even when the image stays the same. A volume the service moved with `volume-targets` stays where it was moved to on the new definition, and an upgrade onto a definition that does not mount that volume is refused before the old container is taken away. `clone` places the new service on the definition the source is pinned to, rather than on the one the source's image resolves to. A `clone` given an `--image` or `--image-version` that would move it onto another definition is refused, since the data would be mounted where that definition does not keep it; upgrade the source onto it first, then clone it.

### Migrating data between definitions

A definition that cannot read the data another of its datastore's definitions left behind says so with `x-dokku.upgrade.migrate`, and an upgrade that moves a service onto it carries the data across. Every postgres definition does: no major version reads another's cluster, and a flavor's extensions are only in its own image.

```shell
# the linked apps are stopped while the data is copied, so nothing they write is lost
dokku-datastore upgrade postgres db --definition postgres-18 --restart-apps
```

The upgrade is refused without `--restart-apps`, before anything is touched. The service's data directory is moved aside to `data.<definition>.<timestamp>` under the service root, and the new definition starts from an empty one. The data is then carried across by the step the new definition declares under `x-dokku.upgrade.from.<definition>` for the one the service is moved off, where there is one: each of `postgres-15` through `postgres-18` runs `pg_upgrade` from `tianon/postgres-upgrade:<old>-to-<new>` for a service moved off any older one, such as `tianon/postgres-upgrade:15-to-18` for one moved off `postgres-15`. Only the pairs onto eighteen are published for arm64, so on an arm64 host a move onto an older major falls back. Anywhere else, or when that step fails, the data is exported from the old definition and imported into the new one. A definition declares `x-dokku.upgrade.export` and `x-dokku.upgrade.import` for that where its `export` and `import` dump less than everything the service holds: postgres's dump the service's own database, so a migration uses `pg_dumpall` instead, which carries every database and role. The pair is used when the old definition declares the export and the new one the import, and otherwise the `export` and `import` subcommands are, which is why a definition that migrates has to implement both. Neither is given the service's `export-args` or `import-args`, which shape a dump an operator asked for rather than one that has to carry everything. A step is given the directory the old data was moved to as `{{ .PreviousData }}`, relative to the service root, and nothing but a step may name it.

A migration the new image cannot restore is also refused before anything is touched. The old definition declares `x-dokku.upgrade.requires`, which runs against the running service and prints what its data needs, and the new one declares `x-dokku.upgrade.check`, which runs in a container of the new image, reads that on stdin, and fails when the image cannot hold the data. The pair is used when the old definition declares the requires and the new one the check. Every postgres definition declares both: timescaledb only restores a dump into the version of the extension that made it, so an upgrade onto an image that installs another version is refused until the two match.

A migration that fails puts the service back on the definition and the image it ran, with the data it had. One that succeeds keeps the old data where it was moved to, for the operator to remove with `upgrade-cleanup` once they are satisfied with the new; `destroy` removes it along with the rest of the service.

```shell
dokku-datastore invoke postgres upgrade-cleanup db
```

A definition that says nothing leaves the data where it is, which is right for one whose next major reads the last one's data in place, such as elasticsearch.

## Flavors

A flavor is a datastore on an image other than its own, shipped as definitions of its own - one per major version, named `<plugin>-<flavor>-<major>` - so that it is placed on the right data directory and followed by dependabot like any other. Postgres has three:

| Definition | Image | Tags |
| --- | --- | --- |
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

A flavor is only used when its image or its definition is named. A create naming no image, version or definition runs the datastore's own image on its newest major, so `dokku-datastore create postgres db` lands on `postgres-18` and never on a flavor.

An image no definition ships still runs on the datastore's own definitions, as it did before flavors existed. A service pinned to one of those keeps its pin: a service created with `pgvector/pgvector:pg17` before pgvector had definitions of its own was placed on `postgres-18`, and its data is where that definition mounts it. `upgrade` only moves a service onto a flavor's definition when the image it ran and the one it is moved to resolve to different definitions, so an upgrade inside a major leaves such a service where it is, and one across a major or onto another image moves it. An image of your own built on a flavor can be kept on that flavor's definition with `--definition`, such as `--definition postgres-pgvector-pg17`.

The images are pinned in each definition's `Dockerfile`, and dependabot holds an older major inside it according to how the image writes its tags. A tag carrying the major as a suffix, such as `0.8.6-pg17`, is only ever moved to one with the same suffix, so it needs nothing more; a tag leading with the major, such as `17-3.5`, needs its semver-major updates ignored, as `postgres-17` does. `go test` checks both, and that every definition has an entry.

postgis publishes images for amd64 only, so its definitions do not run on an arm64 host. The timescaledb image is alpine rather than debian, so its postgres user is `70` rather than `999`, and it ships no `openssl`: its certificate is made in the plain postgres image of the same major, at the tag that definition pins.

## Encrypted postgres connections

Every postgres service is created with a self-signed certificate, and its server encrypts any connection whose client asks it to. Nothing said so, and nothing handed the certificate out, so a client off the host could not verify it was talking to the service. `certificate` prints it, read from the running container, and the plugin readme explains how a client requires tls and verifies the server with it.

```shell
# the certificate a client verifies the server with
dokku-datastore invoke postgres certificate lollipop > server.crt

# a client that refuses an unencrypted connection and verifies the server
psql "postgres://postgres:<password>@db.example.com:5432/lollipop?sslmode=verify-ca&sslrootcert=server.crt"
```

The certificate names no host, so `verify-full` fails where `verify-ca` succeeds. It is made once and kept when the service is rebuilt or upgraded. A certificate of your own is written over the service's `certs/server.crt` and `certs/server.key` as root, which keeps the owner and mode the server needs to read its key, and is served once the service is restarted.

## Encrypted rabbitmq connections

Rabbitmq serves tls on its own ports, 5671 for amqps and 15671 for the management interface over https, rather than on the ones it listens on in plain text. A service had no certificate to serve, a certificate copied into its data directory before `create` stopped the service being created, and the settings that turn tls on had to be written into its `ENV` by hand and the container removed for them to be read.

Every rabbitmq service is now created with a self-signed certificate, as a postgres service is, and serves amqps and https beside its plain listeners. A linked app keeps the `amqp://` dsn it was given, and a client asks for an encrypted connection by connecting to a tls port. `certificate` prints the certificate, read from the running container, and the plugin readme explains how a client verifies the server with it.

```shell
# the certificate a client verifies the server with
dokku-datastore invoke rabbitmq certificate lollipop > server.crt
```

The certificate names no host, so a client verifies it against the certificate rather than the hostname it connects to. It is made once and kept when the service is rebuilt or upgraded, and one of your own is written over `certs/server.crt` and `certs/server.key` as root and served once the service is restarted. The settings that turn tls on are seeded once into `config/tls.conf`, which rabbitmq reads beside the operator's own `rabbitmq.conf`, and find the certificate wherever its volume is mounted.

A service created before this has no certificate and serves no tls until its container is next made. A `stop` and a `start` make it, as does an `upgrade`; a `restart` starts the container the service already has, which was made without tls. The certificate and the settings are made then, and the service's data is left as it was.

```shell
dokku rabbitmq:stop lollipop
dokku rabbitmq:start lollipop
```

A service exposed before this keeps the four ports it was exposed on, and its tls ports are left unexposed until it is exposed again.

## Postgres database encoding and locale

The bash plugin made a postgres service's database itself with `createdb -E utf8`. A custom env that set the locale to `C`, such as `LC_ALL=C`, had the image make its template databases in `SQL_ASCII`, which refused a utf8 copy, and the error was swallowed, so the service was created without its database. There was also no way to ask for a utf8 database with the `C` collation some applications require.

The image now makes the database the first time the service starts, in the encoding and locale its cluster was made with, so a service created with `LC_ALL=C` has its database, in `SQL_ASCII`. The encoding and locale are chosen by handing `initdb` its arguments through the image's `POSTGRES_INITDB_ARGS`, which every postgres definition's image reads, flavors included.

```shell
dokku postgres:create lollipop --custom-env "POSTGRES_INITDB_ARGS=--encoding=UTF8 --locale=C"
```

They are only read when the data directory is first made, so changing the custom env of an existing service leaves its database as it was. A database is moved to another encoding or locale by importing an export of it into a service created with the new ones, and the plugin readme says so.

## Timescaledb imports

An import into a timescaledb service failed now and then with `duplicate key value violates unique constraint "metadata_pkey"` on the key `exported_uuid`. `pg_restore` drops and remakes the timescaledb extension before copying its catalog back, and the extension's background workers could write their own `exported_uuid` into that catalog in between, so whether an import worked came down to which got there first.

An import now runs `timescaledb_pre_restore()` before `pg_restore` and `timescaledb_post_restore()` after it, the way timescale documents a restore, which stops the background workers for the restore and starts them again once it is done. They are started again when the restore fails as well, and the database's `timescaledb.restoring` setting is reset by name, so a restore that fails after dropping the extension does not leave the database with its workers stopped. Arguments passed to `import` still reach `pg_restore`.

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

## The images a plugin needs

A service's own image is not the only one a datastore plugin runs. Four more are pinned by the plugin itself - an ambassador to publish a service's ports, a probe to wait until it answers, busybox to take its data back off it, and the tool that ships a dump to s3 - and a definition may name one of its own on a command, which is how couchdb dumps with a tool its image does not have.

Installing a plugin fetches all of them, and that is the only time they were ever fetched. Every one is now checked again at the moment it is about to be run, so a host that has been pruned since gets it back rather than a command failing on an image nothing put there.

```shell
# comes back even on a host that has been emptied of images
docker system prune --all
dokku-datastore start redis lollipop
```

`dokku redis:set --global disable-pull true` turns fetching off, and turns it off for all of them rather than for the service image alone - docker used to pull the rest regardless of what it had been told. A command that needs an image it may not fetch says which `docker image pull` would give it one, and stops before it changes anything: an expose that cannot get the ambassador leaves the service unexposed, and a destroy that cannot get busybox leaves the service whole.

## Graphite on grafana 13

Graphite services are now made on `dokku/docker-grafana-graphite:13.2.4`, which runs grafana 13.2.4, graphite-web 1.2.1-pre2 and carbon 1.1.10 rather than grafana 6.4.4 and graphite 0.9. A service made before keeps running 6.4.4 until it is upgraded, the same as any service keeps the version it recorded.

```shell
# moves a graphite service onto grafana 13
dokku graphite:upgrade lollipop
```

An upgraded service keeps the metrics carbon has written, and grafana keeps its dashboards, accounts and datasources, migrating its own database as it starts. Graphite-web's database is migrated as the container starts as well, keeping its accounts, saved graphs and dashboards rather than being made again.

## Graphite dashboards

Grafana in a graphite service used to start with a datasource and no dashboards. The image ships dashboards of its own, but nothing loads them, and they graph metrics nothing in a dokku install sends. A graphite service on grafana 13 now has two dashboards in grafana's `Dokku` folder: `Graphite`, which shows carbon and statsd themselves and has data as soon as the service is up, and `StatsD`, which shows the counters, timers and gauges linked apps send, picked out by the first part of each metric's name.

```shell
# grafana, where the dashboards are
dokku graphite:nginx-expose lollipop
```

The dashboards are provisioned from the plugin and written again each time the container is made, so grafana refuses to save a change to one, and a copy saved under another name is kept with grafana's other dashboards. Host metrics need a collector of their own sending to carbon, which nothing in the plugin runs. A service made before has the dashboards the next time its container is made, which is a stop and start rather than a restart, and a service still on grafana 6 has them once it is upgraded.

```shell
dokku graphite:stop lollipop
dokku graphite:start lollipop
```

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

## Container labels

A datastore container carried the labels dokku finds it by and nothing else, so a tool that discovers containers by their labels - traefik, a prometheus scrape config, a backup sidecar - had no way to find one. Docker cannot add a label to a container it has already made, so a label put on by hand was lost the next time the container was built. A service may now be given labels of its own, which are put on every container it is made with from then on.

```shell
# one or several at once, and a value may hold commas and spaces
dokku redis:label lollipop traefik.enable=true "traefik.http.routers.lollipop.middlewares=auth,compress"

# a key the service already has is given the new value
dokku redis:label lollipop traefik.enable=false

# and taken away again by key, or all at once
dokku redis:unlabel lollipop traefik.enable
dokku redis:unlabel --all lollipop
```

Labels have commands of their own rather than a `set` key, since a label's value is often a comma separated list itself and `set` takes a single value. `info --labels` reports them as a json object, sorted by key and empty for a service with none, since a list joined by spaces or commas could not be read back. A key starting with `dokku.` or `com.dokku.`, or the key `dokku` itself, is dokku's own and is refused, so a service cannot hide or stand in for the containers dokku finds by them. The ambassador an exposed service runs is not given the service's labels, so that a tool discovering containers by label finds the service once.

They may be given at `create`, `clone` and `upgrade` as well, with `--label`, repeated for each. A `clone` not passed it takes the source's, and `--label ""` gives it none. A label given to `upgrade` replaces every label the service has. A change reaches a container the next time one is built: `restart` keeps the container it has, so a service already running takes a `stop` and then a `start`.

```shell
dokku-datastore create redis lollipop --label traefik.enable=true --label com.example.team=data
```

## Readiness wait timeout

A service is waited on with the `dokku/wait` probe until it answers on its port, after `create`, `clone`, `start`, `restart`, `upgrade` and `expose` and before an app's deploy starts it. The probe gave up after its own default of 30 seconds unless the definition said otherwise, and only elasticsearch did, at 60. On a slow host, or with an image that does more on its first boot, `create` failed with `ERROR: unable to connect` before the datastore was up, and nothing could raise the limit.

A service may now name a timeout of its own, in seconds, through the `wait-timeout` property.

```shell
# wait up to two minutes for the service to answer
dokku redis:set lollipop wait-timeout 120

# and back to the host's or the datastore's default
dokku redis:set lollipop wait-timeout
```

It is settable at `create`, `clone` and `upgrade` as well, with `--wait-timeout`, and `create` itself is waited on for that long. A `clone` not passed it takes the source's. A host that is slow for every service of a datastore sets the global `wait-timeout` property instead, with `dokku redis:set --global wait-timeout 120`.

```shell
dokku-datastore create postgres lollipop --wait-timeout 120
```

The service's own setting is used first, then the global property, then the definition's `x-dokku.wait_timeout`, and otherwise the probe's default. The value must be a whole number of seconds greater than zero, and anything else is refused, including a malformed global property. `info` reports what the service set, empty when nothing was. Unlike the container settings above, a change takes effect the next time the service is waited on, with no rebuild. The wait header names the timeout when one is in effect.

## Container memory limit

A service's memory limit, in megabytes, is settable at `create`, `clone` and `upgrade` with `--memory`, and `0` means no limit. An `upgrade` not passed it keeps the limit the service has, and a `clone` not passed it takes the source's. It can also be changed on its own with the `memory` property, which clearing sets back to `0`. `info --memory` reports it.

```shell
dokku-datastore upgrade redis lollipop --memory 512

# the same, without an upgrade, reaching the container once it is built again
dokku redis:set lollipop memory 512

# the same, building the container again if the limit changed
dokku redis:set --allow-restart lollipop memory 512
```

## Datastore memory

The memory a datastore itself is told to use, in megabytes, is a setting of its own, apart from the container's memory limit: memcached's cache size, redis's `maxmemory` and the elasticsearch heap. It is settable at `create`, `clone` and `upgrade` with `--service-memory`, changed with the `service-memory` property, cleared by giving no value, and reported by `info --service-memory`. An `upgrade` not passed it keeps what the service has, and a `clone` not passed it takes the source's. A datastore that cannot be told one refuses it.

```shell
dokku-datastore create memcached lollipop --service-memory 256

# redis takes the new maxmemory while it runs, with no restart
dokku redis:set lollipop service-memory 512

# memcached and elasticsearch read it when they start, so the container is built again
dokku memcached:set --allow-restart lollipop service-memory 512
```

A memcached service is created with 64 megabytes, which is kept for every container the service is given afterwards, and its container is left without a memory limit unless `--memory` is passed. A memcached service created before there was a service memory sizes its cache by its memory limit, or at 64 megabytes without one, until it is given one. Redis and elasticsearch services are created without one, and keep their own behaviour until they are given one: redis takes whatever its `redis.conf` says, and elasticsearch sizes its heap from the memory limit, or holds it to 512 megabytes without one.

When the memory limit is set too, keep the service memory below it, since a datastore that grows past the limit has its container killed. For redis, the service memory is passed after `redis.conf` and so wins over a `maxmemory` line in it, and clearing it on a running service goes back to that line, or to no limit without one. A `--maxmemory` in the config options is passed after the service memory and wins over it whenever the container starts, so set one or the other.

A definition declares the setting with `x-dokku.service_memory`, and reads the value in its templates as `{{ .ServiceMemory }}`, empty when the service has none. Its `default` is what a service is created with when it is not given one, and its `change` is a command run against a running service with the new value, for a datastore that can take one while it runs. Without a `change`, a new value reaches the datastore the next time its container is built.

```yaml
x-dokku:
  service_memory:
    default: 64
```

## Changing a service's settings

Every setting a service is created with can be changed with `set` afterwards, other than the ones another command owns: the image, its version and the definition belong to `upgrade`, and the mounts to `mount` and `unmount`. `set` refuses those keys and names the command to use instead. The passwords are set too, and are changed in the datastore as well as on disk, as [Changing a service's password](#changing-a-services-password) describes. The container settings `create` writes into files of their own are set the same way as every other property.

```shell
dokku redis:set lollipop config-options -- "--maxmemory-policy allkeys-lru"
dokku redis:set lollipop custom-env "TZ=UTC;LANG=C.UTF-8"
dokku redis:set lollipop shm-size 256m
```

A value that starts with a dash, as config options usually do, follows `--` so that `set` does not read it as a flag of its own. `custom-env` is semicolon delimited, the way `--custom-env` takes it, and `shm-size` is checked with the parser docker reads `--shm-size` with, at `create`, `clone` and `upgrade` as well as at `set`. Each of these reaches the container the next time it is built, and `set` warns when a setting it wrote is one of those: `restart` keeps the container a service has, so a service already running takes a `stop` and then a `start`.

`--allow-restart` has `set` do that itself. When the value it wrote differs from the one the service had, a setting read when the container is built has the service stopped and started, whether or not it was running, and an expose setting has an exposed service reexposed, or started if it is stopped. A value set again to what it already was restarts nothing, and the warning is still printed, since a value set earlier without the flag may not have reached the container yet. A setting read each time it is used takes no restart, so the flag changes nothing for it.

```shell
dokku redis:set --allow-restart lollipop shm-size 256m
```

The backup settings are set the same way, one at a time, without passing everything `backup-auth` takes again. A credential is never printed back: `set` names the key it wrote, and `info` reports a fingerprint.

```shell
dokku redis:set lollipop backup-endpoint-url https://nyc3.digitaloceanspaces.com
dokku redis:set lollipop backup-secret-access-key "<secret-access-key>"
dokku redis:set lollipop backup-encryption-passphrase "<passphrase>"

# a scheduled backup's bucket and credentials, which rewrite its crontab entry
dokku redis:set lollipop backup-bucket other-bucket
dokku redis:set lollipop backup-use-iam true
```

The generated readme of each plugin lists every key under Service properties, with when a change to it takes effect.

## Settings for every service of a datastore

Ubuntu 25.10 ships sudo-rs, which does not pass arbitrary environment variables through to the processes it starts, so an environment variable exported before a `dokku` command no longer reaches the plugin. The plugin readme and help no longer document setting any, and name the flag that does the same thing instead, such as `--image`, `--image-version`, `--definition`, `--custom-env`, `--config-options` and `--volume-target` on `create`.

The two settings that applied to every service of a datastore and had no flag are global properties now, set with `set --global`, which takes no service name.

```shell
# never pull an image, for every redis service
dokku redis:set --global disable-pull true

# wait up to two minutes for every redis service that does not set its own wait-timeout
dokku redis:set --global wait-timeout 120

# and back to the default
dokku redis:set --global wait-timeout
```

The backend a service is created with, which `DOKKU_DATASTORE_BACKEND` chose, is now given with `--backend` on `create`, and a `clone` is made with the backend of its source.

```shell
dokku-datastore create redis lollipop --backend compose
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

## Moving where a definition's volumes are mounted

Every definition mounts its volumes at a fixed path in the container, which is the path its own image expects. An image that keeps its data somewhere else, such as `bitnami/postgresql` at `/bitnami/postgresql`, had no way to have its data mounted there. Moving the volume is not always all such an image needs - bitnami's postgres, for one, also takes its settings from environment variables of its own - but it is the part nothing else could do. A service may now move any of its definition's volumes with the `volume-targets` property. A volume is named by where it lives under the service's own directory - `data`, `config`, `certs`, `initdb`, `data/grafana` and so on - which stays put however the volume is mounted.

```shell
# the data volume mounted where the image keeps its data
dokku postgres:set lollipop volume-targets data=/bitnami/postgresql

# several at once, separated by spaces
dokku redis:set lollipop volume-targets "data=/srv/redis config=/etc/redis"

# every volume back where the definition mounts it
dokku postgres:set lollipop volume-targets
```

They may be given at `create`, `clone` and `upgrade` as well, with `--volume-target`, repeated for each. A `clone` not passed it takes the source's, and `--volume-target ""` puts every volume back. `info --volume-targets` reports only the volumes a service moved, sorted and space separated, and is empty for one that moved none. A change reaches a container the next time one is built: `restart` keeps the container it has, so a service already running takes a `stop` and then a `start`.

```shell
dokku-datastore create redis lollipop --volume-target data=/srv/redis
```

Moving a volume moves where it is mounted, not where the image reads and writes. Wherever a definition names one of its volumes' paths itself - the config file redis is started with and the directory it writes its dump to, typesense's data and config, the certificates postgres serves and the configs clickhouse's dump scripts read - it follows the volume. The hooks that copy an image's own configuration out still copy it from where the image keeps it, since they run in a container of their own. Nor does what the image does on its own, so an image that keeps writing to its own path writes into the container rather than into the volume, and what it writes is lost when the container is rebuilt. Only move a volume to where the image expects its data. Moving a volume does not move the data on the host, and a config file is seeded into the same directory as before.

Some targets are refused before anything is written:

- a volume the service's definition does not mount, which names the ones it does
- a path that is not absolute, is `/`, or has a colon or a comma in it, which docker's `-v` and compose's short volume syntax would split on
- two volumes at one path, including a volume moved onto where another is mounted
- the path a payload script is mounted at, or a directory above one
- a path a mount already holds, and the other way round, a mount at the path a volume was moved to

The containers `export` and `import` run in beside or in place of the service are given the moved volumes too, so they read and write where the service does. The hooks keep the mounts they declare, which are their own rather than the service's. `upgrade` checks the targets against the definition it lands on, since a volume moved on one definition has to be one the other mounts, and refuses before the old container is taken away until the target is cleared or replaced with `--volume-target`.

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

## Exporting through a terminal

`export` wrote its dump through whatever stdout was. The dokku client runs `ssh -t` whenever its stdin is a terminal, which makes stdout on the dokku host a terminal too, and a terminal adds a carriage return before every newline written to it. A dump exported that way came back a byte larger for every newline in it: a postgres custom-format dump, a mongo archive, a redis dump or a clickhouse backup could not be read back at all, and a mysql, mariadb or couchdb dump had a carriage return at the end of every line.

`export` now turns off the terminal's output processing while the dump is written, and turns it back on once the export is done, so the dump comes back unchanged however ssh was run. Anything logged to the same terminal while the dump is written is printed without that processing too. The dump is also no longer held in memory while it is written.

```shell
# both of these write a dump that can be read back
dokku_client.sh postgres:export lollipop > data.dump
ssh -T dokku@dokku.me postgres:export lollipop > data.dump
```

## Exporting every database in a service

`export` dumped only the database named after the service, but an app can make databases of its own on the same server. Prisma, for one, makes a `prisma` database beside the one it is given. Those databases were left out of every export and every backup, with nothing to say so, and the dump of a service holding its data elsewhere came back empty.

`export` now takes `--all-databases`, which dumps every database in the service rather than only the one named after it, and `import` takes `--all-databases` to load such a dump. Neither is the default. Each database in the dump is replaced under the name it was exported from, and a database the dump does not hold is left alone. The databases the server keeps for itself are not dumped, or are not loaded, so the accounts and passwords of the service it is imported into are left as they were.

```shell
# every database in the service
dokku postgres:export lollipop --all-databases > all.dump

# loaded back into the databases it was taken from
dokku postgres:import lollipop --all-databases < all.dump
```

postgres, mysql, mariadb, mongo, couchdb and clickhouse take it, which every definition of them declares with `all_databases` on its `export` and `import` commands. redis already dumps every database the server holds, so it refuses the flag, as does a datastore with no `export` at all.

What the dump holds is up to each datastore:

- postgres dumps each database as plain sql rather than in `pg_dump`'s own format, which is loaded with `psql` rather than `pg_restore`. Each database is dropped and made again with the encoding and locale it had, owned by `postgres`, since the dump carries no owners. The templates are left out, as is the `postgres` database unless it is the service's own. `pg_dumpall` is not used, since it dumps the roles too, and loading them would set the `postgres` password back to the one the dump was taken with.
- mysql and mariadb dump every database but `mysql`, `sys`, `information_schema` and `performance_schema`, as root. Each database is dropped and made again as it is loaded.
- mongo dumps every database as the admin account. `mongodump` cannot leave a database out of such a dump, so `admin`, `config` and `local` are left out when it is loaded instead.
- couchdb dumps every database whose name does not start with `_`, keyed by name.
- clickhouse backs up every database but the ones it keeps for itself, under the names they have, leaving out `default` when it holds no tables. Every database is restored beside the live one before any is swapped in, so an archive that fails to restore leaves them all as they were.

`backup` now always dumps every database wherever the datastore can, so a backup of one of these datastores is restored with `import --all-databases`. A backup made before this change holds only the service's own database, and is still restored without it.

```shell
dokku postgres:import lollipop --all-databases < backup-folder/export
```

`clone` still copies only the service's own database. A dump of every database loads each one under the name it was exported from, so the source's database would land beside the clone's own rather than in it.

Arguments after `--`, and the `export-args` and `import-args` properties, are passed on by these dumps as well, a backup's included. They reach the same tools as before, with one exception: postgres loads every database with `psql` rather than `pg_restore`, so its `import-args` are handed to `psql`. postgres passes `export-args` to `pg_dump` once for each database, so a format such as `-Fc` there would leave a backup that cannot be loaded.

## Resetting a service's data

Emptying a service meant destroying it and creating it again, which needed every linked app unlinked first and handed the new service new credentials, so each app had to be linked again.

`reset` deletes all of a service's data and leaves the service as empty as a newly created one. The service, its credentials and the apps it is linked to are kept, so a linked app keeps the url it was given. Connections the apps hold open may be closed. It asks for the service name before deleting anything, unless `--force` is given.

```shell
# asks for the service name first
dokku postgres:reset lollipop

# without asking
dokku postgres:reset lollipop --force
```

How the data is deleted is up to each datastore:

- postgres drops the database, ending the sessions in it, and creates it again with the owner, encoding and locale it had. The extensions it had, such as `vector`, `postgis` or `timescaledb`, are created again at the image's default version, in the `public` schema. Settings made with `ALTER DATABASE` and grants on the database itself are not kept.
- mysql and mariadb drop the database, ending the sessions in it, and create it again with the character set and collation it had. Grants on the database are kept.
- mongo drops the database. The service account is kept, since mongo stores accounts apart from the database they belong to.
- redis and memcached flush every key.
- clickhouse drops the database and creates it again as it was declared.
- omnisci drops every view and table in the database.
- couchdb deletes every database whose name does not start with `_`, and creates the service's database again.
- elasticsearch deletes every data stream and index whose name does not start with `.`. Those are the ones elasticsearch keeps for itself. Templates, ingest pipelines and lifecycle policies are kept.
- meilisearch deletes every index, and typesense deletes every collection and alias. Both keep their api keys.
- rabbitmq deletes every vhost, which takes their queues and messages with them, and creates the service's vhost again. The accounts are kept.
- rethinkdb drops every table in every database but its own.
- solr deletes its core's index, keeping the configuration a deploy copied into it. The service is stopped while this happens.
- graphite deletes every metric, keeping grafana's dashboards and accounts. The service is stopped while this happens.

nats and pushpin keep no data, so `reset` reports that it is not implemented for them.

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

`info` reports every setting `backup-auth` and `backup-set-encryption` store, so tooling can tell whether what it would apply is already in place. The region, signature version and endpoint are reported as they are, under `--backup-default-region`, `--backup-signature-version` and `--backup-endpoint-url`. The access key id and secret, and the backup passphrase, are secrets and are never printed. Each is reported on its own as a lowercase hex sha256 fingerprint of the stored value with surrounding whitespace trimmed, under `--backup-access-key-id-fingerprint`, `--backup-secret-access-key-fingerprint` and `--backup-encryption-fingerprint`. A fingerprint is empty when that value is not stored, so one credential can be compared with a candidate for it alone and a set of that credential skipped when the candidate is already in place. The access key id and the secret are also reported together under `--backup-auth-fingerprint`, which is empty until both are stored and is what `backup-auth` writes in one call. `--backup-authenticated` is only `true` when both the access key id and the secret are stored, since a backup refuses to run with either one missing. Each `backup-auth` call replaces every stored setting, so a region, signature version or endpoint omitted from the call is removed and reported as empty.

The service's passwords are reported the same way, under `--password-fingerprint` and `--root-password-fingerprint`.

A fingerprint is compared against one computed from a copy of the value:

```shell
# one credential
printf '%s' "$AWS_ACCESS_KEY_ID" | sha256sum
printf '%s' "$AWS_SECRET_ACCESS_KEY" | sha256sum

# the pair, which is what --backup-auth-fingerprint reports
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

## Backup object name

Backups were always uploaded to `<prefix>-<service>-<timestamp>.tgz`, so every backup landed on a new key and bucket versioning and lifecycle rules had nothing to rotate. A service may now name the key its backups are uploaded to through the `backup-object-name` property, and drop the timestamp through the `backup-timestamp` property.

```shell
# upload every backup to db/latest.tgz
dokku redis:set lollipop backup-object-name db/latest
dokku redis:set lollipop backup-timestamp false

# and back to the default name and a timestamp
dokku redis:set lollipop backup-object-name
dokku redis:set lollipop backup-timestamp
```

| `backup-object-name` | `backup-timestamp` | key                              |
| -------------------- | ------------------ | -------------------------------- |
| unset                | unset or `true`    | `redis-lollipop-<timestamp>.tgz` |
| `db/latest`          | unset or `true`    | `db/latest-<timestamp>.tgz`      |
| unset                | `false`            | `redis-lollipop.tgz`             |
| `db/latest`          | `false`            | `db/latest.tgz`                  |

An encrypted backup ends in `.tgz.gpg` instead. The bucket given to `backup` and `backup-schedule` may also end in a path, such as `my-bucket/redis-backups`, which the key is placed under.

The object name may hold letters, numbers, dots, dashes, underscores and slashes, and may not start or end with a slash or hold an empty, `.` or `..` path segment. `backup-timestamp` takes `true` or `false`. Anything else is refused before it is written.

Without a timestamp, each backup replaces the one before it unless the bucket has [versioning](https://docs.aws.amazon.com/AmazonS3/latest/userguide/Versioning.html) turned on, which is the intended pairing. A backup that fails after it started uploading removes only what it uploaded, so on a versioned bucket the previous backup stays current. An older backup is restored by downloading that version of the object, for instance with `aws s3api get-object --version-id`, and importing it as before.

Both are read when a backup runs, so they apply to the next backup, scheduled ones included, without rescheduling. `info` reports them under `--backup-object-name`, empty when nothing was set, and `--backup-timestamp`, `true` unless it was turned off. `clone` copies `backup-timestamp` but not `backup-object-name`, since a clone backed up to the same bucket under the source's name would replace the source's backups.

## Backups to s3 compatible services

Backups are uploaded by the aws cli in the backup image, which names the upload `s3://<bucket>/<key>` whatever service it is sent to. The `s3://` is not where the backup goes. When `backup-auth` is given an endpoint url, the upload is sent to that endpoint instead of to aws, so any s3 compatible service such as [minio](https://github.com/minio/minio) or [DigitalOcean Spaces](https://docs.digitalocean.com/products/spaces/) works.

```shell
# a space in the nyc3 region, whose endpoint does not include the space name
dokku redis:backup-auth lollipop SPACES_ACCESS_KEY SPACES_SECRET_KEY nyc3 s3v4 https://nyc3.digitaloceanspaces.com

# backed up to the my-space space
dokku redis:backup lollipop my-space
```

A bucket named `s3://my-space` was passed through as it was, so the backup was uploaded to `s3://s3://my-space` and failed after the service had been exported, and an endpoint without a scheme was only refused by the aws cli at the same point. Both are now refused before anything else is done:

- `backup` and `backup-schedule` refuse a bucket that does not follow the [s3 general purpose bucket naming rules](https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html), a bucket named with a scheme such as `s3://`, and a path after the bucket that ends in a slash or holds an empty, `.` or `..` segment.
- `backup-auth` refuses an endpoint url that is not an `http` or `https` url with a host, and leaves the settings it stored earlier as they were.

Buckets created in `us-east-1` before March 2018 may have uppercase letters and underscores, which the naming rules no longer allow, and are refused as well. A backup already scheduled to one keeps its entry in the dokku crontab, but the backup it runs is refused, and the reason is written to the service's backup log.

## Multiple backup schedules

A service has one scheduled backup until another is added. `backup-schedule` without `--id` records that schedule, and calling it again replaces its cron expression, bucket and instance role. `--id <name>` adds a schedule under that name, or replaces the one that name already has. `--id default` is the schedule made without `--id`, not a second copy. An id is made of letters, numbers, dashes and underscores.

```shell
# the schedule a service already had
dokku redis:backup-schedule lollipop "0 3 * * *" my-bucket

# a frequent backup and a monthly one, each to its own bucket
dokku redis:backup-schedule lollipop "*/15 * * * *" bucket-a --id frequent
dokku redis:backup-schedule lollipop "@monthly" bucket-c --id monthly --use-iam
```

Each schedule is a line in the dokku crontab. The command it runs names the schedule, `dokku redis:backup lollipop my-bucket --id default`, and appends `--use-iam` when that schedule authenticates with an instance role. A line written before schedules had ids runs with the service's settings, which is what an un-overridden schedule does. The next time the crontab is written, the line gains `--id`.

`backup-schedule-cat` prints the one crontab line when a service has one schedule, and one line per schedule when it has several: the id, a tab, then the crontab line. `--id` prints one schedule in the single-schedule form. `--format json` for one schedule is the object it always was, plus the id and the settings that backup runs with. For several schedules it is `{"schedules":[...]}`. The passphrase is a fingerprint. `overrides` lists the properties that schedule set itself.

`backup-unschedule` removes the only schedule. With several and no `--id` it refuses, and says to pass `--id` or `--all`. `--id` removes that one, including `--id default`. `--all` removes every schedule. Destroying a service removes every schedule.

`info --backup-schedule`, `--backup-bucket` and `--backup-use-iam` stay the default schedule, and are empty (and `false` for the instance role) when that schedule is gone. `--backup-schedules` is every schedule on one line, the default one first and the others by id. A record is `id|cron|bucket|use-iam`, and records are separated by semicolons:

```text
default|0 3 * * *|my-bucket|false;frequent|*/15 * * * *|bucket-a|false;monthly|@monthly|bucket-c|true
```

The other backup keys `info` reports stay the service's values. A passphrase that exists only on a schedule does not make `--backup-encrypted` true.

A schedule can set its own storage class, object name, timestamp, mailto, keyserver, export arguments, passphrase, public key, region, signature version and endpoint. `set <key> <value> --id <id>` records it, and an empty value clears that schedule's own value so it inherits the service's again. The schedule has to exist already. `set backup-bucket` and `set backup-use-iam` still change the default schedule, and only when it is the only one; otherwise they are refused, and the schedule is changed with `backup-schedule --id`. The encryption commands, `backup-auth` and `backup-deauth` take the same `--id`.

```shell
# credentials every schedule uses, unless it has its own or authenticates with an instance role
dokku redis:backup-auth lollipop AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY

# the monthly schedule ships to a different account
dokku redis:backup-auth lollipop OTHER_ACCESS_KEY_ID OTHER_SECRET_ACCESS_KEY us-east-1 --id monthly
```

`backup-auth` without `--id` still replaces the service's credentials as a whole, so a region, signature version or endpoint that is not passed is removed and the next backup has none. `backup-auth --id` writes both keys onto that schedule. A region, signature version or endpoint that is not passed is cleared on the schedule, and the backup inherits the service's value. The schedule must already exist. `backup-deauth` without `--id` removes the service's `backup/` directory and leaves credentials stored on a schedule. `backup-deauth --id` clears that schedule's five auth values, and the next run without `--use-iam` uses the service's pair.

A backup passes no access key and no secret when it is run with `--use-iam`. The crontab line of a schedule created with `--use-iam` already includes that flag. A manual `backup --id <id>` without the flag uses credentials, including a pair stored on a schedule that normally uses an instance role. A schedule with both an access key and a secret uses that pair. A schedule with neither uses the service's files in `backup/`. `set --id` can change the region, signature version or endpoint of a schedule on its own, and can rotate one key once `backup-auth --id` has stored both. It refuses to set the first key, and it refuses to clear one key while the other remains.

A schedule that sets a passphrase or a public key uses that pair instead of the service's, and the public key still takes precedence inside the pair. Export arguments set on a schedule are what that schedule's export runs with. Anything a schedule does not set is the service's value, read when the backup runs. `backup` without `--id` uses the service's settings. `backup --id <id>` uses that schedule's settings, and the bucket and `--use-iam` still come from the command, which is what the crontab line passes. A backup started with `--id` names that id in the line it writes for the bucket.

On disk, the default schedule's cron expression, bucket and instance role stay in `backup-schedule`, `backup-bucket` and `backup-use-iam` under `$DOKKU_LIB_ROOT/config/<prefix>/<service>/`. Every other schedule, and any setting a schedule sets for itself, is one object in the `backup-schedules` file in that same directory, mode `0600`. The file is a JSON array. It is absent when the only schedule is the default one with nothing of its own, and an empty array is deleted rather than left in place. The default schedule is an object in the array only when it has set something for itself, and the three property files win when that object disagrees with them. A passphrase recorded on a schedule is in that object, and so are credentials recorded with `backup-auth --id`. `info` and `backup-schedule-cat` never print them. `backup-schedule-cat` reports fingerprints of the pair a run of that schedule would use without `--use-iam`, and the region, signature version and endpoint that run would use. `info` still reports the service's credentials. A pair that exists only on a schedule does not make `--backup-authenticated` true. The service's credentials stay in its `backup/` directory, and the service's encryption files stay the pair a schedule inherits. Clone does not copy schedules. Destroy removes the directory, and unscheduling removes the schedule and its own settings with it. Two schedules share a pair by both inheriting the service's, or by each storing one.

## Scheduled backup email

The bash plugins wrote each scheduled backup to a cron file of its own in `/etc/cron.d`, where a `MAILTO` line could be added by hand to have the backup's output mailed. Scheduled backups are now part of the dokku crontab, which only has the global `MAILTO` set with `dokku cron:set --global mailto`, and their output is appended to the service's backup log, so cron had nothing to mail. A service may now name who the output of its scheduled backups is mailed to through the `backup-mailto` property.

```shell
# mail the output of each scheduled backup to two people
dokku redis:set lollipop backup-mailto ops@example.com,dba@example.com

# and back to the global MAILTO
dokku redis:set lollipop backup-mailto
```

The recipients are a comma-separated list of email addresses or local users, without spaces, and anything else is refused before it is written. Dokku writes the backup under a `MAILTO` line of its own in its crontab, and cron mails all of the backup's output, whether the backup succeeded or failed, while it is still appended to the log file. A mail transfer agent has to be configured on the host for the mail to be delivered.

The property is read when dokku writes its crontab, so setting or clearing it on a service with a scheduled backup has dokku write the crontab again. It is kept when a backup is scheduled again or unscheduled. The service value applies to every schedule that has not set its own. `info` reports the service value under `--backup-mailto`, empty when nothing was set, and `clone` copies it.

The recipients are handed to dokku through the `cron-entries` trigger as a json entry, which only dokku versions including [dokku/dokku#9104](https://github.com/dokku/dokku/pull/9104) read. Older versions refuse the whole crontab when they see one, so the plugin hands them the entry it always has, and warns that the recipients are ignored. `backup-schedule-cat` shows the line a dokku version that reads them writes.

A cron file an earlier version of the plugin wrote is migrated onto the dokku crontab when the plugin is installed. A `MAILTO` line added to it by hand is now kept as the service's `backup-mailto`, rather than leaving the file where it was, unless it cannot be written into the crontab, in which case it is dropped with a warning.

## Scheduled backup logs

The output of every scheduled backup of a datastore was appended to a single `/var/log/dokku/<prefix>.log`, and nothing in it said which service a line came from or when it was written. `backup` printed nothing of its own when it succeeded, so a run that worked and one that never started looked the same, and nothing read the file back. Each service's scheduled backups are now appended to a log of their own, `/var/log/dokku/<prefix>.<service>.backup.log`, which the `backup-logs` command prints.

```shell
# the last 100 lines
dokku redis:backup-logs lollipop

# followed
dokku redis:backup-logs lollipop --tail

# followed, starting from the last 50 lines
dokku redis:backup-logs lollipop --tail=50
```

`backup` now starts with a line saying which service it is backing up, to which bucket and at what time, and ends with a line saying whether it finished or failed and at what time, in utc, so each run can be picked out of the log.

The log is kept under `DOKKU_LOGS_DIR` when dokku keeps its logs somewhere other than `/var/log/dokku`, and is rotated by the logrotate configuration dokku installs for that directory. It is removed along with the service, so a service created later under the same name does not start with its output.

The dokku crontab is written again when the plugin is installed on a host with a scheduled backup, which moves every scheduled backup onto its own log. The shared `/var/log/dokku/<prefix>.log` is left where it was, since it holds the only record of the backups that ran before, and can be removed by hand once it is no longer needed.

## Backup notifications

Nothing outside dokku could be told whether a backup worked, short of having cron mail its whole output. Every `backup` now fires the `service-action` trigger once it ends, whether cron or a person ran it, the way `create`, `destroy`, `link` and `unlink` already do. It is handed `post-backup`, the datastore's command prefix, the service, the bucket and `success` or `failure`.

```shell
#!/usr/bin/env bash
# posts every failed backup to a webhook
main() {
  declare ACTION="$1" SERVICE_TYPE="$2" SERVICE="$3" BUCKET="$4" STATUS="$5"
  [[ "$ACTION" == "post-backup" ]] || return 0
  [[ "$STATUS" == "failure" ]] || return 0
  curl -fsS -X POST -d "the $SERVICE_TYPE service $SERVICE failed to back up to $BUCKET" https://example.com/hooks/backups
}

main "$@"
```

A backup that is refused before it starts, because the bucket is missing or invalid or the service is pinned to a definition the plugin does not ship, fires `failure` too, with the bucket empty when none was named. A backup of a service that does not exist fires nothing. A trigger that fails is warned about, and the backup exits as it would have without it, so a broken notification never turns a backup that worked into one that failed. What the trigger prints is part of the backup's output, so for a scheduled backup it is appended to the service's backup log.

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

The passwords a definition declares under `secrets` are generated when a service is created, unless `--password` or `--root-password` gives one. Each flag sets the secret whose `env` is `SERVICE_PASSWORD` or `SERVICE_ROOT_PASSWORD`.

```shell
dokku-datastore create mysql lollipop --password <password> --root-password <root-password>
```

A datastore with no secret for a flag refuses it before anything is created, rather than starting on a generated password nobody was told about. Postgres and redis, for instance, have no root password, and memcached has no password at all.

### Changing a service's password

A password is kept by the datastore as well as in the service's `PASSWORD` or `ROOTPASSWORD` file, so writing the file alone changed nothing the datastore checks. `set` changes either one with the `password` and `root-password` keys, in the datastore first and then on disk, and points every linked app at the new url.

```shell
dokku postgres:set lollipop password <new-password>
dokku mysql:set lollipop root-password <new-root-password>

# the linked apps are given the new url without being restarted
dokku postgres:set --no-restart lollipop password <new-password>
```

Each datastore takes a new password its own way, which its definition declares. Most are told it while they run: postgres, mysql, mariadb, mongo, rabbitmq, couchdb and omnisci change the account in the datastore itself, and redis is told the new password and has the `requirepass` line of its config file written over to match. The rest only read it as they start, so changing it stops and starts the service, and `set` refuses to unless it is given `--allow-restart`: nats, meilisearch, clickhouse and graphite are made again on the new password, and typesense has the `api-key` line of its config file written over with the service down. A password told to the datastore is only changed while the service is running, and one read as it starts starts a stopped service, the way `--allow-restart` does for every other setting.

A linked app holding the service's url under any of the keys its link set has the password in those values replaced, keeping the scheme and anything after the url, and is restarted unless `--no-restart` is given. `--allow-restart` is about the service and `--no-restart` about its apps, and `--no-restart` is refused for any other key, since no other key reaches an app. A password set to the one the service already has touches nothing.

A password cannot be cleared, and may only hold letters, digits, `.`, `_`, `~` and `-`, since it is written into the url, config files and statements as it is. It is never printed back: `info` reports a fingerprint of each under `--password-fingerprint` and `--root-password-fingerprint`, which is empty for a password the datastore does not have.

A definition says how one of its `secrets` is changed with `change`, a command run like any other and given the value in use as `{{ .Secret.<name> }}` and the new one as `{{ .NewSecret }}`, which nothing else may name. It is best passed through `env`, so that it stays out of the container's process table. A secret the datastore reads as its container is made declares `rebuild: true` instead. A change in `offline` mode, or a rebuild, stops the service and so needs `--allow-restart`. A secret declaring neither cannot be changed.

```yaml
x-dokku:
  secrets:
    password:
      file: PASSWORD
      length: 16
      env: SERVICE_PASSWORD
      change:
        exec: ["thing-passwd"]
        env:
          OLD_PASSWORD: "{{ .Secret.password }}"
          NEW_PASSWORD: "{{ .NewSecret }}"
```

## Cloned services

A clone was made on the source's image and given its data, but nothing else about the source carried over: every other setting came from the flags passed to `clone`, so a clone made without repeating all of them landed on the defaults rather than on what the source runs with.

A clone now starts from the source's settings - its config options, custom env, memory, service memory, shm size, initial, post-create and post-start networks, log driver, log options, restart policy, labels, mounts, volume targets, backup keyserver, backup mailto, backup storage class, backup timestamp and export and import arguments. A flag passed to `clone` overrides that one setting, and a flag passed empty clears it, the same as on `upgrade`.

```shell
# the same settings as lollipop
dokku-datastore clone redis lollipop lollipop-2

# the same settings as lollipop, except these two
dokku-datastore clone redis lollipop lollipop-3 --restart no --custom-env ""
```

A clone runs the source's image and image version unless `--image` or `--image-version` say otherwise. These two are the exception to a flag passed empty clearing a setting, since a service always runs an image, so an empty one keeps the source's. An `--image-version` alone keeps the source's image. An `--image` alone takes the version the definition ships when it is the definition's own image, and is otherwise refused until `--image-version` names one, the same as on `upgrade`. An image or version that would run on another definition than the one the source is pinned to, such as another major version or flavor, is refused before anything is made: upgrade the source onto it first, then clone it.

```shell
# the same settings as lollipop, on another image version
dokku-datastore clone redis lollipop lollipop-4 --image-version 8.8.0
```

The networks are copied as well, since a container joins a network under its own service name and a clone next to its source does not clash with it.

The password, the exposed ports and the `port-bind-address`, `port-source-range` and `expose-host` that go with them, the app links and the backup credentials, schedule, encryption and object name are not copied. The password is generated for each service unless `--password` or `--root-password` gives one, an exposed port would clash with the source's on the host, links belong to the apps, a copied backup schedule would ship a second set of backups to the source's bucket, and a copied object name would have those backups replace the source's.

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

### Exposing udp ports

A port a definition declares as udp is published and forwarded over udp. Graphite's statsd port is the only one shipped: it has always been exposed over tcp, where a statsd client off the host, which sends udp, reached nothing. Random ports picked for a udp port are ones free over udp. The ambassador image is now `dokku/ambassador:0.8.4`, since the socat in 0.8.2 refuses to listen for udp. A graphite service exposed by an older version of the plugin keeps its tcp-only ambassador until the service is next started, exposed or restarted, or until `reexpose` replaces it. `info --exposed-ports` still reports each port as `container->host`, without the protocol.

```shell
# statsd is published over udp, and the rest of graphite's ports over tcp
dokku graphite:expose lollipop 8125 8126 8080 8081 2003

# a service exposed before this picks it up without a restart
dokku graphite:reexpose lollipop
```

### A definition that gains a port

A service's port file holds a host port for each port its definition had when it was exposed, in their order. A definition adds a port at the end of its list, so a service exposed before that holds fewer host ports than the definition has ports. Starting such a service through an ambassador used to fail outright - `port file ... holds 4 ports, expected 6` - leaving none of its ports published, and its `exposed-dsn` was empty.

The ports it holds are now published where they were and the new ones are left unexposed, as a service exposed directly already did, and `exposed-dsn` is reported so long as the port it names is exposed. `expose` takes fewer ports than the definition has as well, down to its primary port, so an `expose` written for a datastore before it gained a port keeps working. More ports than the definition has are still refused. A service is exposed on every port by exposing it again.

```shell
# exposed on the four ports rabbitmq had before it served tls
dokku rabbitmq:expose lollipop 5672 4369 35197 15672

# and on its tls ports as well
dokku rabbitmq:unexpose lollipop
dokku rabbitmq:expose lollipop
```

### A definition whose pre_create hook changes

A definition's `pre_create` hook fills what the service mounts before its container is made, such as the configuration clickhouse copies out of its image. It used to run when a service was created, and again only when something the service mounts was missing. A definition that started mounting a directory every service already has, or whose hook started copying something new, never had it run for the services that already existed.

The hook that ran is now recorded in the service's `PRE_CREATE` file, and the hook is run again the next time the service's container is made whenever it or the service's image has changed since. Every hook leaves what is already there alone, so running one again only fills in what is missing. Every service of a definition with a hook has it run once more after upgrading. For elasticsearch, that run changes the owner of everything in the service's data directory again, which takes longer on a large one.

Graphite's config directory is the first to need this. It is now mounted at `/opt/graphite/conf`, where carbon reads its configuration, and filled with the files the image ships there, so a `storage-schemas.conf` written there takes effect. A graphite service made before keeps any file already in its config directory and has the rest copied in once its container is made again.

```shell
# the container is kept by a restart, so stop and start the service instead
dokku graphite:stop lollipop
dokku graphite:start lollipop
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

# the same, reexposing in the same step when the range changed
dokku redis:set --allow-restart lollipop port-source-range
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

### JDBC urls

A java app connecting through a jdbc driver cannot read `DATABASE_URL`, since the driver expects `jdbc:postgresql://host:port/database?user=...&password=...` rather than `postgres://user:password@host:port/database`, so the url had to be rewritten by hand or by a wrapper in the app. `link --jdbc` sets the jdbc url beside the service url, along with the username and password it names, the way Heroku hands them to java apps. Postgres, MySQL and MariaDB support it, using the `postgresql`, `mysql` and `mariadb` jdbc drivers. Every other datastore refuses the flag and changes nothing.

The jdbc variables are named after the variable holding the service url. `DATABASE_URL` is joined by `JDBC_DATABASE_URL`, `JDBC_DATABASE_USERNAME` and `JDBC_DATABASE_PASSWORD`, an `--alias` or a generated alias by the same three named after it, and an `--env-var` such as `MB_DB_CONNECTION_URI` by `JDBC_MB_DB_CONNECTION_URI`, `JDBC_MB_DB_CONNECTION_URI_USERNAME` and `JDBC_MB_DB_CONNECTION_URI_PASSWORD`. A name already set on the app is refused before anything changes. A `--querystring` is appended to the jdbc url as well, after the credentials it already carries. All of the variables are set together, so the app restarts once.

`link` records the jdbc variables in the service's `link-jdbc-config-keys` property, apart from `link-config-keys`, and `unlink` unsets them along with the service url. A jdbc url that has been pointed elsewhere is left alone, as the service url is. `promote` copies the promoted service's jdbc values onto `JDBC_DATABASE_URL`, `JDBC_DATABASE_USERNAME` and `JDBC_DATABASE_PASSWORD`, and the ones they displace go with the displaced url to its generated alias. Promoting a service that was linked without `--jdbc` leaves the jdbc variables alone, with a warning that they do not point at it. Changing the password updates the jdbc url and password the same way it updates the service url, and renaming or cloning an app carries the record over. An app that is already linked has to be unlinked and linked again to be given the jdbc variables.

```shell
# sets DATABASE_URL, JDBC_DATABASE_URL, JDBC_DATABASE_USERNAME and JDBC_DATABASE_PASSWORD
dokku postgres:link lollipop spring-app --jdbc

# unsets all four
dokku postgres:unlink lollipop spring-app
```

## Multiple databases and users

A service runs in a container of its own, with one database and one user, so every app that needed a database of its own needed a service of its own, and on a small host each of those containers takes memory. A service may now hold databases and users beside the ones it was created with, and an app may be linked to any database and as any user the service holds, so several apps can share one container. Services are still created with one database and one user, and nothing changes for a service that is never given more.

```shell
# a database and a user beside the service's own
dokku postgres:create-database lollipop paintshop
dokku postgres:create-user lollipop painter

# an app linked to that database as that user, whose DATABASE_URL names both
dokku postgres:link lollipop playground --database paintshop --user painter

# the names a service holds, never the passwords
dokku postgres:info lollipop --databases
dokku postgres:info lollipop --users
```

`create-user` generates a password the way `create` does when none is given, and one of your own may follow the user name. `set-user-password` gives a user a new one, generated the same way when none is given, and rewrites the url of every app linked as that user, restarting it unless `--no-restart` is given. The apps linked as any other user hold nothing the change touches and are left alone, and the same is true of the password of the user the service was created with, which is still changed with `set lollipop password`. `destroy-database` and `destroy-user` remove what was made, asking first unless `--force` is given, and refuse while an app is linked to it. The database and user a service was created with are only removed with the service. A name may only contain lowercase letters, digits and `_`, start with a letter or `_`, and be at most 32 characters long, and the names a datastore keeps for itself are refused.

Every user made in a service can use every database it holds, including the ones made after the user, but is not a superuser. On postgres each is a member of a role named `dokku_users`, which every database grants what is in its `public` schema, along with whatever any user makes there later. On mysql and mariadb each is granted every privilege on every database the service holds, but none on the server's own and no grant option. Mongo, couchdb, clickhouse, rabbitmq and omnisci grant each user every database the same way, a database being a vhost on rabbitmq. Redis users are acl users with the default user's permissions, kept in `users.acl` in the data volume. Each database is given every user the service holds when it is made, so making the database or the user first comes to the same thing.

| datastore                                   | databases                                  | users |
| ------------------------------------------- | ------------------------------------------ | ----- |
| clickhouse, couchdb, mariadb, mongo, mysql  | made with `create-database`                | yes   |
| omnisci, postgres and every postgres flavor | made with `create-database`                | yes   |
| rabbitmq                                    | vhosts, made with `create-database`        | yes   |
| redis                                       | its sixteen, numbered from 0               | yes   |
| rethinkdb, solr                             | made with `create-database`, cores on solr | no    |

Redis always holds sixteen databases, so there are none to make, and an app is linked to one by its index with `link --database 3`. The bound is redis's default rather than the `databases` line of the service's own config. A clickhouse or redis service made before this only takes users once its container has been made again, since the server is only told where to keep them as it starts: `create-user` says so, and `stop` and then `start` make it again. A clickhouse url using the `http` scheme names no database, so `--database` changes the native url alone.

`link` records the database and user an app was linked on in the service's `link-database` and `link-user` properties, so `unlink`, `promote`, a password change and renaming or cloning an app find the url it was given. A link on the service's own database and user records nothing, which is what every link made before looks like. An app that is already linked has to be unlinked and linked again to be moved to another database or user.

`connect` takes `--database` and `--user`, and `export`, `import` and `reset` take `--database`, each addressing that database or user in place of the service's own. A command that acts on the whole service refuses them rather than ignoring them, as redis's `export`, which dumps every database at once, does with `--database`. `export --database` cannot be combined with `--all-databases`. A `reset` of the service's own database on couchdb, rabbitmq and rethinkdb, which empties every database an app could have written to, leaves the ones made with `create-database` alone, and a `reset` of one of those empties it alone. A backup is unchanged, and holds the databases made in a service only where it holds every database.

```shell
dokku postgres:connect lollipop --database paintshop --user painter
dokku postgres:export lollipop --database paintshop > paintshop.dump
dokku postgres:import lollipop --database paintshop < paintshop.dump
dokku postgres:reset lollipop --database paintshop
```

A `clone` makes every user and database its source made, and copies the data of each database across through the same export and import its own database is copied with. Unlike the password of the user the service was created with, which a clone generates afresh, each user keeps its password, so an app linked to the clone as that user keeps working with what it already holds.

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

`--format json` prints a command's result as json on stdout: `create`, `info`, `list`, `links`, `app-links`, `backup-schedule-cat`, `trigger-cron-entries` and `trigger-service-list` have one. `backup-schedule-cat` prints one schedule as an object, and several as `{"schedules":[...]}`. `info` reports every schedule on the `backup-schedules` key as one line, which is the same line `--backup-schedules` prints. Everything else - progress, warnings and errors as json objects, and whatever a trigger or a docker command prints on the way - goes to stderr, so stdout can be handed straight to a json reader. The text a trigger prints without it is unchanged, since dokku reads it a line at a time. `readme` and `trigger-help` only print text, and refuse `--format json`.

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

### Readme sections a definition adds

What a datastore does that none of its commands explain had nowhere to be written but a plugin's own `docs/<command>.md`, which is attached to a command and kept out of reach of the tests here. A definition may now add sections of its own to the readme under `x-dokku.documentation`, each with a `title` and a `body`. They are written after the sections every datastore has and before the one on disabling `docker image pull`, and where several definitions of a datastore declare the same title, the newest one's is used.

```yaml
x-dokku:
  documentation:
    - title: Encrypting connections with TLS
      body: |
        every {{.Title}} service is created with a self-signed certificate.
        to refuse an unencrypted connection, add 'sslmode=require' to the dsn the service is exposed at:
        dokku {{.CommandPrefix}}:info lollipop --exposed-dsn
            postgres://postgres:PASSWORD@db.example.com:5432/lollipop?sslmode=require
```

Both are templates rendered against the same data a command's documentation is, and the body is written the way that documentation is. A line starting with `dokku` or `export` becomes a shell block, a line indented by four spaces becomes a literal block, and a line opening with `>` becomes a note. Every other line is prose, and prose lines run together into one paragraph up to the next block, so each sentence ends in a period. Inline code is written in single quotes, which become backticks, and an all caps word is quoted as though it were a variable, so write "tls" and "dsn" in lowercase and leave contractions out. A section with no title or no body, or two with one title, is refused when the definition is parsed.
