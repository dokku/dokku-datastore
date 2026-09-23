package internal

import (
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
