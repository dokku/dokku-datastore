package service

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dokku/dokku/plugins/common"
)

func TestParseLabels(t *testing.T) {
	tests := []struct {
		name     string
		specs    []string
		expected map[string]string
	}{
		{name: "none", specs: nil, expected: map[string]string{}},
		{name: "one", specs: []string{"traefik.enable=true"}, expected: map[string]string{"traefik.enable": "true"}},
		{
			// split on the first equals sign, so a value may hold one, and a
			// comma or a space is the value's own
			name:  "a value holding an equals sign, a comma and a space",
			specs: []string{"traefik.http.routers.a.rule=Host(`a`) && Query(`b=c`)", "traefik.http.routers.a.middlewares=auth,compress"},
			expected: map[string]string{
				"traefik.http.routers.a.rule":        "Host(`a`) && Query(`b=c`)",
				"traefik.http.routers.a.middlewares": "auth,compress",
			},
		},
		{name: "an empty value", specs: []string{"com.example.flag="}, expected: map[string]string{"com.example.flag": ""}},
		{
			// what --label "" is given, which asks for no labels at all
			name:     "an empty spec",
			specs:    []string{""},
			expected: map[string]string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			labels, err := ParseLabels(test.specs)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !reflect.DeepEqual(labels, test.expected) {
				t.Errorf("expected %v, got %v", test.expected, labels)
			}
		})
	}
}

func TestParseLabelsRefusesAMalformedLabel(t *testing.T) {
	tests := []struct {
		name     string
		specs    []string
		contains string
	}{
		{name: "no equals sign", specs: []string{"traefik.enable"}, contains: "<key>=<value>"},
		{name: "an empty key", specs: []string{"=true"}, contains: "must not be empty"},
		{name: "a key with a space", specs: []string{"com.example a=b"}, contains: "whitespace"},
		{name: "a key with a tab", specs: []string{"com.example\ta=b"}, contains: "whitespace"},
		{name: "a newline in the value", specs: []string{"com.example.a=b\nc"}, contains: "newline"},
		{name: "a key given twice", specs: []string{"com.example.a=1", "com.example.a=2"}, contains: "more than once"},
		{name: "the dokku key", specs: []string{"dokku=app"}, contains: "reserved"},
		{name: "the plugin's namespace", specs: []string{"dokku.service=postgres"}, contains: "reserved"},
		{name: "the published ports label", specs: []string{"dokku.service.published-ports=1234:6379"}, contains: "reserved"},
		{name: "dokku core's namespace", specs: []string{"com.dokku.app-name=lollipop"}, contains: "reserved"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseLabels(test.specs)
			if err == nil {
				t.Fatalf("expected %v to be refused", test.specs)
			}

			if !strings.Contains(err.Error(), test.contains) {
				t.Errorf("expected the error to say %q, got %q", test.contains, err)
			}
		})
	}
}

// Only the key is held to a namespace, so one that merely mentions dokku
// further along is the service's to set.
func TestValidateLabelAllowsAKeyOutsideDokkusNamespace(t *testing.T) {
	for _, key := range []string{"dokkuish", "com.example.dokku", "io.dokku.thing", "dokku-backup.schedule"} {
		if err := ValidateLabel(key, "value"); err != nil {
			t.Errorf("expected %s to be allowed, got %v", key, err)
		}
	}
}

func TestValidateLabels(t *testing.T) {
	if err := ValidateLabels(nil); err != nil {
		t.Errorf("expected no labels to be valid, got %v", err)
	}

	if err := ValidateLabels(map[string]string{"com.example.a": "b"}); err != nil {
		t.Errorf("expected a label to be valid, got %v", err)
	}

	err := ValidateLabels(map[string]string{"com.example.a": "b", "dokku.service": "redis"})
	if err == nil || !strings.Contains(err.Error(), "dokku.service") {
		t.Errorf("expected a reserved label to be refused by name, got %v", err)
	}
}

func TestFormatLabels(t *testing.T) {
	if formatted := FormatLabels(nil); formatted != "" {
		t.Errorf("expected no labels to report as empty, got %q", formatted)
	}

	// sorted by key, and the && of a traefik rule left as it was given rather
	// than escaped the way encoding/json escapes html by default
	formatted := FormatLabels(map[string]string{"b": "x,y z", "a": "Host(`a`) && Path(`/`)"})
	expected := "{\"a\":\"Host(`a`) && Path(`/`)\",\"b\":\"x,y z\"}"
	if formatted != expected {
		t.Errorf("expected %s, got %s", expected, formatted)
	}
}

func TestServiceLabelsRoundTrip(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	labels, err := ServiceLabels(redis, "lollipop")
	if err != nil || len(labels) != 0 {
		t.Fatalf("expected a service with no labels to report none, got %v and %v", labels, err)
	}

	written := map[string]string{"traefik.enable": "true", "traefik.http.routers.a.middlewares": "auth,compress"}
	if err := WriteLabels(redis, "lollipop", written); err != nil {
		t.Fatalf("failed to write the labels: %v", err)
	}

	read, err := ServiceLabels(redis, "lollipop")
	if err != nil {
		t.Fatalf("failed to read the labels: %v", err)
	}
	if !reflect.DeepEqual(read, written) {
		t.Errorf("expected %v, got %v", written, read)
	}

	if err := WriteLabels(redis, "lollipop", nil); err != nil {
		t.Fatalf("failed to clear the labels: %v", err)
	}

	if value := common.PropertyGet(redis.Properties().CommandPrefix, "lollipop", LabelsProperty); value != "" {
		t.Errorf("expected the property to be cleared rather than left empty, got %q", value)
	}

	read, err = ServiceLabels(redis, "lollipop")
	if err != nil || len(read) != 0 {
		t.Errorf("expected no labels once cleared, got %v and %v", read, err)
	}
}

// A property that is not the json it was written as is an error rather than no
// labels, so a container is never made without the labels it was given.
func TestServiceLabelsRefusesAnUnreadableProperty(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	if err := common.PropertyWrite(redis.Properties().CommandPrefix, "lollipop", LabelsProperty, "traefik.enable=true"); err != nil {
		t.Fatalf("failed to write the property: %v", err)
	}

	if _, err := ServiceLabels(redis, "lollipop"); err == nil || !strings.Contains(err.Error(), LabelsProperty) {
		t.Errorf("expected the property to be refused by name, got %v", err)
	}
}

func TestCommitServiceConfigWritesTheLabels(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	labels := map[string]string{"com.example.team": "data"}
	if err := CommitServiceConfig(CommitServiceConfigInput{
		Datastore:    redis,
		Image:        "redis",
		ImageVersion: "8.9.0",
		Labels:       labels,
		ServiceName:  "lollipop",
	}); err != nil {
		t.Fatalf("failed to commit the service config: %v", err)
	}

	read, err := ServiceLabels(redis, "lollipop")
	if err != nil {
		t.Fatalf("failed to read the labels: %v", err)
	}
	if !reflect.DeepEqual(read, labels) {
		t.Errorf("expected %v, got %v", labels, read)
	}
}
