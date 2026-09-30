package internal

import (
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

func TestSetPropertyRejectsUnknownKeys(t *testing.T) {
	datastore := service.Datastores["redis"]

	// an unknown key must be refused before anything is written, and the message
	// has to name the keys that are accepted
	err := SetProperty(datastore, "lollipop", "not-a-property", "value")
	if err == nil {
		t.Fatal("expected an error for an unknown key, got none")
	}

	expected := "Invalid key specified, valid keys include: initial-network, post-create-network, post-start-network, backup-keyserver, backup-storage-class, log-driver, log-opt, restart-policy, wait-timeout, port-bind-address, port-source-range, expose-host, expose-mode, export-args, import-args, volume-targets"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err)
	}
}

func TestSettableProperties(t *testing.T) {
	// the three the bash datastore plugins accept, the keyserver the backup image
	// is told to fetch a public key from and the storage class it uploads with,
	// which have nowhere else to be set, and
	// the two that bound a container's log, the policy docker restarts it by, how
	// long it is waited on to become ready, where and to whom an exposed service
	// is published, the host its exposed dsn names, whether it is published
	// through an ambassador or directly, and the arguments its exports
	// and imports are run with, and where the definition's volumes are mounted
	expected := []string{"initial-network", "post-create-network", "post-start-network", "backup-keyserver", "backup-storage-class", "log-driver", "log-opt", "restart-policy", "wait-timeout", "port-bind-address", "port-source-range", "expose-host", "expose-mode", "export-args", "import-args", "volume-targets"}
	if strings.Join(SettableProperties, ",") != strings.Join(expected, ",") {
		t.Errorf("expected %v, got %v", expected, SettableProperties)
	}
}

// The property name is written in two places that have to agree: the list that
// makes it settable, and the read in the backup path.
func TestKeyserverPropertyIsSettable(t *testing.T) {
	if !slices.Contains(SettableProperties, service.KeyserverProperty) {
		t.Errorf("expected %s to be settable, got %v", service.KeyserverProperty, SettableProperties)
	}

	// the message listing valid keys is built from the slice, so it says so
	if !strings.Contains(InvalidPropertyError().Error(), service.KeyserverProperty) {
		t.Errorf("expected the error to list it, got %q", InvalidPropertyError())
	}
}

