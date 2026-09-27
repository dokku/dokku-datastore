package internal

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mitchellh/cli"
)

// testUi is a ui whose text and json output can both be read back
func testUi(format string, quiet bool) (Ui, *cli.MockUi, *bytes.Buffer, *bytes.Buffer) {
	mock := cli.NewMockUi()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	return Ui{Ui: mock, Format: format, Quiet: quiet, Stdout: stdout, Stderr: stderr}, mock, stdout, stderr
}

// Quiet leaves out what a command says it is doing, and never what went wrong
// or what it was asked for.
func TestUiQuiet(t *testing.T) {
	ui, mock, _, _ := testUi("text", true)

	ui.Info("starting")
	ui.Warn(WarnInput{Warning: "careful"})
	ui.Error(ErrorInput{Error: errors.New("broken")})

	if output := mock.OutputWriter.String(); output != "" {
		t.Errorf("expected quiet to leave out the info, got %q", output)
	}

	errorOutput := mock.ErrorWriter.String()
	for _, expected := range []string{"careful", "broken"} {
		if !strings.Contains(errorOutput, expected) {
			t.Errorf("expected %q to be shown however quiet, got %q", expected, errorOutput)
		}
	}

	// the header is chrome, which quiet leaves out, and the header helpers
	// return before they need a terminal ui to print it with
	if err := ui.Header1("heading"); err != nil {
		t.Errorf("expected a quiet header to print nothing, got %s", err)
	}
	if err := ui.Header2("heading"); err != nil {
		t.Errorf("expected a quiet header to print nothing, got %s", err)
	}
}

func TestUiTextIsUnchanged(t *testing.T) {
	ui, mock, stdout, stderr := testUi("text", false)

	ui.Info("starting")
	ui.Warn(WarnInput{Warning: "careful"})
	ui.Error(ErrorInput{Error: errors.New("broken"), Message: "usage"})

	if output := mock.OutputWriter.String(); output != "starting\n" {
		t.Errorf("expected the info as text, got %q", output)
	}

	if output := mock.ErrorWriter.String(); output != "careful\nbroken\nusage\n" {
		t.Errorf("expected the warning and the error as text, got %q", output)
	}

	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("expected no json, got %q and %q", stdout, stderr)
	}
}

// Json is json and nothing else, and stdout is left for the document a command
// prints, so what it says on the way goes to stderr.
func TestUiJSON(t *testing.T) {
	ui, mock, stdout, stderr := testUi("json", false)

	ui.Info("starting")
	if err := ui.Header1("heading"); err != nil {
		t.Fatalf("unable to print the header: %s", err)
	}
	ui.Warn(WarnInput{Warning: "careful"})
	ui.Error(ErrorInput{Error: errors.New("broken"), Message: "usage"})

	if mock.OutputWriter.String() != "" || mock.ErrorWriter.String() != "" {
		t.Errorf("expected no text in json mode, got %q and %q", mock.OutputWriter, mock.ErrorWriter)
	}

	if stdout.Len() != 0 {
		t.Errorf("expected stdout to be left for the document, got %q", stdout)
	}

	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	expected := []map[string]string{
		{"message": "starting"},
		{"header1": "heading"},
		{"message": "careful"},
		{"message": "usage", "error": "broken"},
	}
	if len(lines) != len(expected) {
		t.Fatalf("expected %d json lines, got %q", len(expected), stderr)
	}

	for index, line := range lines {
		actual := map[string]string{}
		if err := json.Unmarshal([]byte(line), &actual); err != nil {
			t.Fatalf("expected json, got %q: %s", line, err)
		}

		for key, value := range expected[index] {
			if actual[key] != value {
				t.Errorf("expected %s to be %q in %q", key, value, line)
			}
		}
	}
}

func TestUiDocument(t *testing.T) {
	ui, _, stdout, _ := testUi("text", false)
	if err := ui.Document([]string{"a", "b"}, "a\nb\n"); err != nil {
		t.Fatalf("unable to print the document: %s", err)
	}
	if stdout.String() != "a\nb\n" {
		t.Errorf("expected the text as it is, got %q", stdout)
	}

	ui, _, stdout, _ = testUi("json", false)
	if err := ui.Document([]string{"a", "b"}, "a\nb\n"); err != nil {
		t.Fatalf("unable to print the document: %s", err)
	}
	if stdout.String() != "[\"a\",\"b\"]\n" {
		t.Errorf("expected the value as json, got %q", stdout)
	}

	// and quiet does not touch it, since it is what was asked for
	ui, _, stdout, _ = testUi("text", true)
	if err := ui.Document(nil, "a\n"); err != nil {
		t.Fatalf("unable to print the document: %s", err)
	}
	if stdout.String() != "a\n" {
		t.Errorf("expected quiet to leave the document alone, got %q", stdout)
	}
}

func TestErrorInputMarshalsTheErrorAsItsMessage(t *testing.T) {
	encoded, err := json.Marshal(ErrorInput{Error: errors.New("broken")})
	if err != nil {
		t.Fatalf("unable to encode: %s", err)
	}

	if string(encoded) != `{"error":"broken"}` {
		t.Errorf("expected the error's message, got %s", encoded)
	}
}
