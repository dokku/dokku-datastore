package internal

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/mitchellh/cli"
)

// linkedServices points the package at a temporary services root and writes the
// links file of every service named, so a trigger runs over a known set rather
// than over whatever the host happens to have.
func linkedServices(t *testing.T, links map[string][]string) *service.Datastore {
	t.Helper()

	withDataRoot(t)

	datastore := service.Datastores["redis"]
	for serviceName, apps := range links {
		if err := os.MkdirAll(service.Folders(datastore, serviceName).Root, 0755); err != nil {
			t.Fatalf("failed to create the service root: %s", err)
		}

		for _, appName := range apps {
			input := service.LinkedAppsInput{Datastore: datastore, ServiceName: serviceName}
			if err := service.AddLinkedApp(t.Context(), input, appName); err != nil {
				t.Fatalf("failed to link %s to %s: %s", appName, serviceName, err)
			}
		}
	}

	return datastore
}

// triggerInput carries a usable logger, since RemoveAppLinks reports each
// service it touches and would otherwise report it to nothing at all
func triggerInput(datastore *service.Datastore) TriggerInput {
	return TriggerInput{Datastore: datastore, Logger: Ui{Ui: cli.NewMockUi()}}
}

func assertLinkedApps(t *testing.T, datastore *service.Datastore, serviceName string, expected []string) {
	t.Helper()

	input := service.LinkedAppsInput{Datastore: datastore, ServiceName: serviceName}
	if actual := service.LinkedApps(t.Context(), input); !slices.Equal(actual, expected) {
		t.Errorf("expected %s to be linked to %v, got %v", serviceName, expected, actual)
	}
}

func TestCopyAppLinks(t *testing.T) {
	tests := []struct {
		name     string
		links    map[string][]string
		expected map[string][]string
	}{
		{
			name:     "a service the app is linked to",
			links:    map[string][]string{"lollipop": {"my-app"}},
			expected: map[string][]string{"lollipop": {"my-app", "renamed-app"}},
		},
		{
			// the trigger is handed every service of the datastore, so one the
			// app never used has to come through untouched
			name:     "a service the app is not linked to",
			links:    map[string][]string{"lollipop": {"my-app"}, "gobstopper": {"other-app"}},
			expected: map[string][]string{"lollipop": {"my-app", "renamed-app"}, "gobstopper": {"other-app"}},
		},
		{
			name:     "no service links the app",
			links:    map[string][]string{"lollipop": {"other-app"}},
			expected: map[string][]string{"lollipop": {"other-app"}},
		},
		{
			name:     "a new name that is linked already",
			links:    map[string][]string{"lollipop": {"my-app", "renamed-app"}},
			expected: map[string][]string{"lollipop": {"my-app", "renamed-app"}},
		},
		{
			name:     "a service with no links file at all",
			links:    map[string][]string{"lollipop": {}},
			expected: map[string][]string{"lollipop": {}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			datastore := linkedServices(t, test.links)

			if err := CopyAppLinks(t.Context(), triggerInput(datastore), "my-app", "renamed-app"); err != nil {
				t.Fatalf("failed to copy the app links: %s", err)
			}

			for serviceName, expected := range test.expected {
				assertLinkedApps(t, datastore, serviceName, expected)
			}
		})
	}
}

func TestRemoveAppLinks(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{
		"lollipop":    {"my-app", "other-app"},
		"gobstopper":  {"my-app"},
		"everlasting": {"other-app"},
	})

	if err := RemoveAppLinks(t.Context(), triggerInput(datastore), "my-app"); err != nil {
		t.Fatalf("failed to remove the app links: %s", err)
	}

	assertLinkedApps(t, datastore, "lollipop", []string{"other-app"})
	assertLinkedApps(t, datastore, "gobstopper", []string{})
	assertLinkedApps(t, datastore, "everlasting", []string{"other-app"})
}

