package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
//
// The default version is part of the default image rather than a half that can
// be drawn on alone: a tag belongs to the repository that published it, so
// there is nothing to fall back to for an image the definition does not ship.
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
		expectedErr          bool
	}{
		{
			name:         "both halves recorded",
			image:        "redis",
			imageVersion: "8.9.0",
			expected:     "redis:8.9.0",
		},
		{
			// the definition ships no tag for somebody else's repository, and
			// pasting its own on named redis/redis-stack-server:<redis version>,
			// which failed saying an image nobody ever built was missing
			name:        "only a custom image recorded",
			image:       "redis/redis-stack-server",
			expectedErr: true,
		},
		{
			// a service created before the IMAGE file existed recorded only its
			// version, and the definition's image is the one it has been running
			// all along, so this half is still filled in
			name:         "only the version recorded",
			imageVersion: "8.9.0",
			expected:     defaultImage + ":8.9.0",
		},
		{
			name:     "the definition's own image recorded without a version",
			image:    defaultImage,
			expected: defaultImage + ":" + defaultVersion,
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
		{
			// create with --image and no --image-version, which is where the
			// false missing image was reached from
			name:          "a custom image override with no version",
			imageOverride: "redis/redis-stack-server",
			expectedErr:   true,
		},
		{
			name:                 "a custom image override with a version",
			imageOverride:        "redis/redis-stack-server",
			imageVersionOverride: "7.2.0-v10",
			expected:             "redis/redis-stack-server:7.2.0-v10",
		},
		{
			// the version the service already recorded is a version, so an
			// image override alone is only refused where there is none
			name:          "a custom image override over a recorded version",
			imageVersion:  "7.2.0-v10",
			imageOverride: "redis/redis-stack-server",
			expected:      "redis/redis-stack-server:7.2.0-v10",
		},
		{
			name:         "a private registry carries its port, not a tag",
			image:        "registry.example.com:5000/redis",
			imageVersion: "8.9.0",
			expected:     "registry.example.com:5000/redis:8.9.0",
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
			if test.expectedErr {
				if !errors.Is(err, ErrNoImageVersion) {
					t.Fatalf("expected a refusal, got %q and %v", actual, err)
				}
				return
			}

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

// What the read paths get for a record nothing can settle. They have nowhere to
// return an error to, so they are handed the repository with an empty tag - a
// reference docker refuses - rather than one it would resolve to latest. The
// verbs are the callers that would run it, and they refuse first.
func TestTaggedImageReportsARecordItCannotSettle(t *testing.T) {
	redis := redisDatastore(t)
	serviceRoot := withServiceRoot(t, redis, "lollipop")
	writeRecord(t, serviceRoot, "redis/redis-stack-server", "")

	if actual := redis.taggedImage("lollipop"); actual != "redis/redis-stack-server:" {
		t.Errorf("expected the recorded repository with no tag, got %q", actual)
	}
}

// An offline verb stops the service and runs a throwaway container against its
// data, so which version it runs is the whole question. A service whose record
// cannot answer is told rather than run at whatever the definition ships now.
func TestAVerbRefusesARecordItCannotSettle(t *testing.T) {
	redis := redisDatastore(t)
	serviceRoot := withServiceRoot(t, redis, "lollipop")
	writeRecord(t, serviceRoot, "redis/redis-stack-server", "")

	err := redis.ExportService(context.Background(), ExportServiceInput{
		ServiceName: "lollipop",
		Writer:      io.Discard,
	})
	if !errors.Is(err, ErrNoImageVersion) {
		t.Fatalf("expected a refusal, got %v", err)
	}

	if !strings.Contains(err.Error(), "lollipop") {
		t.Errorf("expected the refusal to name the service, got %q", err)
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
		{
			// splitting on the first colon recorded the image as
			// registry.example.com at the version 5000/redis:8.9.0, and every
			// later decision was then made about a host name
			name:            "a container image from a private registry",
			running:         "registry.example.com:5000/redis:8.9.0",
			expected:        RecordedImage{Image: "registry.example.com:5000/redis", ImageVersion: "8.9.0"},
			expectedChanged: true,
		},
		{
			name:     "a container image from a private registry with no tag",
			running:  "registry.example.com:5000/redis",
			expected: RecordedImage{},
		},
		{
			// the colon in sha256: belongs to the digest, so there is no tag to
			// learn rather than one called after the hash
			name:     "a container image pinned by digest",
			running:  "redis@sha256:0000000000000000000000000000000000000000000000000000000000000000",
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
//
// The actions are the ones the tree actually uses: create, upgrade and start
// name the service image; the rest name a step that runs one of the images the
// plugin runs beside a service, and export stands in for every verb, which
// passes its own name.
// The wording an operator reads when they named an image and no version. The
// sentinel is unwrapped to rather than wrapped, so its own text does not turn up
// in front of the sentence.
func TestNoImageVersionError(t *testing.T) {
	err := NoImageVersionError("redis/redis-stack-server", "redis")

	expected := "redis/redis-stack-server is not the image the redis definition ships, " +
		"so it has no version to fall back on; name one with --image-version"
	if err.Error() != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, err.Error())
	}

	if !errors.Is(err, ErrNoImageVersion) {
		t.Error("expected the refusal to be an ErrNoImageVersion")
	}
}

func TestPullDisabledError(t *testing.T) {
	actions := []string{
		"creation",
		"upgrade",
		"start",
		"port publishing",
		"readiness check",
		"backup",
		"destroy",
		"export",
	}

	for _, action := range actions {
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

// Install fetches what a plugin needs before any service exists, so the line
// naming a service and what could not be done to it has nothing to say and is
// left off. This is the wording install warns with and then carries on past.
func TestPullDisabledErrorWithoutAService(t *testing.T) {
	err := pullDisabledError("REDIS_DISABLE_PULL", "dokku/wait:0.9.3", "", "")

	expected := "REDIS_DISABLE_PULL environment variable detected. Not running pull command.\n" +
		"docker image pull dokku/wait:0.9.3"
	if err.Error() != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, err.Error())
	}
}

// Install is the one caller that carries on past a disabled pull rather than
// refusing, and it tells the two apart with errors.Is. Nothing about the message
// says so, which is why this is pinned separately from the wording.
func TestPullDisabledErrorIsErrPullDisabled(t *testing.T) {
	err := pullDisabledError("REDIS_DISABLE_PULL", "redis:8.9.0", "lollipop", "start")
	if !errors.Is(err, ErrPullDisabled) {
		t.Error("expected a disabled pull to be recognisable as one")
	}

	if !errors.Is(fmt.Errorf("unable to start lollipop: %w", err), ErrPullDisabled) {
		t.Error("expected a disabled pull to survive being wrapped")
	}

	if errors.Is(errors.New("failed to pull image redis:8.9.0"), ErrPullDisabled) {
		t.Error("expected a failed pull not to look like a disabled one")
	}
}

// An ambassador is kept only when it is running and fronts the container the
// service has now. Everything else about an exposed service's ambassador is
// replaced rather than started, since starting one linked to a container that
// is gone or down is what docker refuses.
func TestActionForAmbassador(t *testing.T) {
	tests := []struct {
		name      string
		exposed   bool
		status    string
		frontedID string
		serviceID string
		expected  ambassadorAction
	}{
		{name: "not exposed, no ambassador", exposed: false, status: "missing", serviceID: "abc", expected: ambassadorNone},
		{name: "not exposed, running ambassador", exposed: false, status: "running", frontedID: "abc", serviceID: "abc", expected: ambassadorRemove},
		{name: "not exposed, stopped ambassador", exposed: false, status: "exited", frontedID: "old", serviceID: "abc", expected: ambassadorRemove},
		{name: "exposed, no ambassador", exposed: true, status: "missing", serviceID: "abc", expected: ambassadorCreate},
		{name: "exposed, running and fronting the service", exposed: true, status: "running", frontedID: "abc", serviceID: "abc", expected: ambassadorKeep},
		{name: "exposed, fronting a container that is gone", exposed: true, status: "running", frontedID: "old", serviceID: "abc", expected: ambassadorReplace},
		{name: "exposed, made before the label existed", exposed: true, status: "running", frontedID: "", serviceID: "abc", expected: ambassadorReplace},
		{name: "exposed, no service container", exposed: true, status: "running", frontedID: "", serviceID: "", expected: ambassadorReplace},
		{name: "exposed, stopped by a pause", exposed: true, status: "exited", frontedID: "abc", serviceID: "abc", expected: ambassadorReplace},
		{name: "exposed, created and never started", exposed: true, status: "created", frontedID: "abc", serviceID: "abc", expected: ambassadorReplace},
		{name: "exposed, dead", exposed: true, status: "dead", frontedID: "abc", serviceID: "abc", expected: ambassadorReplace},
		{name: "exposed, restarting on a failed link", exposed: true, status: "restarting", frontedID: "abc", serviceID: "abc", expected: ambassadorReplace},
		{name: "exposed, a state docker has not shipped yet", exposed: true, status: "hibernating", frontedID: "abc", serviceID: "abc", expected: ambassadorReplace},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := actionForAmbassador(test.exposed, test.status, test.frontedID, test.serviceID); actual != test.expected {
				t.Errorf("expected %d, got %d", test.expected, actual)
			}
		})
	}
}

func TestAmbassadorRunArgs(t *testing.T) {
	tests := []struct {
		name     string
		input    ambassadorRunArgsInput
		expected string
	}{
		{
			name: "one port",
			input: ambassadorRunArgsInput{
				AmbassadorName: "dokku.postgres.lake.ambassador",
				CommandPrefix:  "postgres",
				ContainerID:    "abc123",
				ContainerName:  "dokku.postgres.lake",
				ContainerPorts: []int{5432},
				HostPorts:      []string{"5678"},
				Image:          "dokku/ambassador:0.8.2",
				LogArgs:        []string{"--log-opt=max-size=10m"},
			},
			expected: "container run -d --link=dokku.postgres.lake:postgres --name=dokku.postgres.lake.ambassador --restart=always --label=dokku=ambassador --label=dokku.ambassador=postgres --label=dokku.ambassador.container-id=abc123 --log-opt=max-size=10m --publish=5678:5432 dokku/ambassador:0.8.2",
		},
		{
			name: "several ports, published in order",
			input: ambassadorRunArgsInput{
				AmbassadorName: "dokku.rabbitmq.queue.ambassador",
				CommandPrefix:  "rabbitmq",
				ContainerID:    "def456",
				ContainerName:  "dokku.rabbitmq.queue",
				ContainerPorts: []int{5672, 4369, 35197, 15672},
				HostPorts:      []string{"1", "2", "3", "4"},
				Image:          "dokku/ambassador:0.8.2",
			},
			expected: "container run -d --link=dokku.rabbitmq.queue:rabbitmq --name=dokku.rabbitmq.queue.ambassador --restart=always --label=dokku=ambassador --label=dokku.ambassador=rabbitmq --label=dokku.ambassador.container-id=def456 --publish=1:5672 --publish=2:4369 --publish=3:35197 --publish=4:15672 dokku/ambassador:0.8.2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := strings.Join(ambassadorRunArgs(test.input), " "); actual != test.expected {
				t.Errorf("expected:\n%s\ngot:\n%s", test.expected, actual)
			}
		})
	}
}
