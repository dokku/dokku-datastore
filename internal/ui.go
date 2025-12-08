package internal

import (
	"encoding/json"
	"fmt"
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
}

// ErrorInput is the input for the Error method
type ErrorInput struct {
	// Message is the error message
	Message string `json:"message,omitempty"`
	// Error is the error
	Error error `json:"error,omitempty"`
}

// Error outputs an error message
func (u *Ui) Error(input ErrorInput) {
	if u.Format == "json" {
		json.NewEncoder(os.Stderr).Encode(input) //nolint:errcheck
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
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"help": message})
	}

	u.Ui.Output(message)
	return nil
}

// Header1 outputs a header1 message
func (u *Ui) Header1(message string) error {
	if u.Format == "json" {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"header1": message})
	}

	logger, ok := u.Ui.(*command.ZerologUi)
	if !ok {
		return fmt.Errorf("failed to cast Ui to ZerologUi")
	}

	logger.LogHeader1(message)
	return nil
}

// Header1 outputs a header1 message
func (u *Ui) Header2(message string) error {
	if u.Format == "json" {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"header2": message})
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
	if u.Format == "json" {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"message": message}) //nolint:errcheck
	}

	u.Ui.Output(message)
}

// Table outputs a table of data
func (u *Ui) Table(header string, rows []string) error {
	if u.Format == "json" {
		return json.NewEncoder(os.Stdout).Encode(rows)
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

// WarnInput is the input for the Warn method
type WarnInput struct {
	// Warning is the warning message
	Warning string `json:"message,omitempty"`
}

// Warn outputs a warning message
func (u *Ui) Warn(input WarnInput) {
	if u.Format == "json" {
		json.NewEncoder(os.Stderr).Encode(input) //nolint:errcheck
	}

	u.Ui.Warn(input.Warning)
}
