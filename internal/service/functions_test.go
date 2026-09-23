package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateServiceName(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		expectedErr error
	}{
		{
			name:        "empty name",
			serviceName: "",
			expectedErr: ErrMissingServiceName,
		},
		{
			name:        "name with a period",
			serviceName: "d.erp",
			expectedErr: ErrInvalidServiceName,
		},
		{
			name:        "name with a space",
			serviceName: "my service",
			expectedErr: ErrInvalidServiceName,
		},
		{
			name:        "simple name",
			serviceName: "lollipop",
		},
		{
			name:        "name with dashes",
			serviceName: "service-with-dashes",
		},
		{
			name:        "name with underscores",
			serviceName: "service_with_underscores",
		},
		{
			name:        "name with digits",
			serviceName: "redis2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateServiceName(test.serviceName)
			if test.expectedErr == nil {
				if err != nil {
					t.Fatalf("expected no error, got %q", err)
				}
				return
			}

			if !errors.Is(err, test.expectedErr) {
				t.Errorf("expected error %q, got %q", test.expectedErr, err)
			}
		})
	}
}

// redisDatastore is the datastore the image tests are written against. Redis
// has one definition, so what it pins is unambiguous.
func redisDatastore(t *testing.T) *Datastore {
	t.Helper()

	redis, ok := Datastores["redis"]
	if !ok {
		t.Fatal("expected redis to be registered")
	}

	return redis
}

