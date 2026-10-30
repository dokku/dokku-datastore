package main

import (
	"os"

	"github.com/dokku/dokku-datastore/sdk"
)

// The name of the cli tool
var AppName = sdk.AppName

// Holds the version
var Version string

func main() {
	os.Exit(Run(os.Args[1:]))
}

// Executes the specified subcommand
func Run(args []string) int {
	return sdk.Run(sdk.RunInput{
		AppName: AppName,
		Args:    args,
		Version: Version,
	})
}
