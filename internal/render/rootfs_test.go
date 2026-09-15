package render

import (
	"strings"
	"testing"
)

func TestRootfsFilesForRedis(t *testing.T) {
	files := RootfsFiles(redisInput(t))

	if len(files) != 2 {
		t.Fatalf("expected the two vendored dump scripts, got %d", len(files))
	}

	export := files[0]
	if export.Target != "/usr/local/bin/dokku-redis-export" {
		t.Errorf("expected the export script, got %q", export.Target)
	}

	// written on the tool side, mounted from the host side: the two differ on a
	// docker in docker install and only one of them is writable from here
	if expected := "/var/lib/dokku/services/redis/lollipop/rootfs/usr/local/bin/dokku-redis-export"; export.Path != expected {
		t.Errorf("expected %q, got %q", expected, export.Path)
	}

	// read only, so a compromised container cannot rewrite a script the host
	// also trusts
	if expected := "/var/lib/dokku/services/redis/lollipop/rootfs/usr/local/bin/dokku-redis-export:/usr/local/bin/dokku-redis-export:ro"; export.Mount != expected {
		t.Errorf("expected %q, got %q", expected, export.Mount)
	}

	// an embedded file has no mode of its own, and a script that arrives unable
	// to run fails when the verb is used rather than when it is loaded
	if export.Mode != 0755 {
		t.Errorf("expected the script to be executable, got %v", export.Mode)
	}

	if !strings.Contains(string(export.Contents), "BGSAVE") {
		t.Error("expected the export script's contents")
	}
}

// Mounting each file on its own rather than mounting the directory that holds
// them: a directory mounted over /usr/local/bin hides the datastore's own
// binaries, leaving nothing able to start.
func TestRootfsFilesMountFilesNotDirectories(t *testing.T) {
	for _, file := range RootfsFiles(redisInput(t)) {
		if strings.HasSuffix(file.Target, "/bin") || strings.HasSuffix(file.Target, "/") {
			t.Errorf("%q mounts a directory", file.Target)
		}
	}
}

func TestContainerArgsMountsThePayload(t *testing.T) {
	args, err := ContainerArgs(redisInput(t))
	if err != nil {
		t.Fatalf("unable to render: %s", err)
	}

	joined := strings.Join(args.Volumes, " ")
	for _, expected := range []string{
		"/rootfs/usr/local/bin/dokku-redis-export:/usr/local/bin/dokku-redis-export:ro",
		"/rootfs/usr/local/bin/dokku-redis-import:/usr/local/bin/dokku-redis-import:ro",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("expected a mount for %q, got %v", expected, args.Volumes)
		}
	}

	// the declared volumes still come first, so a payload mount cannot land
	// underneath one and be hidden by it
	if !strings.HasPrefix(joined, "/var/lib/dokku/services/redis/lollipop/config:/usr/local/etc/redis") {
		t.Errorf("expected the declared volumes first, got %v", args.Volumes)
	}
}

func TestRootfsFilesAreOrdered(t *testing.T) {
	// a map would otherwise emit the mounts differently each run
	files := RootfsFiles(redisInput(t))
	for i := 1; i < len(files); i++ {
		if files[i-1].Target >= files[i].Target {
			t.Errorf("expected the payload sorted by target, got %v then %v", files[i-1].Target, files[i].Target)
		}
	}
}
