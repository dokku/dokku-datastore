package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// migration is how an upgrade carries a service's data onto the definition it
// moves the service to.
type migration int

const (
	// migrationNone leaves the data where it is, which is every upgrade inside
	// a definition and every move onto one that reads the last one's data
	migrationNone migration = iota

	// migrationStep runs the step the definition declares for the one the
	// service is moved off, falling back to migrationExport if it fails
	migrationStep

	// migrationExport exports the data from the old definition and imports it
	// into the new one
	migrationExport
)

// upgradeMigration is how an upgrade from one definition onto another carries
// the data across.
//
// Decided by the definition the service lands on, since that is what has to
// read the data: a definition that migrates does so whichever of its plugin's
// definitions the service comes from, and the step it declares for that one is
// tried first.
//
// Pure, so which upgrades migrate is pinned by a test rather than by a docker
// daemon.
func upgradeMigration(current *service.Datastore, target *service.Datastore) migration {
	if current.DefinitionName() == target.DefinitionName() {
		return migrationNone
	}

	if !target.Definition.Dokku.Upgrade.Migrate {
		return migrationNone
	}

	if target.HasUpgradeStep(current.DefinitionName()) {
		return migrationStep
	}

	return migrationExport
}

// checkMigration refuses a migration that could not be undone or would lose
// data, before anything about the service has been touched.
func checkMigration(input UpgradeServiceInput, target *service.Datastore, plan migration, recorded service.RecordedImage) error {
	if plan == migrationNone {
		return nil
	}

	// an app left running writes to the old data after it was copied, and what
	// it wrote is gone once the service comes back on the new
	if !input.RestartApps {
		return fmt.Errorf("unable to upgrade %s: moving it from %s to %s migrates its data, which needs --restart-apps so that nothing writes to it while it is copied", input.ServiceName, input.Datastore.DefinitionName(), target.DefinitionName())
	}

	// a migration that fails puts the service back on what it ran, which has
	// to be known to be put back on
	if !recorded.Complete() {
		return fmt.Errorf("unable to upgrade %s: unable to determine the image it runs, which a migration that fails is undone onto", input.ServiceName)
	}

	return nil
}

// migrateServiceInput is the input for migrateService.
type migrateServiceInput struct {
	// Upgrade is the upgrade being run, with the service on its old definition
	Upgrade UpgradeServiceInput

	// Target is the definition the service is moved onto
	Target *service.Datastore

	// Plan is how the data is carried across
	Plan migration

	// Placement is the image the service is moved onto
	Placement upgradePlacement

	// Previous is the image the service ran, which a failed migration puts it
	// back on
	Previous upgradePlacement
}

// migrateService moves a service onto another definition and carries its data
// across.
//
// The old data is moved aside rather than changed, so that whatever goes wrong
// the service can be put back on what it ran with the data it had. It is kept
// once the migration succeeds as well, for the operator to remove once they are
// satisfied with the new.
//
// A step that fails is undone and the data is exported and imported instead,
// which is slower but needs nothing from the definition beyond the export and
// import it already has.
func migrateService(ctx context.Context, input migrateServiceInput) error {
	upgrade := input.Upgrade
	current := upgrade.Datastore
	plan := input.Plan

	for {
		err := migrateOnce(ctx, input, plan)
		if err == nil {
			return nil
		}

		if plan != migrationStep || errors.Is(err, errNotUndone) {
			return err
		}

		upgrade.Logger.Warn(WarnInput{
			Warning: fmt.Sprintf("Unable to migrate %s from %s in place, exporting and importing it instead: %s", upgrade.ServiceName, current.DefinitionName(), err),
		})
		plan = migrationExport
	}
}

// upgradeCleanupCommand is the custom command a definition that migrates data
// declares to remove the data an upgrade kept aside.
const upgradeCleanupCommand = "upgrade-cleanup"