// The log properties are the first whose value is checked rather than only
// whose key is, because docker refuses to make a container from a malformed one
// and the service would be left unable to start by a command that said it had
// worked.
func TestSetPropertyRejectsAnUnusableValue(t *testing.T) {
	datastore := service.Datastores["redis"]

	tests := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{
			// the aws cli in the backup image matches the name exactly
			name:     "a storage class in lowercase",
			key:      service.BackupStorageClassProperty,
			value:    "standard_ia",
			expected: `invalid backup-storage-class value "standard_ia"`,
		},
		{
			name:     "a storage class s3 does not have",
			key:      service.BackupStorageClassProperty,
			value:    "NOT_A_CLASS",
			expected: `invalid backup-storage-class value "NOT_A_CLASS"`,
		},
		{
			name:     "a storage class with a space in it",
			key:      service.BackupStorageClassProperty,
			value:    "STANDARD IA",
			expected: `invalid backup-storage-class value "STANDARD IA"`,
		},
		{
			name:     "a driver that is not a name",
			key:      service.LogDriverProperty,
			value:    "not a driver",
			expected: `invalid log-driver value "not a driver"`,
		},
		{
			name:     "an option that is not a pair",
			key:      service.LogOptProperty,
			value:    "max-size",
			expected: `invalid log-opt entry "max-size"`,
		},
		{
			name:     "a max-size with no unit",
			key:      service.LogOptProperty,
			value:    "max-size=20",
			expected: `invalid max-size value "20"`,
		},
		{
			name:     "a max-file that is not a count",
			key:      service.LogOptProperty,
			value:    "max-size=20m,max-file=none",
			expected: `invalid max-file value "none"`,
		},
		{
			name:     "a restart policy docker does not have",
			key:      service.RestartPolicyProperty,
			value:    "sometimes",
			expected: `invalid restart-policy value "sometimes"`,
		},
		{
			// accepted by dokku core, and refused by docker when the container is made
			name:     "a retry count that is not a count",
			key:      service.RestartPolicyProperty,
			value:    "on-failure:abc",
			expected: `invalid restart-policy value "on-failure:abc"`,
		},
		{
			name:     "a wait timeout that is not a number",
			key:      service.WaitTimeoutProperty,
			value:    "forever",
			expected: `invalid wait-timeout value "forever"`,
		},
		{
			// clearing the setting is done by giving no value, not a zero
			name:     "a wait timeout of zero",
			key:      service.WaitTimeoutProperty,
			value:    "0",
			expected: `invalid wait-timeout value "0"`,
		},
		{
			// docker publishes on an address, not a name
			name:     "a port bind address that is a hostname",
			key:      service.PortBindAddressProperty,
			value:    "localhost",
			expected: `invalid port-bind-address value "localhost"`,
		},
		{
			// the brackets belong to a port, not to an address
			name:     "a port bind address in brackets",
			key:      service.PortBindAddressProperty,
			value:    "[::1]",
			expected: `invalid port-bind-address value "[::1]"`,
		},
		{
			name:     "a port bind address that is not an address",
			key:      service.PortBindAddressProperty,
			value:    "a.b.c.d",
			expected: `invalid port-bind-address value "a.b.c.d"`,
		},
		{
			name:     "a source range with too many bits",
			key:      service.PortSourceRangeProperty,
			value:    "10.0.0.0/33",
			expected: `invalid port-source-range value "10.0.0.0/33"`,
		},
		{
			name:     "a source range that is not an address",
			key:      service.PortSourceRangeProperty,
			value:    "a.b.c.d",
			expected: `invalid port-source-range value "a.b.c.d"`,
		},
		{
			// socat, which enforces it, honors one range per port
			name:     "more than one source range",
			key:      service.PortSourceRangeProperty,
			value:    "10.0.0.0/8,192.168.0.0/16",
			expected: `invalid port-source-range value "10.0.0.0/8,192.168.0.0/16"`,
		},
		{
			// the port comes from the exposed ports
			name:     "an expose host with a port",
			key:      service.ExposeHostProperty,
			value:    "db.example.com:5432",
			expected: `invalid expose-host value "db.example.com:5432"`,
		},
		{
			name:     "an expose host that is a url",
			key:      service.ExposeHostProperty,
			value:    "postgres://db.example.com",
			expected: `invalid expose-host value "postgres://db.example.com"`,
		},
		{
			name:     "an expose host that is not a hostname",
			key:      service.ExposeHostProperty,
			value:    "bad_host",
			expected: `invalid expose-host value "bad_host"`,
		},
		{
			name:     "an expose mode that is not one",
			key:      service.ExposeModeProperty,
			value:    "host",
			expected: `invalid expose-mode value "host"`,
		},
		{
			name:     "export arguments with an unterminated quote",
			key:      service.ExportArgsProperty,
			value:    `--where="id > 1`,
			expected: `invalid export-args value`,
		},
		{
			// expanded to nothing, it would hand the tool some other argument
			name:     "import arguments naming a variable",
			key:      service.ImportArgsProperty,
			value:    "--init-command=$SQL",
			expected: `invalid import-args value`,
		},
		{
			name:     "a volume target that is not a pair",
			key:      service.VolumeTargetsProperty,
			value:    "/redis-data",
			expected: `invalid volume-targets value "/redis-data"`,
		},
		{
			name:     "a volume target that is relative",
			key:      service.VolumeTargetsProperty,
			value:    "data=redis-data",
			expected: `must be absolute`,
		},
		{
			name:     "a volume target at the container root",
			key:      service.VolumeTargetsProperty,
			value:    "data=/",
			expected: `must not be mounted at /`,
		},
		{
			// a volume the definition does not mount cannot be moved
			name:     "a volume target for a volume redis does not have",
			key:      service.VolumeTargetsProperty,
			value:    "certs=/certs",
			expected: `has no volume certs, must be one of [config, data]`,
		},
		{
			name:     "a volume target onto another volume",
			key:      service.VolumeTargetsProperty,
			value:    "data=/usr/local/etc/redis",
			expected: `would both be mounted at /usr/local/etc/redis`,
		},
		{
			name:     "a volume target onto the payload",
			key:      service.VolumeTargetsProperty,
			value:    "data=/usr/local/bin",
			expected: `which holds the /usr/local/bin/dokku-redis-export`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := SetProperty(datastore, "lollipop", test.key, test.value)
			if err == nil {
				t.Fatalf("expected %s=%s to be refused, got no error", test.key, test.value)
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected the error to contain %q, got %q", test.expected, err)
			}
		})
	}
}

