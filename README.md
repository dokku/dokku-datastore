# dokku-datastore

A re-implementation of the datastore codebases in golang.

## Building

```shell
# substitute the version number as desired
go build -ldflags "-X main.Version=0.1.0
```

## Usage

```
Usage: dokku-datastore [--version] [--help] <command> [<args>]

Available commands are:
    app-links    Lists all app links for a given app
    create       Creates a new datastore service
    destroy      Destroys a datastore service
    enter        Enters a service
    exists       Checks if a service exists
    expose       Exposes a service
    info         Gets information about a service
    list         Lists all services of a given datastore type
    start        Starts a service
    version      Return the version of the binary
```