// errNotUndone marks a migration that failed and then could not be undone,
// which is not one to try again a different way.
var errNotUndone = errors.New("the service could not be put back on what it ran")

// migrateOnce runs one attempt at a migration, and undoes it if it fails.
func migrateOnce(ctx context.Context, input migrateServiceInput, plan migration) error {
	upgrade := input.Upgrade
	current := upgrade.Datastore
	folders := service.Folders(current, upgrade.ServiceName)

	// the export is taken first, from the old service while it still runs, and
	// staged on disk so that a failed export never reaches the import. Under
	// the service root, which is private to dokku and on the same disk as the
	// data it is a copy of
	dump := ""
	if plan == migrationExport {
		staged, err := stageExport(ctx, upgrade, input.Target)
		if err != nil {
			return fmt.Errorf("unable to export %s: %w", upgrade.ServiceName, err)
		}

		dump = staged
		defer os.Remove(dump)
	}

	if err := service.RemoveServiceContainer(ctx, service.RemoveServiceContainerInput{
		Datastore:   current,
		ServiceName: upgrade.ServiceName,
	}); err != nil {
		return undoMigration(ctx, input, "", err)
	}

	previousData := service.PreviousDataName(current.DefinitionName(), time.Now())
	if err := service.MoveDataAside(current, upgrade.ServiceName, previousData); err != nil {
		return undoMigration(ctx, input, "", err)
	}

	if err := migrateOnto(ctx, input, plan, previousData, dump); err != nil {
		return undoMigration(ctx, input, previousData, err)
	}

	upgrade.Logger.Info(fmt.Sprintf("The data %s had on %s was kept in %s", upgrade.ServiceName, current.DefinitionName(), filepath.Join(folders.Root, previousData)))
	if _, ok := input.Target.Definition.Dokku.CustomCommands[upgradeCleanupCommand]; ok {
		upgrade.Logger.Info(fmt.Sprintf("Remove it once the upgrade is confirmed with: dokku %s:%s %s", current.Properties().CommandPrefix, upgradeCleanupCommand, upgrade.ServiceName))
	}
	return nil
}

// migrateOnto places a service on the definition it is moved onto, with its old
// data already moved aside, and carries that data across.
func migrateOnto(ctx context.Context, input migrateServiceInput, plan migration, previousData string, dump string) error {
	upgrade := input.Upgrade
	from := upgrade.Datastore.DefinitionName()

	if err := recordPlacement(upgrade, input.Target, input.Placement); err != nil {
		return err
	}

	upgrade.Datastore = input.Target
	if plan == migrationStep {
		upgrade.Logger.Info(fmt.Sprintf("Migrating the data of %s from %s in place", upgrade.ServiceName, from))
		if err := input.Target.RunUpgradeStep(ctx, upgrade.ServiceName, from, previousData); err != nil {
			return fmt.Errorf("unable to migrate the data of %s from %s: %w", upgrade.ServiceName, from, err)
		}
	}

	if err := startPlacement(ctx, upgrade, input.Placement); err != nil {
		return err
	}

	if plan != migrationExport {
		return nil
	}

	upgrade.Logger.Info(fmt.Sprintf("Importing the data of %s exported from %s", upgrade.ServiceName, from))
	reader, err := os.Open(dump)
	if err != nil {
		return fmt.Errorf("unable to read the export of %s: %w", upgrade.ServiceName, err)
	}
	defer reader.Close()

	if err := input.Target.ImportForUpgrade(ctx, input.Upgrade.Datastore, upgrade.ServiceName, reader); err != nil {
		return fmt.Errorf("unable to import into %s: %w", upgrade.ServiceName, err)
	}

	return nil
}

