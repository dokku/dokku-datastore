# dokku-datastore

An example cli tool for the `cli-skeleton` project. It switches the logger to use zerolog with "human-readable" for output. Other than configuring zerolog as the logger, it is equivalent to the `hello-world` example.

## Building

```shell
# substitute the version number as desired
go build -ldflags "-X main.Version=0.1.0
```

## Usage

```
Usage: dokku-datastore [--version] [--help] [--quiet] [--format json|text] <command> [<args>]

Available commands are:
    list       Lists all datastores
    version    Return the version of the binary
```
