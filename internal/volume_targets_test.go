package internal

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// movedService is a redis service named lollipop whose data volume is mounted
// at /redis-data rather than /data
func movedService(t *testing.T) (*service.Datastore, string) {
	t.Helper()

	datastore, source := mountedService(t, nil)
	if err := service.WriteVolumeTargets(datastore, "lollipop", map[string]string{"data": "/redis-data"}); err != nil {
		t.Fatalf("failed to write the volume targets: %s", err)
	}

	return datastore, source
}

// withoutVolume is a datastore on a definition like the one given, less the
// volume named, the way a definition a service is upgraded onto can lack one
// the service moved
func withoutVolume(datastore *service.Datastore, key string) *service.Datastore {
	changed := datastore.Definition
	changed.Service.Volumes = []definition.Volume{}
	for _, volume := range datastore.Definition.Service.Volumes {
		if definition.VolumeKey(volume) != key {
			changed.Service.Volumes = append(changed.Service.Volumes, volume)
		}
	}

	return &service.Datastore{Definition: changed}
}

// A mount is checked against where the service has the definition's volumes,
// so it cannot take the path a volume was moved to and can take the one it left
func TestMountServiceFollowsAMovedVolume(t *testing.T) {
	datastore, source := movedService(t)

	_, err := MountService(t.Context(), MountServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Specs:       []string{source + ":/redis-data"},
	})
	if err == nil || !strings.Contains(err.Error(), "Container path /redis-data is already mounted by the redis definition") {
		t.Errorf("expected the moved volume's path to be refused, got %v", err)
	}

	if _, err := MountService(t.Context(), MountServiceInput{
		Datastore:   datastore,
		ServiceName: "lollipop",
		Specs:       []string{source + ":/data"},
	}); err != nil {
		t.Errorf("expected the path the volume left to be free, got %v", err)
	}
}

func TestUpgradeChangesSettingsWithVolumeTargets(t *testing.T) {
	none := map[string]string{}
	if !(UpgradeServiceInput{VolumeTargets: &none}).changesSettings() {
		t.Error("expected clearing the volume targets to count as a change")
	}
}

// A volume moved on one definition has to be one the definition an upgrade
// lands on mounts too, and that is found out before the old container is taken
// away rather than once there is nothing to go back to
func TestCheckUpgradeMountsChecksTheVolumeTargets(t *testing.T) {
	datastore, _ := movedService(t)
	input := UpgradeServiceInput{Datastore: datastore, ServiceName: "lollipop"}

	if err := checkUpgradeMounts(t.Context(), input, datastore); err != nil {
		t.Errorf("expected the stored targets to be accepted on the same definition, got %v", err)
	}

	err := checkUpgradeMounts(t.Context(), input, withoutVolume(datastore, "data"))
	if err == nil {
		t.Fatal("expected a volume the target definition lacks to be refused")
	}

	for _, expected := range []string{"unable to upgrade lollipop", "has no volume data", "clear the volume-targets property or give --volume-target"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("expected the refusal to contain %q, got %q", expected, err)
		}
	}

	// the targets asked for are checked instead of the stored ones
	cleared := map[string]string{}
	input.VolumeTargets = &cleared
	if err := checkUpgradeMounts(t.Context(), input, withoutVolume(datastore, "data")); err != nil {
		t.Errorf("expected the targets asked for to be checked instead, got %v", err)
	}
}

func TestApplyUpgradeSettingsWritesTheVolumeTargets(t *testing.T) {
	datastore, _ := movedService(t)

	// left alone when the upgrade was not asked about them
	if err := applyUpgradeSettings(UpgradeServiceInput{Datastore: datastore, ServiceName: "lollipop"}); err != nil {
		t.Fatalf("failed to apply the settings: %s", err)
	}

	targets, err := service.ServiceVolumeTargets(datastore, "lollipop")
	if err != nil || !reflect.DeepEqual(targets, map[string]string{"data": "/redis-data"}) {
		t.Errorf("expected the targets to be kept, got %v and %v", targets, err)
	}

	replacement := map[string]string{"config": "/etc/redis"}
	if err := applyUpgradeSettings(UpgradeServiceInput{Datastore: datastore, ServiceName: "lollipop", VolumeTargets: &replacement}); err != nil {
		t.Fatalf("failed to apply the settings: %s", err)
	}

	targets, err = service.ServiceVolumeTargets(datastore, "lollipop")
	if err != nil || !reflect.DeepEqual(targets, replacement) {
		t.Errorf("expected %v, got %v and %v", replacement, targets, err)
	}
}

// A volume the definition does not have, or one moved onto a mount, is refused
// before the service root is written
func TestCreateServiceRefusesAnUnusableVolumeTarget(t *testing.T) {
	tests := []struct {
		name     string
		targets  map[string]string
		mounts   []service.Mount
		expected string
	}{
		{
			name:     "a volume the definition does not have",
			targets:  map[string]string{"certs": "/certs"},
			expected: "has no volume certs",
		},
		{
			name:     "a volume moved onto another",
			targets:  map[string]string{"data": "/usr/local/etc/redis"},
			expected: "would both be mounted at /usr/local/etc/redis",
		},
		{
			name:     "a mount at the path a volume was moved to",
			targets:  map[string]string{"data": "/redis-data"},
			mounts:   []service.Mount{{Source: "some-volume", ContainerPath: "/redis-data"}},
			expected: "Container path /redis-data is already mounted by the redis definition",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			datastore := service.Datastores["redis"]
			withDataRoot(t)

			err := CreateService(t.Context(), CreateServiceInput{
				Datastore:     datastore,
				ServiceName:   "lollipop",
				Mounts:        test.mounts,
				VolumeTargets: test.targets,
			})
			if err == nil {
				t.Fatal("expected the create to be refused")
			}

			if !strings.Contains(err.Error(), test.expected) {
				t.Errorf("expected an error containing %q, got %q", test.expected, err)
			}

			if root := service.Folders(datastore, "lollipop").Root; common.DirectoryExists(root) {
				t.Errorf("a refused create left %s behind", root)
			}
		})
	}
}

func TestInfoReportsTheVolumeTargets(t *testing.T) {
	datastore, _ := movedService(t)

	info := Info(t.Context(), InfoInput{Datastore: datastore, ServiceName: "lollipop"})
	if info[service.VolumeTargetsProperty] != "data=/redis-data" {
		t.Errorf("expected the moved volume, got %q", info[service.VolumeTargetsProperty])
	}
}
