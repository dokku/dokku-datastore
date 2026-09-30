package internal

import (
	"io"
	"strings"
	"testing"
)

// Stopping and starting a running service is only done when it is asked for,
// either by --force or by an answer of yes, and anything else is a refusal
// that says how to go ahead
func TestConfirmRecreate(t *testing.T) {
	tests := []struct {
		name     string
		force    bool
		ask      func(string) (string, error)
		accepted bool
	}{
		{name: "forced", force: true, accepted: true},
		{name: "forced without anyone to ask", force: true, ask: nil, accepted: true},
		{name: "yes", ask: answer("y"), accepted: true},
		{name: "yes in full", ask: answer(" YES \n"), accepted: true},
		{name: "no", ask: answer("n")},
		{name: "nothing", ask: answer("")},
		{name: "something else", ask: answer("sure")},
		{name: "no input left", ask: func(string) (string, error) { return "", io.EOF }},
		{name: "nobody to ask"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			asked := ""
			ask := test.ask
			if ask != nil {
				ask = func(question string) (string, error) {
					asked = question
					return test.ask(question)
				}
			}

			err := ConfirmRecreate(ConfirmRecreateInput{
				ServiceName: "lollipop",
				Logger:      Ui{Quiet: true, Stderr: io.Discard, Stdout: io.Discard, Format: "json"},
				Force:       test.force,
				Ask:         ask,
			})

			if test.accepted && err != nil {
				t.Fatalf("expected the stop and start to go ahead, got %q", err)
			}

			if !test.accepted {
				if err == nil {
					t.Fatal("expected the stop and start to be refused")
				}

				if !strings.Contains(err.Error(), "nothing was changed") || !strings.Contains(err.Error(), "--force") {
					t.Errorf("expected the refusal to say nothing changed and how to go ahead, got %q", err)
				}
			}

			if test.force && asked != "" {
				t.Errorf("expected nothing to be asked when forced, got %q", asked)
			}

			if !test.force && test.ask != nil && !strings.Contains(asked, "Stop and start lollipop now?") {
				t.Errorf("expected to be asked about the service, got %q", asked)
			}
		})
	}
}

func answer(value string) func(string) (string, error) {
	return func(string) (string, error) {
		return value, nil
	}
}
