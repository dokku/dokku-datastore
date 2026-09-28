package service

import (
	"strings"
	"testing"

	"github.com/dokku/dokku/plugins/common"
)

func TestValidatePortBindAddress(t *testing.T) {
	for _, value := range []string{"", "10.0.0.5", "0.0.0.0", "::1", "2001:db8::1"} {
		if err := ValidatePortBindAddress(value); err != nil {
			t.Errorf("expected %q to be accepted, got %q", value, err)
		}
	}

	// a hostname is not an address docker can publish on, the brackets belong to
	// a port, and a zone names an interface of the host rather than an address
	for _, value := range []string{"localhost", "example.com", "[::1]", "10.0.0.5:6379", "10.0.0.0/8", "fe80::1%eth0", "a.b.c.d"} {
		err := ValidatePortBindAddress(value)
		if err == nil {
			t.Errorf("expected %q to be refused", value)
			continue
		}

		if !strings.Contains(err.Error(), PortBindAddressProperty) {
			t.Errorf("expected the error to name %s, got %q", PortBindAddressProperty, err)
		}
	}
}

func TestValidateExposeSourceRange(t *testing.T) {
	for _, value := range []string{"", "10.0.0.0/8", "10.1.2.3/8", "192.0.2.1", "0.0.0.0/0", "2001:db8::/32", "::1"} {
		if err := ValidateExposeSourceRange(value); err != nil {
			t.Errorf("expected %q to be accepted, got %q", value, err)
		}
	}

	for _, value := range []string{"localhost", "10.0.0.0/33", "10.0.0.0/", "a.b.c.d", "10.0.0.0/8,192.168.0.0/16", "10.0.0.0/8 192.168.0.0/16", "[::1]"} {
		err := ValidateExposeSourceRange(value)
		if err == nil {
			t.Errorf("expected %q to be refused", value)
			continue
		}

		if !strings.Contains(err.Error(), "must be a single IP address or CIDR") {
			t.Errorf("expected the error to say what is taken, got %q", err)
		}
	}
}

func TestServiceExposeSettings(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	if address, sourceRange := ServicePortBindAddress(redis, "lollipop"), ServiceExposeSourceRange(redis, "lollipop"); address != "" || sourceRange != "" {
		t.Fatalf("expected nothing to be set, got %q and %q", address, sourceRange)
	}

	commandPrefix := redis.Properties().CommandPrefix
	if err := common.PropertyWrite(commandPrefix, "lollipop", PortBindAddressProperty, "10.0.0.5"); err != nil {
		t.Fatalf("failed to write the property: %v", err)
	}
	if err := common.PropertyWrite(commandPrefix, "lollipop", ExposeSourceRangeProperty, "10.0.0.0/8"); err != nil {
		t.Fatalf("failed to write the property: %v", err)
	}

	if address := ServicePortBindAddress(redis, "lollipop"); address != "10.0.0.5" {
		t.Errorf("expected the address to be read back, got %q", address)
	}
	if sourceRange := ServiceExposeSourceRange(redis, "lollipop"); sourceRange != "10.0.0.0/8" {
		t.Errorf("expected the range to be read back, got %q", sourceRange)
	}

	if settings := serviceAmbassadorSettings(redis, "lollipop"); settings != (ambassadorSettings{Address: "10.0.0.5", SourceRange: "10.0.0.0/8"}) {
		t.Errorf("expected the ambassador to be made with both, got %+v", settings)
	}
}

func TestValidateExposeHost(t *testing.T) {
	for _, value := range []string{"", "db.example.com", "localhost", "my-host", "10.0.0.5", "::1", "2001:db8::1", strings.Repeat("a", 63) + ".com"} {
		if err := ValidateExposeHost(value); err != nil {
			t.Errorf("expected %q to be accepted, got %q", value, err)
		}
	}

	// the port comes from the exposed ports, so a value carrying one of its own,
	// a scheme or anything else that is not a host is refused rather than put
	// into the dsn as it is
	for _, value := range []string{"db.example.com:5432", "postgres://db.example.com", "bad_host", "-a.com", "a-.com", "a..com", ".com", strings.Repeat("a", 64) + ".com", "db example.com", "[::1]", "fe80::1%eth0", "db.example.com/path"} {
		err := ValidateExposeHost(value)
		if err == nil {
			t.Errorf("expected %q to be refused", value)
			continue
		}

		if !strings.Contains(err.Error(), ExposeHostProperty) {
			t.Errorf("expected the error to name %s, got %q", ExposeHostProperty, err)
		}
	}
}