// stageExport exports a service into a private file under its service root,
// starting the service first if it is not running, since an export reads from
// the running service.
func stageExport(ctx context.Context, upgrade UpgradeServiceInput, target *service.Datastore) (string, error) {
	if err := service.Start(ctx, service.StartInput{
		Datastore:   upgrade.Datastore,
		ServiceName: upgrade.ServiceName,
	}); err != nil {
		return "", err
	}

	if err := WaitForService(ctx, WaitForServiceInput{
		Datastore:   upgrade.Datastore,
		ServiceName: upgrade.ServiceName,
		Logger:      upgrade.Logger,
	}); err != nil {
		return "", err
	}

	upgrade.Logger.Info(fmt.Sprintf("Exporting the data of %s from %s", upgrade.ServiceName, upgrade.Datastore.DefinitionName()))

	// CreateTemp makes the file readable by its owner alone, which is what a
	// copy of every row in the datastore has to be
	file, err := os.CreateTemp(service.Folders(upgrade.Datastore, upgrade.ServiceName).Root, "upgrade-*.export")
	if err != nil {
		return "", fmt.Errorf("unable to create a file to export into: %w", err)
	}

	if err := upgrade.Datastore.ExportForUpgrade(ctx, target, upgrade.ServiceName, file); err != nil {
		file.Close()
		os.Remove(file.Name())
		return "", err
	}

	if err := file.Close(); err != nil {
		os.Remove(file.Name())
		return "", fmt.Errorf("unable to write the export: %w", err)
	}

	return file.Name(), nil
}

// undoMigration puts a service back on the definition and the image it ran,
// with the data it had, and reports why it had to.
//
// Everything the migration did is taken back in the order it was done: the new
// container, what it wrote into the data directory, the old data moved aside,
// and the record. The service is then made again exactly as an upgrade within
// its own definition would make it. Settings the upgrade was asked to change
// stay changed, as they do for any upgrade that fails after writing them.
func undoMigration(ctx context.Context, input migrateServiceInput, previousData string, cause error) error {
	upgrade := input.Upgrade
	current := upgrade.Datastore

	upgrade.Logger.Warn(WarnInput{
		Warning: fmt.Sprintf("Putting %s back on %s: %s", upgrade.ServiceName, current.DefinitionName(), cause),
	})

	undo := func() error {
		if err := service.RemoveServiceContainer(ctx, service.RemoveServiceContainerInput{
			Datastore:   input.Target,
			ServiceName: upgrade.ServiceName,
		}); err != nil {
			return err
		}

		if previousData != "" {
			if err := clearDataDirectory(ctx, current, upgrade.ServiceName); err != nil {
				return err
			}

			if err := service.RestoreDataAside(current, upgrade.ServiceName, previousData); err != nil {
				return err
			}
		}

		if err := recordPlacement(withoutSettings(upgrade), current, input.Previous); err != nil {
			return err
		}

		return startPlacement(ctx, upgrade, input.Previous)
	}

	if err := undo(); err != nil {
		return fmt.Errorf("%w; %w: %w", cause, errNotUndone, err)
	}

	return cause
}

// withoutSettings is an upgrade that changes nothing but the image, which is
// what putting a service back has to be: the settings were already written.
func withoutSettings(input UpgradeServiceInput) UpgradeServiceInput {
	return UpgradeServiceInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		Logger:      input.Logger,
	}
}

// clearDataDirectory removes what a failed migration wrote into a service's
// data directory.
//
// What is in it belongs to the datastore's user rather than to dokku, so its
// mode is widened from a container first, the way destroy does it.
func clearDataDirectory(ctx context.Context, s *service.Datastore, serviceName string) error {
	folders := service.Folders(s, serviceName)
	arguments := RemoveDataArgs(RemoveDataArgsInput{
		Directories: []string{folders.HostData},
		Image:       hostenv.BusyboxImage,
	})

	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    arguments,
	}); err != nil {
		return fmt.Errorf("unable to clear the data of %s: %w", serviceName, err)
	}

	if err := os.RemoveAll(folders.Data); err != nil {
		return fmt.Errorf("unable to clear the data of %s: %w", serviceName, err)
	}

	return nil
}