// The two halves of a rename, in the order dokku runs them: this trigger, and
// then the pre-delete that fires when the old app is destroyed a moment later.
// Reading the first half on its own is what made the old name look like it was
// left behind for good.
func TestARenameLeavesOnlyTheNewName(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"my-app"}})
	input := triggerInput(datastore)

	if err := CopyAppLinks(t.Context(), input, "my-app", "renamed-app"); err != nil {
		t.Fatalf("failed to copy the app links: %s", err)
	}

	if err := RemoveAppLinks(t.Context(), input, "my-app"); err != nil {
		t.Fatalf("failed to remove the app links: %s", err)
	}

	assertLinkedApps(t, datastore, "lollipop", []string{"renamed-app"})
}

// A clone keeps both, because both apps are still there afterwards and both are
// using the service
func TestACloneLeavesBothNames(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"my-app"}})

	if err := CopyAppLinks(t.Context(), triggerInput(datastore), "my-app", "cloned-app"); err != nil {
		t.Fatalf("failed to copy the app links: %s", err)
	}

	assertLinkedApps(t, datastore, "lollipop", []string{"cloned-app", "my-app"})
}

func TestServicesWithLinks(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{
		"lollipop":    {"my-app"},
		"gobstopper":  {"my-app", "other-app"},
		"everlasting": {},
	})

	services, err := ServicesWithLinks(t.Context(), datastore)
	if err != nil {
		t.Fatalf("failed to list the linked services: %s", err)
	}

	slices.Sort(services)
	if expected := []string{"gobstopper", "lollipop"}; !slices.Equal(services, expected) {
		t.Errorf("expected %v, got %v", expected, services)
	}
}

// dokku fires some triggers with no app at all, which leaves nothing to start
func TestStartLinkedServicesWithoutAnApp(t *testing.T) {
	datastore := linkedServices(t, map[string][]string{"lollipop": {"my-app"}})

	if err := StartLinkedServices(t.Context(), triggerInput(datastore), ""); err != nil {
		t.Errorf("expected no error for a trigger naming no app, got %s", err)
	}
}

// Every scheduled service is handed to dokku, in order, and nothing else. A
// service whose recorded schedule cron cannot run is left out and reported,
// since one invalid line makes dokku refuse its whole crontab.
func TestCronEntriesForTrigger(t *testing.T) {
	datastore := withScheduleService(t, "banana", "cherry", "grape", "apple")

	for serviceName, schedule := range map[string]BackupSchedule{
		"apple":  {Schedule: "0 3 * * *", BucketName: "my-bucket"},
		"cherry": {Schedule: "@daily", BucketName: "other-bucket", UseIAM: true},
		"grape":  {Schedule: "daily", BucketName: "my-bucket"},
	} {
		// written directly, since scheduling refuses what grape is recorded
		// with, as a hand edit or an earlier version could have left it
		if err := writeBackupSchedule(datastore, serviceName, schedule); err != nil {
			t.Fatalf("failed to record the schedule for %s: %s", serviceName, err)
		}
	}

	entries, skipped, err := CronEntriesForTrigger(t.Context(), triggerInput(datastore))
	if err != nil {
		t.Fatalf("failed to list the cron entries: %s", err)
	}

	expected := []string{
		"0 3 * * *;dokku redis:backup apple my-bucket;/var/log/dokku/redis.log",
		"@daily;dokku redis:backup cherry other-bucket --use-iam;/var/log/dokku/redis.log",
	}
	if !slices.Equal(entries, expected) {
		t.Errorf("expected %q, got %q", expected, entries)
	}

	if len(skipped) != 1 || !strings.Contains(skipped[0].Error(), "grape") {
		t.Errorf("expected grape to be reported as skipped, got %v", skipped)
	}
}

// A datastore with nothing scheduled prints nothing, rather than a blank line
// that would make dokku drop every task other plugins hand it
func TestCronEntriesForTriggerWithNothingScheduled(t *testing.T) {
	datastore := withScheduleService(t, "lollipop")

	entries, skipped, err := CronEntriesForTrigger(t.Context(), triggerInput(datastore))
	if err != nil {
		t.Fatalf("failed to list the cron entries: %s", err)
	}

	if len(entries) != 0 || len(skipped) != 0 {
		t.Errorf("expected nothing, got %q and %v", entries, skipped)
	}
}
