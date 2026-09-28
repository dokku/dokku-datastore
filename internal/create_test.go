package internal

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// currentUserAndGroup returns names SetPermissions can look up, so the test does
// not depend on a dokku user existing on the machine running it
func currentUserAndGroup(t *testing.T) (string, string) {
	t.Helper()

	current, err := user.Current()
	if err != nil {
		t.Fatalf("failed to look up the current user: %v", err)
	}

	group, err := user.LookupGroupId(current.Gid)
	if err != nil {
		t.Fatalf("failed to look up the current group: %v", err)
	}

	return current.Username, group.Name
}

func TestCreateServiceFolders(t *testing.T) {
	username, groupName := currentUserAndGroup(t)

	// a umask that would strip the group write bit, to prove the mode is applied
	// explicitly rather than left to MkdirAll
	previous := syscall.Umask(0022)
	t.Cleanup(func() { syscall.Umask(previous) })

	root := t.TempDir()
	folders := []string{
		filepath.Join(root, "lollipop"),
		filepath.Join(root, "lollipop", "config"),
		filepath.Join(root, "lollipop", "data"),
	}

	if err := CreateServiceFolders(folders, username, groupName); err != nil {
		t.Fatalf("failed to create service folders: %v", err)
	}

	for _, folder := range folders {
		info, err := os.Stat(folder)
		if err != nil {
			t.Fatalf("expected %s to exist: %v", folder, err)
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", folder)
		}
		// pinned to the concrete value on purpose: the datastore container takes
		// ownership of the data folder, so losing the group write bit breaks
		// import for everyone but root
		if mode := info.Mode().Perm(); mode != 0775 {
			t.Errorf("expected %s to have mode 775, got %o", folder, mode)
		}
	}
}

func TestCreateServiceFoldersIsIdempotent(t *testing.T) {
	username, groupName := currentUserAndGroup(t)

	root := t.TempDir()
	folders := []string{filepath.Join(root, "lollipop", "data")}

	if err := CreateServiceFolders(folders, username, groupName); err != nil {
		t.Fatalf("failed to create service folders: %v", err)
	}
	if err := CreateServiceFolders(folders, username, groupName); err != nil {
		t.Fatalf("expected a second run to succeed, got: %v", err)
	}

	info, err := os.Stat(folders[0])
	if err != nil {
		t.Fatalf("expected %s to exist: %v", folders[0], err)
	}
	if mode := info.Mode().Perm(); mode != 0775 {
		t.Errorf("expected mode 775, got %o", mode)
	}
}

// A log option docker will not accept is refused where the command starts,
// because everything after this point writes: the service root, its credentials
// and its config files would all be on disk before docker was the one to say so.
func TestCreateServiceRefusesAnUnusableLogConfig(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	err := CreateService(t.Context(), CreateServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		LogOptions:  []string{"max-size=20"},
	})
	if err == nil {
		t.Fatal("expected a malformed log option to be refused, got no error")
	}

	if !strings.Contains(err.Error(), `invalid max-size value "20"`) {
		t.Errorf("expected the error to name the value, got %q", err)
	}

	if root := service.Folders(datastore, "lollipop").Root; common.DirectoryExists(root) {
		t.Errorf("a refused create left %s behind", root)
	}
}

// And for the same reason: a restart policy docker will not accept is a
// container that cannot be made.
func TestCreateServiceRefusesAnUnusableRestartPolicy(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	err := CreateService(t.Context(), CreateServiceInput{
		Datastore:     datastore,
		ServiceName:   "lollipop",
		RestartPolicy: "on-failure:abc",
	})
	if err == nil {
		t.Fatal("expected a malformed restart policy to be refused, got no error")
	}

	if !strings.Contains(err.Error(), `invalid restart-policy value "on-failure:abc"`) {
		t.Errorf("expected the error to name the value, got %q", err)
	}

	if root := service.Folders(datastore, "lollipop").Root; common.DirectoryExists(root) {
		t.Errorf("a refused create left %s behind", root)
	}
}

// A wait timeout is checked the same way, since a service written down with one
// the probe cannot use would fail every start after it.
func TestCreateServiceRefusesAnUnusableWaitTimeout(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	err := CreateService(t.Context(), CreateServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		WaitTimeout: "forever",
	})
	if err == nil {
		t.Fatal("expected a malformed wait timeout to be refused, got no error")
	}

	if !strings.Contains(err.Error(), `invalid wait-timeout value "forever"`) {
		t.Errorf("expected the error to name the value, got %q", err)
	}

	if root := service.Folders(datastore, "lollipop").Root; common.DirectoryExists(root) {
		t.Errorf("a refused create left %s behind", root)
	}
}

// And a password with no secret to go to: redis has no root password, so
// --root-password would be dropped and the operator left believing it was set.
func TestCreateServiceRefusesAPasswordWithNowhereToGo(t *testing.T) {
	datastore := service.Datastores["redis"]
	withDataRoot(t)

	err := CreateService(t.Context(), CreateServiceInput{
		Datastore:    datastore,
		ServiceName:  "lollipop",
		Password:     "given",
		RootPassword: "given-root",
	})
	if err == nil {
		t.Fatal("expected a root password for redis to be refused, got no error")
	}

	if !strings.Contains(err.Error(), "--root-password") {
		t.Errorf("expected the error to name the flag, got %q", err)
	}

	if root := service.Folders(datastore, "lollipop").Root; common.DirectoryExists(root) {
		t.Errorf("a refused create left %s behind", root)
	}
}

// A name the datastore keeps a database under is refused before anything is
// made, since the service's database is named after it and the app would be
// handed the datastore's own. Hyphens become underscores in a database name, so
// the hyphenated spelling is refused as well.
func TestCreateServiceRefusesAReservedName(t *testing.T) {
	datastore := service.Datastores["mysql"]
	withDataRoot(t)

	for _, name := range []string{"mysql", "Information-Schema"} {
		t.Run(name, func(t *testing.T) {
			err := CreateService(t.Context(), CreateServiceInput{
				Datastore:   datastore,
				ServiceName: name,
			})
			if !errors.Is(err, service.ErrReservedServiceName) {
				t.Fatalf("expected %s to be refused as reserved, got %v", name, err)
			}

			if root := service.Folders(datastore, name).Root; common.DirectoryExists(root) {
				t.Errorf("a refused create left %s behind", root)
			}
		})
	}
}

// A definition that is not the datastore's own is refused before anything is
// made, whether nothing ships it or another datastore does, rather than placing
// the service on whatever its image would have resolved to.
func TestCreateServiceRefusesAnUnknownDefinition(t *testing.T) {
	datastore := service.Datastores["postgres"]
	withDataRoot(t)

	for _, name := range []string{"postgres-12", "redis"} {
		t.Run(name, func(t *testing.T) {
			err := CreateService(t.Context(), CreateServiceInput{
				Datastore:   datastore,
				Definition:  name,
				ServiceName: "lollipop",
			})
			if err == nil {
				t.Fatalf("expected the %s definition to be refused, got no error", name)
			}

			if !strings.Contains(err.Error(), "postgres-17") {
				t.Errorf("expected the error to list the postgres definitions, got %q", err)
			}

			if root := service.Folders(datastore, "lollipop").Root; common.DirectoryExists(root) {
				t.Errorf("a refused create left %s behind", root)
			}
		})
	}
}
