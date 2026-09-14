package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dokku/dokku-datastore/commands"

	"github.com/josegonzalez/cli-skeleton/command"
	"github.com/mitchellh/cli"
)

// The name of the cli tool
var AppName = "dokku-datastore"

// Holds the version
var Version string

func main() {
	os.Exit(Run(os.Args[1:]))
}

// Executes the specified subcommand
func Run(args []string) int {
	ctx := context.Background()
	commandMeta := command.SetupRun(ctx, AppName, Version, args)
	commandMeta.Ui = command.HumanZerologUiWithFields(commandMeta.Ui, make(map[string]interface{}, 0))
	c := cli.NewCLI(AppName, Version)
	c.Args = os.Args[1:]
	c.Commands = command.Commands(ctx, commandMeta, Commands)
	exitCode, err := c.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error executing CLI: %s\n", err.Error())
		return 1
	}

	return exitCode
}

// Returns a list of implemented commands
func Commands(ctx context.Context, meta command.Meta) map[string]cli.CommandFactory {
	return map[string]cli.CommandFactory{
		"app-links": func() (cli.Command, error) {
			return &commands.AppLinksCommand{Meta: meta}, nil
		},
		"backup": func() (cli.Command, error) {
			return &commands.BackupCommand{Meta: meta}, nil
		},
		"backup-auth": func() (cli.Command, error) {
			return &commands.BackupAuthCommand{Meta: meta}, nil
		},
		"backup-deauth": func() (cli.Command, error) {
			return &commands.BackupDeauthCommand{Meta: meta}, nil
		},
		"backup-schedule": func() (cli.Command, error) {
			return &commands.BackupScheduleCommand{Meta: meta}, nil
		},
		"backup-schedule-cat": func() (cli.Command, error) {
			return &commands.BackupScheduleCatCommand{Meta: meta}, nil
		},
		"backup-set-encryption": func() (cli.Command, error) {
			return &commands.BackupSetEncryptionCommand{Meta: meta}, nil
		},
		"backup-set-public-key-encryption": func() (cli.Command, error) {
			return &commands.BackupSetPublicKeyEncryptionCommand{Meta: meta}, nil
		},
		"backup-unschedule": func() (cli.Command, error) {
			return &commands.BackupUnscheduleCommand{Meta: meta}, nil
		},
		"backup-unset-encryption": func() (cli.Command, error) {
			return &commands.BackupUnsetEncryptionCommand{Meta: meta}, nil
		},
		"backup-unset-public-key-encryption": func() (cli.Command, error) {
			return &commands.BackupUnsetPublicKeyEncryptionCommand{Meta: meta}, nil
		},
		"create": func() (cli.Command, error) {
			return &commands.CreateCommand{Meta: meta}, nil
		},
		"destroy": func() (cli.Command, error) {
			return &commands.DestroyCommand{Meta: meta}, nil
		},
		"enter": func() (cli.Command, error) {
			return &commands.EnterCommand{Meta: meta}, nil
		},
		"exists": func() (cli.Command, error) {
			return &commands.ExistsCommand{Meta: meta}, nil
		},
		"expose": func() (cli.Command, error) {
			return &commands.ExposeCommand{Meta: meta}, nil
		},
		"export": func() (cli.Command, error) {
			return &commands.ExportCommand{Meta: meta}, nil
		},
		"import": func() (cli.Command, error) {
			return &commands.ImportCommand{Meta: meta}, nil
		},
		"info": func() (cli.Command, error) {
			return &commands.InfoCommand{Meta: meta}, nil
		},
		"list": func() (cli.Command, error) {
			return &commands.ListCommand{Meta: meta}, nil
		},
		"link": func() (cli.Command, error) {
			return &commands.LinkCommand{Meta: meta}, nil
		},
		"linked": func() (cli.Command, error) {
			return &commands.LinkedCommand{Meta: meta}, nil
		},
		"links": func() (cli.Command, error) {
			return &commands.LinksCommand{Meta: meta}, nil
		},
		"unlink": func() (cli.Command, error) {
			return &commands.UnlinkCommand{Meta: meta}, nil
		},
		"promote": func() (cli.Command, error) {
			return &commands.PromoteCommand{Meta: meta}, nil
		},
		"restart": func() (cli.Command, error) {
			return &commands.RestartCommand{Meta: meta}, nil
		},
		"logs": func() (cli.Command, error) {
			return &commands.LogsCommand{Meta: meta}, nil
		},
		"pause": func() (cli.Command, error) {
			return &commands.PauseCommand{Meta: meta}, nil
		},
		"start": func() (cli.Command, error) {
			return &commands.StartCommand{Meta: meta}, nil
		},
		"stop": func() (cli.Command, error) {
			return &commands.StopCommand{Meta: meta}, nil
		},
		"trigger-post-app-clone-setup": func() (cli.Command, error) {
			return &commands.TriggerPostAppCloneSetupCommand{Meta: meta}, nil
		},
		"trigger-post-app-rename-setup": func() (cli.Command, error) {
			return &commands.TriggerPostAppRenameSetupCommand{Meta: meta}, nil
		},
		"trigger-pre-delete": func() (cli.Command, error) {
			return &commands.TriggerPreDeleteCommand{Meta: meta}, nil
		},
		"trigger-pre-restore": func() (cli.Command, error) {
			return &commands.TriggerPreRestoreCommand{Meta: meta}, nil
		},
		"trigger-pre-start": func() (cli.Command, error) {
			return &commands.TriggerPreStartCommand{Meta: meta}, nil
		},
		"trigger-service-list": func() (cli.Command, error) {
			return &commands.TriggerServiceListCommand{Meta: meta}, nil
		},
		"unexpose": func() (cli.Command, error) {
			return &commands.UnexposeCommand{Meta: meta}, nil
		},
		"connect": func() (cli.Command, error) {
			return &commands.ConnectCommand{Meta: meta}, nil
		},
		"set": func() (cli.Command, error) {
			return &commands.SetCommand{Meta: meta}, nil
		},
		"upgrade": func() (cli.Command, error) {
			return &commands.UpgradeCommand{Meta: meta}, nil
		},
		"version": func() (cli.Command, error) {
			return &command.VersionCommand{Meta: meta}, nil
		},
	}
}
