package internal

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// cloneSource is a service with every setting a clone copies given a value, so
// that a setting dropped on the way through shows up as a difference
func cloneSource() serviceSettings {
	return serviceSettings{
		BackupMailto:       "ops@example.com",
		BackupStorageClass: "STANDARD_IA",
		BackupTimestamp:    "false",
		ConfigOptions:      "--appendonly yes",
		CustomEnv:          "ONE=1;TWO=2",
		ExportArgs:         "--hex-blob",
		ImportArgs:         "--force",
		InitialNetwork:     "initial",
		Keyserver:          "keys.example.com",
		Labels:             map[string]string{"traefik.enable": "true", "com.example.team": "data,ops"},
		LogDriver:          "json-file",
		LogOptions:         []string{"max-size=20m", "max-file=3"},
		Memory:             512,
		Mounts:             []service.Mount{{Source: "/srv/a", ContainerPath: "/opt/a", Readonly: true, VolumeOptions: "z"}},
		PostCreateNetworks: []string{"created-one", "created-two"},
		PostStartNetworks:  []string{"started"},
		RestartPolicy:      "unless-stopped",
		ServiceMemory:      "256",
		ShmSize:            "128m",
		VolumeTargets:      map[string]string{"data": "/redis-data"},
		WaitTimeout:        "120",
	}
}

// The bug this closes: a clone made without repeating every flag landed on the
// defaults rather than on what the source runs with.
func TestCloneKeepsTheSourceSettings(t *testing.T) {
	source := cloneSource()

	if actual := source.withOverrides(CloneServiceInput{}); !reflect.DeepEqual(actual, source) {
		t.Errorf("expected the source settings %+v, got %+v", source, actual)
	}
}

func TestCloneFlagOverridesOneSetting(t *testing.T) {
	policy := "no"
	timeout := "300"
	networks := []string{"elsewhere"}
	memory := 1024
	serviceMemory := "512"
	mounts := []service.Mount{{Source: "other", ContainerPath: "/opt/other"}}
	volumeTargets := map[string]string{"config": "/etc/redis"}
	labels := map[string]string{"com.example.tier": "gold"}

	tests := []struct {
		name     string
		input    CloneServiceInput
		expected func(*serviceSettings)
	}{
		{
			name:     "a string setting",
			input:    CloneServiceInput{RestartPolicy: &policy},
			expected: func(s *serviceSettings) { s.RestartPolicy = policy },
		},
		{
			name:     "the wait timeout",
			input:    CloneServiceInput{WaitTimeout: &timeout},
			expected: func(s *serviceSettings) { s.WaitTimeout = timeout },
		},
		{
			name:     "a list setting",
			input:    CloneServiceInput{PostStartNetworks: &networks},
			expected: func(s *serviceSettings) { s.PostStartNetworks = networks },
		},
		{
			name:     "the memory limit",
			input:    CloneServiceInput{Memory: &memory},
			expected: func(s *serviceSettings) { s.Memory = memory },
		},
		{
			name:     "the service memory",
			input:    CloneServiceInput{ServiceMemory: &serviceMemory},
			expected: func(s *serviceSettings) { s.ServiceMemory = serviceMemory },
		},
		{
			name:     "the mounts",
			input:    CloneServiceInput{Mounts: &mounts},
			expected: func(s *serviceSettings) { s.Mounts = mounts },
		},
		{
			name:     "the volume targets",
			input:    CloneServiceInput{VolumeTargets: &volumeTargets},
			expected: func(s *serviceSettings) { s.VolumeTargets = volumeTargets },
		},
		{
			// the whole set, the same way --volume replaces every mount
			name:     "the labels",
			input:    CloneServiceInput{Labels: &labels},
			expected: func(s *serviceSettings) { s.Labels = labels },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := cloneSource()
			test.expected(&expected)

			if actual := cloneSource().withOverrides(test.input); !reflect.DeepEqual(actual, expected) {
				t.Errorf("expected %+v, got %+v", expected, actual)
			}
		})
	}
}

