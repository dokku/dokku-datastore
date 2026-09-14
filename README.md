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
    restart                               Restarts a service
    start                                 Starts a service
    stop                                  Stops a service and removes the container
    unexpose                              Unexposes a service
    unlink                                Unlinks a service from an app
    version                               Return the version of the binary
```
