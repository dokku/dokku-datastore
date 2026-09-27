package internal

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/mitchellh/cli"
)

// Ui is the UI wrapper for the CLI
type Ui struct {
	// Ui is the underlying UI implementation
	Ui cli.Ui
	// Format is the format to output the data in
	Format string
	// Quiet is whether to suppress output
	Quiet bool
	// Trace is whether to enable trace output
	Trace bool

	// Stdout and Stderr are where json is written, os.Stdout and os.Stderr
	// when unset
	Stdout io.Writer
	Stderr io.Writer
}

// stdout is where a document is written
func (u *Ui) stdout() io.Writer {
	if u.Stdout != nil {
		return u.Stdout
	}

	return os.Stdout
}

// stderr is where anything that is not the document is written in json mode,
// so that stdout holds the document and nothing else
func (u *Ui) stderr() io.Writer {
	if u.Stderr != nil {
		return u.Stderr
	}

	return os.Stderr
}

// ErrorInput is the input for the Error method
type ErrorInput struct {
	// Message is the error message
	Message string `json:"message,omitempty"`
	// Error is the error
	Error error `json:"error,omitempty"`
}

// MarshalJSON writes the error as its message. An error is an interface, and
// encoding one as it is gives {} whatever it says.
func (e ErrorInput) MarshalJSON() ([]byte, error) {
	encoded := struct {
		Message string `json:"message,omitempty"`
		Error   string `json:"error,omitempty"`
	}{Message: e.Message}
	if e.Error != nil {
		encoded.Error = e.Error.Error()
	}

	return json.Marshal(encoded)
}

// Error outputs an error message. It is shown however quiet the command was
// asked to be.
func (u *Ui) Error(input ErrorInput) {
	if u.Format == "json" {
		json.NewEncoder(u.stderr()).Encode(input) //nolint:errcheck
		return
	}

	errorMessage := input.Error.Error()
	lines := strings.Split(errorMessage, "\n")
	for _, line := range lines {
		u.Ui.Error(line)
	}

	if input.Message != "" {
		u.Ui.Error(input.Message)
	}
}

// Help outputs a help message
func (u *Ui) Help(message string) error {
	if u.Format == "json" {
		return json.NewEncoder(u.stdout()).Encode(map[string]string{"help": message})
	}

	u.Ui.Output(message)
	return nil
}

// Header1 outputs a header1 message
func (u *Ui) Header1(message string) error {
	if u.Quiet {
		return nil
	}

	if u.Format == "json" {
		return json.NewEncoder(u.stderr()).Encode(map[string]string{"header1": message})
	}

	logger, ok := u.Ui.(*command.ZerologUi)
	if !ok {
		return fmt.Errorf("failed to cast Ui to ZerologUi")
	}

	logger.LogHeader1(message)
	return nil
}

// Header2 outputs a header2 message. It is rendered the way Header1 is, which
// is how dokku_log_info2 rendered the messages the bash plugins printed with it.
func (u *Ui) Header2(message string) error {
	if u.Quiet {
		return nil
	}

	if u.Format == "json" {
		return json.NewEncoder(u.stderr()).Encode(map[string]string{"header2": message})
	}

	logger, ok := u.Ui.(*command.ZerologUi)
	if !ok {
		return fmt.Errorf("failed to cast Ui to ZerologUi")
	}

	logger.LogHeader1(message)
	return nil
}

// Info outputs an info message
func (u *Ui) Info(message string) {
	if u.Quiet {
		return
	}

	if u.Format == "json" {
		json.NewEncoder(u.stderr()).Encode(map[string]string{"message": message}) //nolint:errcheck
		return
	}

	u.Ui.Output(message)
}

// Table outputs a table of data. The rows are the data, so only the header is
// left out when quiet.
func (u *Ui) Table(header string, rows []string) error {
	if u.Format == "json" {
		return json.NewEncoder(u.stdout()).Encode(rows)
	}

	logger, ok := u.Ui.(*command.ZerologUi)
	if !ok {
		return fmt.Errorf("failed to cast Ui to ZerologUi")
	}

	if !u.Quiet {
		logger.LogHeader1(header)
	}

	for _, row := range rows {
		u.Ui.Output(row)
	}

	return nil
}

// Document writes a command's result: as json when asked for json, and as the
// text it has always been otherwise, byte for byte, since dokku reads some of
// these line by line.
func (u *Ui) Document(value any, text string) error {
	if u.Format == "json" {
		return json.NewEncoder(u.stdout()).Encode(value)
	}

	_, err := io.WriteString(u.stdout(), text)
	return err
}

// WarnInput is the input for the Warn method
type WarnInput struct {
	// Warning is the warning message
	Warning string `json:"message,omitempty"`
}

// Warn outputs a warning message. It is shown however quiet the command was
// asked to be.
func (u *Ui) Warn(input WarnInput) {
	if u.Format == "json" {
		json.NewEncoder(u.stderr()).Encode(input) //nolint:errcheck
		return
	}

	u.Ui.Warn(input.Warning)
}
