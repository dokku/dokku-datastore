package commands

// splitExtraArgs separates a command's own arguments from the ones given after
// a --, which are handed to the datastore's tool as they are. dash is where the
// flag set saw the --, -1 when there was none.
//
// Flag parsing has already dropped the -- itself, and stopped reading flags at
// it, so an argument after it such as --hex-blob is never read as one of the
// command's own.
func splitExtraArgs(args []string, dash int) ([]string, []string) {
	if dash < 0 || dash > len(args) {
		return args, nil
	}

	return args[:dash], args[dash:]
}