// The reason the overrides are pointers: a flag given empty has to be able to
// clear what the source has rather than read as not given.
func TestCloneCanClearASetting(t *testing.T) {
	empty := ""
	none := []string{}
	unlimited := 0
	unmounted := []service.Mount{}
	unmoved := map[string]string{}
	unlabeled := map[string]string{}

	actual := cloneSource().withOverrides(CloneServiceInput{
		ConfigOptions:      &empty,
		CustomEnv:          &empty,
		InitialNetwork:     &empty,
		Labels:             &unlabeled,
		LogDriver:          &empty,
		LogOptions:         &none,
		Memory:             &unlimited,
		Mounts:             &unmounted,
		PostCreateNetworks: &none,
		PostStartNetworks:  &none,
		RestartPolicy:      &empty,
		ServiceMemory:      &empty,
		ShmSize:            &empty,
		VolumeTargets:      &unmoved,
		WaitTimeout:        &empty,
	})

	// the keyserver, the backup mailto, the storage class, the backup timestamp
	// and the extra arguments have no flag, so they are the settings that are
	// always copied
	expected := serviceSettings{
		BackupMailto:       "ops@example.com",
		BackupStorageClass: "STANDARD_IA",
		BackupTimestamp:    "false",
		ExportArgs:         "--hex-blob",
		ImportArgs:         "--force",
		Keyserver:          "keys.example.com",
		Labels:             unlabeled,
		LogOptions:         none,
		Mounts:             unmounted,
		PostCreateNetworks: none,
		PostStartNetworks:  none,
		VolumeTargets:      unmoved,
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %+v, got %+v", expected, actual)
	}
}

func TestReadServiceSettings(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	serviceFiles := service.Files(datastore, "lollipop")
	for filename, contents := range map[string]string{
		serviceFiles.ConfigOptions: "--appendonly yes",
		serviceFiles.Env:           "ONE=1\nTWO=2",
		serviceFiles.Memory:        "512",
		serviceFiles.ServiceMemory: "256",
		serviceFiles.ShmSize:       "128m",
	} {
		writeInfoFile(t, filename, contents)
	}

	for key, value := range map[string]string{
		"initial-network":                  "initial",
		"post-create-network":              "created-one,created-two",
		"post-start-network":               "started",
		service.BackupMailtoProperty:       "ops@example.com",
		service.BackupStorageClassProperty: "STANDARD_IA",
		service.BackupTimestampProperty:    "false",
		// not a setting a clone starts from, so reading it back would be a
		// difference from cloneSource
		service.BackupObjectNameProperty: "db/latest",
		service.KeyserverProperty:        "keys.example.com",
		service.LogDriverProperty:        "json-file",
		service.LogOptProperty:           "max-size=20m,max-file=3",
		service.RestartPolicyProperty:    "unless-stopped",
		service.VolumeTargetsProperty:    "data=/redis-data",
		service.WaitTimeoutProperty:      "120",
	} {
		if err := SetProperty(datastore, "lollipop", key, value); err != nil {
			t.Fatalf("failed to set the %s property: %s", key, err)
		}
	}

	// written directly, since set refuses them for redis, whose export and
	// import ignore them: what is read back is what is under test
	for key, value := range map[string]string{
		service.ExportArgsProperty: "--hex-blob",
		service.ImportArgsProperty: "--force",
	} {
		if err := common.PropertyWrite(datastore.Properties().CommandPrefix, "lollipop", key, value); err != nil {
			t.Fatalf("failed to write the %s property: %s", key, err)
		}
	}

	if err := service.WriteMounts(datastore, "lollipop", cloneSource().Mounts); err != nil {
		t.Fatalf("failed to write the mounts: %s", err)
	}

	if err := service.WriteLabels(datastore, "lollipop", cloneSource().Labels); err != nil {
		t.Fatalf("failed to write the labels: %s", err)
	}

	actual, err := readServiceSettings(datastore, "lollipop")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if expected := cloneSource(); !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %+v, got %+v", expected, actual)
	}
}

// A service that never set anything has nothing to copy, and a list property
// that was never written is no entries rather than one empty one.
func TestReadServiceSettingsOfAnUnsetService(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	actual, err := readServiceSettings(datastore, "lollipop")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if expected := (serviceSettings{}); !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected no settings, got %+v", actual)
	}
}

// Read as unlimited, a memory limit that could not be parsed would be quietly
// dropped from the clone.
func TestReadServiceSettingsRefusesAnUnreadableMemory(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	writeInfoFile(t, service.Files(datastore, "lollipop").Memory, "lots")

	if _, err := readServiceSettings(datastore, "lollipop"); err == nil {
		t.Error("expected an unreadable memory limit to be refused")
	}
}

// A source made before there was a service memory has no file for it, and is
// read as having none, which the clone is then made with rather than with the
// datastore's default: the clone runs as its source does.
func TestReadServiceSettingsReadsAMissingServiceMemoryAsNone(t *testing.T) {
	datastore := service.Datastores["redis"]
	withInfoService(t, datastore, "lollipop")

	settings, err := readServiceSettings(datastore, "lollipop")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if settings.ServiceMemory != "" {
		t.Errorf("expected no service memory, got %q", settings.ServiceMemory)
	}

	if actual := service.DefaultServiceMemory(service.Datastores["memcached"].Definition, &settings.ServiceMemory); actual != "" {
		t.Errorf("expected a clone of it to be made without one rather than with the default, got %q", actual)
	}
}

