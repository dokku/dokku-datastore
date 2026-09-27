package service

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/verb"
	"github.com/dokku/dokku/plugins/common"
)

func mysqlDatastore(t *testing.T) *Datastore {
	t.Helper()

	mysql, ok := Datastores["mysql"]
	if !ok {
		t.Fatal("expected mysql to be registered")
	}

	return mysql
}

func TestValidateExtraArgs(t *testing.T) {
	valid := []string{"", "--hex-blob", " --hex-blob --routines ", `--where="id > 1"`, `--where='cost > $5'`}
	for _, value := range valid {
		if err := ValidateExtraArgs(ExportArgsProperty, value); err != nil {
			t.Errorf("expected %q to be accepted, got %q", value, err)
		}
	}

	// an expansion is refused rather than expanded to nothing, which would pass
	// the tool an argument other than the one that was written, and anything
	// that is not a plain list of words is refused rather than run
	invalid := []string{`--where="id > 1`, "--where=$ID", "--where=${ID}", `--where="id > $ID"`, "--limit=$((1+1))", "$(id)", "--hex-blob; --routines", "--hex-blob > dump.sql"}
	for _, value := range invalid {
		err := ValidateExtraArgs(ExportArgsProperty, value)
		if err == nil {
			t.Errorf("expected %q to be refused, got no error", value)
			continue
		}

		if !strings.Contains(err.Error(), ExportArgsProperty) {
			t.Errorf("expected the error to name %s, got %q", ExportArgsProperty, err)
		}
	}
}

func TestExtraArgsVerb(t *testing.T) {
	tests := map[string]string{
		ExportArgsProperty:  "export",
		ImportArgsProperty:  "import",
		WaitTimeoutProperty: "",
	}

	for property, expected := range tests {
		if actual := ExtraArgsVerb(property); actual != expected {
			t.Errorf("expected %s to be read by %q, got %q", property, expected, actual)
		}
	}
}

// Arguments given for a run replace the property for that run alone, and the
// property is split the way a shell would split it.
func TestExtraArgsPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		verb     string
		property string
		given    []string
		expected []string
	}{
		{name: "nothing set", verb: "export"},
		{name: "the export property", verb: "export", property: `--hex-blob --where="id > 1"`, expected: []string{"--hex-blob", "--where=id > 1"}},
		{name: "the import property", verb: "import", property: "--max-allowed-packet=1G", expected: []string{"--max-allowed-packet=1G"}},
		{name: "given over the property", verb: "export", property: "--hex-blob", given: []string{"--routines"}, expected: []string{"--routines"}},
		{name: "given with nothing set", verb: "import", given: []string{"--force"}, expected: []string{"--force"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mysql := mysqlDatastore(t)
			withServiceRoot(t, mysql, "lollipop")
			t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

			if test.property != "" {
				if err := common.PropertyWrite(mysql.Properties().CommandPrefix, "lollipop", extraArgsProperties[test.verb], test.property); err != nil {
					t.Fatalf("failed to write the property: %v", err)
				}
			}

			actual, err := mysql.extraArgs("lollipop", test.verb, test.given)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if !slices.Equal(actual, test.expected) {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// Only export and import have a property, so nothing is read for anything else.
func TestExtraArgsForAVerbWithoutAProperty(t *testing.T) {
	actual, err := mysqlDatastore(t).extraArgs("lollipop", "connect", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(actual) != 0 {
		t.Errorf("expected no arguments, got %q", actual)
	}
}

func TestAcceptsExtraArgs(t *testing.T) {
	for _, name := range []string{"export", "import"} {
		if !mysqlDatastore(t).AcceptsExtraArgs(name) {
			t.Errorf("expected mysql %s to take extra arguments", name)
		}

		if redisDatastore(t).AcceptsExtraArgs(name) {
			t.Errorf("expected redis %s not to take extra arguments", name)
		}
	}

	if mysqlDatastore(t).AcceptsExtraArgs("connect") {
		t.Error("expected mysql connect not to take extra arguments")
	}
}

// redis dumps and loads with scripts that ignore their argv, so arguments for
// either are refused before anything is run, whether they were given for the
// run or kept on the service.
func TestRedisRefusesExtraArgs(t *testing.T) {
	tests := []struct {
		name     string
		verb     string
		property string
		given    []string
	}{
		{name: "export given", verb: "export", given: []string{"--anything"}},
		{name: "import given", verb: "import", given: []string{"--anything"}},
		{name: "export property", verb: "export", property: "--anything"},
		{name: "import property", verb: "import", property: "--anything"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			redis := redisDatastore(t)
			serviceRoot := withServiceRoot(t, redis, "lollipop")
			writeRecord(t, serviceRoot, "redis", "8.8.0")
			t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

			if test.property != "" {
				if err := common.PropertyWrite(redis.Properties().CommandPrefix, "lollipop", extraArgsProperties[test.verb], test.property); err != nil {
					t.Fatalf("failed to write the property: %v", err)
				}
			}

			var err error
			switch test.verb {
			case "export":
				err = redis.ExportService(context.Background(), ExportServiceInput{
					ServiceName: "lollipop",
					Writer:      io.Discard,
					ExtraArgs:   test.given,
				})
			case "import":
				err = redis.ImportService(context.Background(), ImportServiceInput{
					ServiceName: "lollipop",
					Reader:      strings.NewReader(""),
					ExtraArgs:   test.given,
				})
			}

			var refused verb.ErrExtraArgsRefused
			if !errors.As(err, &refused) {
				t.Fatalf("expected a refusal, got %v", err)
			}

			if expected := "redis " + test.verb + " does not take extra arguments"; err.Error() != expected {
				t.Errorf("expected %q, got %q", expected, err)
			}
		})
	}
}