// Unsetting is how every other property is cleared, so an empty value has to
// reach the delete rather than being refused as an unusable one.
func TestSetPropertyAcceptsAnEmptyValue(t *testing.T) {
	for _, key := range []string{service.BackupStorageClassProperty, service.LogDriverProperty, service.LogOptProperty, service.RestartPolicyProperty, service.PortBindAddressProperty, service.PortSourceRangeProperty, service.ExposeHostProperty, service.ExposeModeProperty, service.ExportArgsProperty, service.ImportArgsProperty, service.VolumeTargetsProperty} {
		if err := ValidatePropertyValue(key, ""); err != nil {
			t.Errorf("expected an empty %s to be accepted, got %q", key, err)
		}
	}
}

// Extra arguments are kept for a datastore whose tools read them, and refused
// for one whose would drop them, so that a backup is not the first thing to
// find out the setting does nothing. Clearing one is always allowed.
func TestSetPropertyExtraArgs(t *testing.T) {
	for _, key := range []string{service.ExportArgsProperty, service.ImportArgsProperty} {
		t.Run(key, func(t *testing.T) {
			mysql := service.Datastores["mysql"]
			withInfoService(t, mysql, "lollipop")

			if err := SetProperty(mysql, "lollipop", key, `--hex-blob --where="id > 1"`); err != nil {
				t.Fatalf("expected mysql to keep %s, got %q", key, err)
			}

			if actual := service.ServiceExtraArgs(mysql, "lollipop", key); actual != `--hex-blob --where="id > 1"` {
				t.Errorf("expected the value as it was written, got %q", actual)
			}

			redis := service.Datastores["redis"]
			withInfoService(t, redis, "lollipop")

			err := SetProperty(redis, "lollipop", key, "--anything")
			if err == nil {
				t.Fatalf("expected redis to refuse %s, got no error", key)
			}

			if !strings.Contains(err.Error(), "does not take extra arguments") {
				t.Errorf("expected the refusal to say why, got %q", err)
			}

			if actual := service.ServiceExtraArgs(redis, "lollipop", key); actual != "" {
				t.Errorf("expected nothing to be written, got %q", actual)
			}

			if err := SetProperty(redis, "lollipop", key, ""); err != nil {
				t.Errorf("expected clearing %s to be allowed, got %q", key, err)
			}
		})
	}
}