func TestCloneImage(t *testing.T) {
	postgres17 := service.Datastores["postgres"].ForImage("", "17.0")
	definition17 := postgres17.Definition
	if definition17.Name != "postgres-17" {
		t.Fatalf("expected the postgres-17 definition, got %s", definition17.Name)
	}

	recorded := service.RecordedImage{Image: "postgres", ImageVersion: "17.0"}

	tests := []struct {
		name         string
		recorded     service.RecordedImage
		image        string
		imageVersion string
		expected     service.RecordedImage
		expectedErr  bool
	}{
		{
			name:     "no flags keep the source's image",
			recorded: recorded,
			expected: recorded,
		},
		{
			name:         "a version given keeps the source's image",
			recorded:     recorded,
			imageVersion: "17.6",
			expected:     service.RecordedImage{Image: "postgres", ImageVersion: "17.6"},
		},
		{
			name:     "the source's image named again keeps its version",
			recorded: recorded,
			image:    "postgres",
			expected: recorded,
		},
		{
			name:     "the definition's image without a version takes the one it ships",
			recorded: service.RecordedImage{Image: "myorg/postgres", ImageVersion: "custom-3"},
			image:    "postgres",
			expected: service.RecordedImage{Image: "postgres", ImageVersion: definition17.DefaultImageVersion},
		},
		{
			// the version belongs to the repository that published it, so the
			// source's is no more applicable to another image than the
			// definition's is
			name:        "another image without a version is refused",
			recorded:    recorded,
			image:       "myorg/postgres",
			expectedErr: true,
		},
		{
			name:         "another image with a version runs it",
			recorded:     recorded,
			image:        "myorg/postgres",
			imageVersion: "custom-3",
			expected:     service.RecordedImage{Image: "myorg/postgres", ImageVersion: "custom-3"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := cloneImage(definition17, test.recorded, test.image, test.imageVersion)
			if test.expectedErr {
				if !errors.Is(err, service.ErrNoImageVersion) {
					t.Fatalf("expected ErrNoImageVersion, got %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if actual != test.expected {
				t.Errorf("expected %+v, got %+v", test.expected, actual)
			}
		})
	}
}

// A clone is placed on the definition its source is pinned to, so an image that
// would run on another one is refused rather than handed data it cannot read.
func TestCheckCloneDefinition(t *testing.T) {
	postgres := service.Datastores["postgres"]

	tests := []struct {
		name        string
		pinned      *service.Datastore
		recorded    service.RecordedImage
		clone       service.RecordedImage
		expectedErr bool
	}{
		{
			name:     "the source's image is allowed",
			pinned:   postgres.ForImage("", "17.0"),
			recorded: service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			clone:    service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
		},
		{
			name:     "inside a major is allowed",
			pinned:   postgres.ForImage("", "17.0"),
			recorded: service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			clone:    service.RecordedImage{Image: "postgres", ImageVersion: "17.11"},
		},
		{
			name:        "across a major is refused",
			pinned:      postgres.ForImage("", "17.0"),
			recorded:    service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			clone:       service.RecordedImage{Image: "postgres", ImageVersion: "18.6"},
			expectedErr: true,
		},
		{
			name:        "onto a flavor is refused",
			pinned:      postgres.ForImage("", "17.0"),
			recorded:    service.RecordedImage{Image: "postgres", ImageVersion: "17.0"},
			clone:       service.RecordedImage{Image: "pgvector/pgvector", ImageVersion: "0.8.6-pg17"},
			expectedErr: true,
		},
		{
			// created with pgvector/pgvector:pg17 before pgvector had
			// definitions, which placed it on postgres-18 and its data with it
			name:     "a pin older than its flavor is allowed",
			pinned:   postgres.ForImage("", "18.4"),
			recorded: service.RecordedImage{Image: "pgvector/pgvector", ImageVersion: "pg17"},
			clone:    service.RecordedImage{Image: "pgvector/pgvector", ImageVersion: "0.8.6-pg17"},
		},
		{
			// a tag that does not carry its major resolves to the newest
			// definition, which is not the one this source was placed on
			name:     "a source placed by name on a custom tag is allowed",
			pinned:   postgres.ForImage("", "17.0"),
			recorded: service.RecordedImage{Image: "myorg/postgres", ImageVersion: "custom-3"},
			clone:    service.RecordedImage{Image: "myorg/postgres", ImageVersion: "custom-4"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkCloneDefinition(test.pinned, "db", test.recorded, test.clone)
			if test.expectedErr && err == nil {
				t.Fatal("expected the clone to be refused")
			}
			if !test.expectedErr && err != nil {
				t.Fatalf("expected the clone to be allowed, got %v", err)
			}
			if err != nil && !strings.Contains(err.Error(), "upgrade db onto it first") {
				t.Errorf("expected the refusal to point at upgrade, got %v", err)
			}
		})
	}
}