// writeRecord puts an IMAGE and an IMAGE_VERSION in a service root, writing
// only the halves it is given. The contents go in verbatim, because how a
// partly written or multi line file is read is exactly what is under test.
func writeRecord(t *testing.T, serviceRoot string, image string, imageVersion string) {
	t.Helper()

	files := map[string]string{"IMAGE": image, "IMAGE_VERSION": imageVersion}
	for name, content := range files {
		if content == "" {
			continue
		}

		if err := os.WriteFile(filepath.Join(serviceRoot, name), []byte(content), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}
}

// A service runs what it recorded. The definition's default only stands in for
// a half it did not record, and an override only for a caller that named one -
// which is create and upgrade, and nothing else.
func TestImageForService(t *testing.T) {
	redis := redisDatastore(t)
	defaultImage := redis.Definition.DefaultImage
	// read off the definition rather than written out, because an image bump
	// changes it and a copy here would fail every one of them
	defaultVersion := redis.Definition.DefaultImageVersion

	tests := []struct {
		name                 string
		image                string
		imageVersion         string
		imageOverride        string
		imageVersionOverride string
		expected             string
	}{
		{
			name:         "both halves recorded",
			image:        "redis",
			imageVersion: "8.9.0",
			expected:     "redis:8.9.0",
		},
		{
			name:         "only the version recorded",
			imageVersion: "8.9.0",
			expected:     defaultImage + ":8.9.0",
		},
		{
			name:     "only the image recorded",
			image:    "redis/redis-stack-server",
			expected: "redis/redis-stack-server:" + defaultVersion,
		},
		{
			name:     "nothing recorded",
			expected: defaultImage + ":" + defaultVersion,
		},
		{
			name:         "a trailing newline is not part of the version",
			image:        "redis\n",
			imageVersion: "8.9.0\n",
			expected:     "redis:8.9.0",
		},
		{
			// the reader this replaced trimmed the whole file, so a second line
			// ended up inside the reference and docker could not resolve it
			name:         "a second line is ignored",
			image:        "redis\nnonsense\n",
			imageVersion: "8.9.0\nnonsense\n",
			expected:     "redis:8.9.0",
		},
		{
			name:                 "an override beats the record",
			image:                "redis",
			imageVersion:         "8.9.0",
			imageOverride:        "redis/redis-stack-server",
			imageVersionOverride: "7.2.0",
			expected:             "redis/redis-stack-server:7.2.0",
		},
		{
			name:                 "an override beats the default",
			imageVersionOverride: "7.2.0",
			expected:             defaultImage + ":7.2.0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceRoot := withServiceRoot(t, redis, "lollipop")
			writeRecord(t, serviceRoot, test.image, test.imageVersion)

			actual, err := ImageForService(ImageForServiceInput{
				Datastore:            redis,
				ServiceName:          "lollipop",
				ImageOverride:        test.imageOverride,
				ImageVersionOverride: test.imageVersionOverride,
			})
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

// There used to be two answers to this question, read out of the same two files
// two different ways, and on a file with a second line they disagreed. One
// resolver now, and this is what keeps it that way.
func TestTaggedImageAgreesWithImageForService(t *testing.T) {
	redis := redisDatastore(t)

	records := []struct {
		name         string
		image        string
		imageVersion string
	}{
		{name: "both halves recorded", image: "redis", imageVersion: "8.9.0"},
		{name: "nothing recorded"},
		{name: "only the version recorded", imageVersion: "8.9.0"},
		{name: "a second line", image: "redis\nnonsense\n", imageVersion: "8.9.0\nnonsense\n"},
	}

	for _, record := range records {
		t.Run(record.name, func(t *testing.T) {
			serviceRoot := withServiceRoot(t, redis, "lollipop")
			writeRecord(t, serviceRoot, record.image, record.imageVersion)

			resolved, err := ImageForService(ImageForServiceInput{
				Datastore:   redis,
				ServiceName: "lollipop",
			})
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if actual := redis.taggedImage("lollipop"); actual != resolved {
				t.Errorf("taggedImage answered %q where ImageForService answered %q", actual, resolved)
			}
		})
	}
}

func TestReadRecordedImage(t *testing.T) {
	redis := redisDatastore(t)

	tests := []struct {
		name                 string
		image                string
		imageVersion         string
		expectedImage        string
		expectedImageVersion string
		expectedComplete     bool
	}{
		{
			name:                 "both halves",
			image:                "redis",
			imageVersion:         "8.9.0",
			expectedImage:        "redis",
			expectedImageVersion: "8.9.0",
			expectedComplete:     true,
		},
		{
			name: "no files at all",
		},
		{
			name:          "an empty version file",
			image:         "redis",
			imageVersion:  "",
			expectedImage: "redis",
		},
		{
			// read as saying nothing, the same as a file that is not there
			name:          "a whitespace only version file",
			image:         "redis",
			imageVersion:  "   \n",
			expectedImage: "redis",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceRoot := withServiceRoot(t, redis, "lollipop")
			writeRecord(t, serviceRoot, test.image, test.imageVersion)

			recorded := ReadRecordedImage(redis, "lollipop")
			if recorded.Image != test.expectedImage {
				t.Errorf("expected image %q, got %q", test.expectedImage, recorded.Image)
			}
			if recorded.ImageVersion != test.expectedImageVersion {
				t.Errorf("expected version %q, got %q", test.expectedImageVersion, recorded.ImageVersion)
			}
			if recorded.Complete() != test.expectedComplete {
				t.Errorf("expected complete %v, got %v", test.expectedComplete, recorded.Complete())
			}
		})
	}
}

// Half a record places a container as surely as none of it does: the missing
// half would come from the definition, which is the drift the record exists to
// stop. This is what Start refuses on, and a gate that only asked whether the
// record was empty would have let these through.
func TestRecordedImageIsIncompleteWithHalfARecord(t *testing.T) {
	tests := []struct {
		name     string
		recorded RecordedImage
		expected bool
	}{
		{name: "both halves", recorded: RecordedImage{Image: "redis", ImageVersion: "8.9.0"}, expected: true},
		{name: "only the image", recorded: RecordedImage{Image: "redis"}},
		{name: "only the version", recorded: RecordedImage{ImageVersion: "8.9.0"}},
		{name: "neither"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := test.recorded.Complete(); actual != test.expected {
				t.Errorf("expected complete %v, got %v", test.expected, actual)
			}
		})
	}
}

func TestRecoveredImage(t *testing.T) {
	tests := []struct {
		name            string
		recorded        RecordedImage
		running         string
		expected        RecordedImage
		expectedChanged bool
	}{
		{
			// the record wins. A container running something else is what Start
			// rebuilds, not a correction to write down
			name:     "a complete record and a container that disagrees",
			recorded: RecordedImage{Image: "redis", ImageVersion: "8.9.0"},
			running:  "redis:8.10.0",
			expected: RecordedImage{Image: "redis", ImageVersion: "8.9.0"},
		},
		{
			name:            "no record and a container",
			running:         "redis:8.9.0",
			expected:        RecordedImage{Image: "redis", ImageVersion: "8.9.0"},
			expectedChanged: true,
		},
		{
			name: "no record and no container",
		},
		{
			name:            "a record missing only the version",
			recorded:        RecordedImage{Image: "redis/redis-stack-server"},
			running:         "redis/redis-stack-server:7.2.0",
			expected:        RecordedImage{Image: "redis/redis-stack-server", ImageVersion: "7.2.0"},
			expectedChanged: true,
		},
		{
			// nothing to split, so nothing is learned rather than a version of ""
			name:     "a container image with no tag",
			running:  "redis",
			expected: RecordedImage{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, changed := recoveredImage(test.recorded, test.running)
			if actual != test.expected {
				t.Errorf("expected %+v, got %+v", test.expected, actual)
			}
			if changed != test.expectedChanged {
				t.Errorf("expected changed %v, got %v", test.expectedChanged, changed)
			}
		})
	}
}

// Start runs from the pre-start trigger and dokku restores apps in parallel, so
// two deploys can reach one service at once. A record that is already there is
// never rewritten, which is what keeps a concurrent reader from finding the file
// empty. Read only files stand in for the write that must not happen.
func TestRecoverRecordedImageSkipsAnUnchangedRecord(t *testing.T) {
	redis := redisDatastore(t)
	serviceRoot := withServiceRoot(t, redis, "lollipop")
	writeRecord(t, serviceRoot, "redis", "8.9.0")

	for _, name := range []string{"IMAGE", "IMAGE_VERSION"} {
		filename := filepath.Join(serviceRoot, name)
		if err := os.Chmod(filename, 0444); err != nil {
			t.Fatalf("failed to chmod %s: %v", filename, err)
		}
	}

	recorded, err := RecoverRecordedImage(context.Background(), RecoverRecordedImageInput{
		Datastore:   redis,
		ServiceName: "lollipop",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if recorded.Tagged() != "redis:8.9.0" {
		t.Errorf("expected redis:8.9.0, got %q", recorded.Tagged())
	}

	for name, expected := range map[string]string{"IMAGE": "redis", "IMAGE_VERSION": "8.9.0"} {
		contents, err := os.ReadFile(filepath.Join(serviceRoot, name))
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}

		if string(contents) != expected {
			t.Errorf("expected %s to still hold %q, got %q", name, expected, string(contents))
		}
	}
}

// The wording a disabled pull leaves behind is what an operator pastes, and it
// already shipped in two places before it was shared, so it is pinned here.
func TestPullDisabledError(t *testing.T) {
	for _, action := range []string{"creation", "upgrade", "start"} {
		t.Run(action, func(t *testing.T) {
			err := pullDisabledError("REDIS_DISABLE_PULL", "redis:8.9.0", "lollipop", action)

			expected := "REDIS_DISABLE_PULL environment variable detected. Not running pull command.\n" +
				"docker image pull redis:8.9.0\n" +
				"lollipop service " + action + " failed"
			if err.Error() != expected {
				t.Errorf("expected:\n%s\ngot:\n%s", expected, err.Error())
			}
		})
	}
}