// The targets are stored the way info reports them, so the same targets always
// read the same however they were written, and a value with nothing in it is
// no targets rather than an empty one
func TestSetVolumeTargetsIsStoredCanonically(t *testing.T) {
	redis := service.Datastores["redis"]
	withInfoService(t, redis, "lollipop")

	if err := SetProperty(redis, "lollipop", service.VolumeTargetsProperty, "  data=/redis-data//   config=/etc/redis "); err != nil {
		t.Fatalf("expected the targets to be accepted, got %q", err)
	}

	info := Info(t.Context(), InfoInput{Datastore: redis, ServiceName: "lollipop"})
	if actual := info[service.VolumeTargetsProperty]; actual != "config=/etc/redis data=/redis-data" {
		t.Errorf("expected the targets sorted and cleaned, got %q", actual)
	}

	if err := SetProperty(redis, "lollipop", service.VolumeTargetsProperty, ""); err != nil {
		t.Fatalf("expected clearing the targets to be allowed, got %q", err)
	}

	targets, err := service.ServiceVolumeTargets(redis, "lollipop")
	if err != nil || targets != nil {
		t.Errorf("expected no targets once cleared, got %v and %v", targets, err)
	}
}

// A volume moved onto a path a mount already holds would give docker two
// mounts at one path, and the container could not be made
func TestSetVolumeTargetsRefusesAMountedPath(t *testing.T) {
	redis := service.Datastores["redis"]
	withInfoService(t, redis, "lollipop")

	if err := service.WriteMounts(redis, "lollipop", []service.Mount{{Source: "some-volume", ContainerPath: "/opt/extra"}}); err != nil {
		t.Fatalf("failed to write the mounts: %v", err)
	}

	err := SetProperty(redis, "lollipop", service.VolumeTargetsProperty, "data=/opt/extra")
	if err == nil {
		t.Fatal("expected a volume moved onto a mount to be refused")
	}

	if !strings.Contains(err.Error(), "Container path /opt/extra is already mounted by the redis definition") {
		t.Errorf("expected the refusal to name the path, got %q", err)
	}

	targets, err := service.ServiceVolumeTargets(redis, "lollipop")
	if err != nil || targets != nil {
		t.Errorf("expected nothing to be written, got %v and %v", targets, err)
	}
}

// A service published directly has no ambassador to hold its clients to a
// range, so the two are refused together whichever is set second, and
// clearing either one is always allowed
func TestSetExposeModeRefusesASourceRange(t *testing.T) {
	redis := service.Datastores["redis"]
	withInfoService(t, redis, "lollipop")

	if err := SetProperty(redis, "lollipop", service.PortSourceRangeProperty, "10.0.0.0/8"); err != nil {
		t.Fatalf("expected the range to be accepted, got %q", err)
	}

	err := SetProperty(redis, "lollipop", service.ExposeModeProperty, service.ExposeModeDirect)
	if err == nil {
		t.Fatal("expected the direct mode to be refused alongside a source range")
	}

	if !strings.Contains(err.Error(), "cannot be enforced") {
		t.Errorf("expected the refusal to say why, got %q", err)
	}

	if mode := service.ServiceExposeMode(redis, "lollipop"); mode != service.ExposeModeAmbassador {
		t.Errorf("expected nothing to be written, got %q", mode)
	}

	if err := SetProperty(redis, "lollipop", service.ExposeModeProperty, service.ExposeModeAmbassador); err != nil {
		t.Errorf("expected the ambassador to be accepted alongside a source range, got %q", err)
	}

	if err := SetProperty(redis, "lollipop", service.PortSourceRangeProperty, ""); err != nil {
		t.Fatalf("expected clearing the range to be allowed, got %q", err)
	}

	if err := SetProperty(redis, "lollipop", service.ExposeModeProperty, service.ExposeModeDirect); err != nil {
		t.Fatalf("expected the direct mode to be accepted without a source range, got %q", err)
	}

	err = SetProperty(redis, "lollipop", service.PortSourceRangeProperty, "10.0.0.0/8")
	if err == nil {
		t.Fatal("expected a source range to be refused for a service exposed directly")
	}

	if actual := service.ServicePortSourceRange(redis, "lollipop"); actual != "" {
		t.Errorf("expected nothing to be written, got %q", actual)
	}

	if err := SetProperty(redis, "lollipop", service.ExposeModeProperty, ""); err != nil {
		t.Errorf("expected clearing the mode to be allowed, got %q", err)
	}
}
