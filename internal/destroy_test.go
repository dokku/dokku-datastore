package internal

import (
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
)

func TestErrLinkedService(t *testing.T) {
	// The bash datastore plugins emit this exact string, and downstream plugin
	// test suites assert on it, so the capitalization is load bearing.
	expected := "Cannot delete linked service"
	if ErrLinkedService.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, ErrLinkedService)
	}
}

// The widening used to cover config and data and nothing else, so a definition
// binding a third directory left a tree the dokku user could not remove. Both
// of the definitions that do bind a third one are checked here, because they
// are what the old shape would have missed.
func TestRemoveDataArgsCoversEveryBindSource(t *testing.T) {
	tests := []struct {
		name      string
		datastore string
		expected  []string
	}{
		{
			name:      "a third directory the hook makes",
			datastore: "postgres",
			expected:  []string{"/data", "/certs"},
		},
		{
			name:      "a third directory the image reads",
			datastore: "mongo",
			expected:  []string{"/config", "/data", "/initdb"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			datastore, ok := service.Datastores[test.datastore]
			if !ok {
				t.Fatalf("expected %s to be registered", test.datastore)
			}

			args := RemoveDataArgs(RemoveDataArgsInput{
				Directories: datastore.BindHostDirectories("lollipop"),
				Image:       "busybox:1.37.0-uclibc",
			})

			joined := strings.Join(args, " ")
			for _, suffix := range test.expected {
				if !strings.Contains(joined, "/lollipop"+suffix+":") {
					t.Errorf("expected %s to be widened, got %s", suffix, joined)
				}
			}

			// one mount per directory, and one target per mount
			mounts := strings.Count(joined, " -v ")
			if mounts != len(test.expected) {
				t.Errorf("expected %d mounts, got %d in %s", len(test.expected), mounts, joined)
			}

			if !strings.Contains(joined, "chmod 777 -R") {
				t.Errorf("expected the mode to be widened, got %s", joined)
			}
		})
	}
}

// Postgres mounts its certificates twice, once in the service and once in the
// hook that makes them, and mounting the same directory at two places would be
// two chances to get the same work wrong.
func TestRemoveDataArgsMountsADirectoryOnce(t *testing.T) {
	postgres, ok := service.Datastores["postgres"]
	if !ok {
		t.Fatal("expected postgres to be registered")
	}

	directories := postgres.BindHostDirectories("lollipop")
	seen := map[string]int{}
	for _, directory := range directories {
		seen[directory]++
	}

	for directory, count := range seen {
		if count != 1 {
			t.Errorf("expected %s once, got it %d times", directory, count)
		}
	}
}

// Memcached holds its data in memory and binds nothing, so there is nothing to
// widen and no reason to start a container to widen it.
func TestRemoveDataArgsSkipsADatastoreThatBindsNothing(t *testing.T) {
	memcached, ok := service.Datastores["memcached"]
	if !ok {
		t.Fatal("expected memcached to be registered")
	}

	directories := memcached.BindHostDirectories("lollipop")
	if len(directories) != 0 {
		t.Fatalf("expected memcached to bind nothing, got %v", directories)
	}

	if args := RemoveDataArgs(RemoveDataArgsInput{Directories: directories, Image: "busybox"}); args != nil {
		t.Errorf("expected no container, got %v", args)
	}
}
