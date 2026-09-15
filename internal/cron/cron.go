// Package cron installs and removes the crontab entry a scheduled backup runs
// from. The cron directory belongs to root, so the work is done through a helper
// script the plugin installs and the dokku group is granted through sudo.
package cron

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/dokku/dokku-datastore/internal/execx"

	"github.com/dokku/dokku/plugins/common"
)

// helperDir is where the helper script is installed. It has to be a directory
// root owns: the dokku group is allowed to run the script as root, so being able
// to replace it, or to replace the directory holding it, would be a way to run
// anything as root.
const helperDir = "/usr/local/bin"

// serviceNamePattern is the same rule the binary validates a service name
// against. The helper applies it again before interpolating a name into a path,
// because sudo can no longer constrain the argument on its behalf.
var serviceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// HelperPath is where a datastore's cron helper is installed.
func HelperPath(plugin string) string {
	return filepath.Join(helperDir, fmt.Sprintf("dokku-%s-cron", plugin))
}

// helperTemplate is the helper script.
//
// It exists because sudo-rs, the default sudo on Ubuntu 25.10 and later, allows
// neither wildcards nor regular expressions in a command argument. The rules
// this replaces used a wildcard to stand for the service name, so sudo-rs
// rejected the whole file and the dokku group was left with no privileges at
// all, which broke backup scheduling outright. A regular expression is no help:
// sudo-rs parses one and then matches it as a literal string.
//
// So the argument is no longer constrained by sudo, and this script constrains
// it instead. That is narrower than what it replaces, not wider: the dokku group
// could previously run mv, chown, chmod and rm as root against anything matching
// a glob, and can now only run this.
const helperTemplate = `#!/usr/bin/env bash
set -eo pipefail

# installed by the dokku %[1]s plugin. do not edit: it is rewritten on every
# plugin install and update.

PLUGIN=%[1]q
DATA_ROOT=%[2]q

usage() {
  echo "usage: $(basename "$0") install <service>" >&2
  echo "       $(basename "$0") remove <service>" >&2
  exit 2
}

# validate_service is called directly rather than from a command substitution: a
# subshell exiting non-zero would leave the caller running with an empty name
validate_service() {
  declare service="$1"

  if [[ ! "$service" =~ ^[A-Za-z0-9_-]+$ ]]; then
    echo "refusing to act on an invalid service name" >&2
    exit 1
  fi
}

main() {
  [[ $# -eq 2 ]] || usage
  declare action="$1" service="$2"

  validate_service "$service"
  declare cron_file="/etc/cron.d/dokku-${PLUGIN}-${service}"

  # derived from the name that was just validated, so this is still a path the
  # caller cannot choose
  declare staged_cron_file="${DATA_ROOT}/${service}/.TMP_CRON_FILE"

  case "$action" in
    install)
      if [[ ! -f "$staged_cron_file" ]]; then
        echo "no cron file staged at $staged_cron_file" >&2
        exit 1
      fi

      mv "$staged_cron_file" "$cron_file"
      chown root:root "$cron_file"
      chmod 644 "$cron_file"
      ;;
    remove)
      rm -f "$cron_file"
      ;;
    *)
      usage
      ;;
  esac
}

main "$@"
`

// HelperContents returns the helper script for a datastore. The data root is
// where that datastore's services live, so that the helper can derive a staged
// path from the service name it validates rather than being handed one.
func HelperContents(plugin string, dataRoot string) string {
	return fmt.Sprintf(helperTemplate, plugin, dataRoot)
}

// HelperFile describes the helper script a datastore plugin installs. It belongs
// to root and is not group writable, so that the group allowed to run it as root
// cannot change what it does.
func HelperFile(plugin string, dataRoot string) common.WriteStringToFileInput {
	return common.WriteStringToFileInput{
		Content:   HelperContents(plugin, dataRoot),
		Filename:  HelperPath(plugin),
		GroupName: "root",
		Mode:      0755,
		Username:  "root",
	}
}

// SudoersContents returns the sudoers file a datastore plugin installs. One rule,
// no arguments, so there is nothing in it for sudo-rs to reject.
func SudoersContents(plugin string) string {
	return fmt.Sprintf("%%dokku ALL=(ALL) NOPASSWD:%s\n", HelperPath(plugin))
}

// SudoersFile describes that file. It has to belong to root: sudo refuses a file
// writable by anyone else, and the dokku user must not be able to rewrite the
// privileges it is being granted.
func SudoersFile(plugin string) common.WriteStringToFileInput {
	return common.WriteStringToFileInput{
		Content:   SudoersContents(plugin),
		Filename:  filepath.Join("/etc/sudoers.d", fmt.Sprintf("dokku-%s", plugin)),
		GroupName: "root",
		Mode:      0440,
		Username:  "root",
	}
}

// Install moves a staged cron file into place for a service.
func Install(ctx context.Context, plugin string, serviceName string) error {
	return run(ctx, plugin, "install", serviceName)
}

// Remove deletes a service's cron file.
func Remove(ctx context.Context, plugin string, serviceName string) error {
	return run(ctx, plugin, "remove", serviceName)
}

// run calls the helper through sudo.
func run(ctx context.Context, plugin string, action string, serviceName string) error {
	if !serviceNamePattern.MatchString(serviceName) {
		return fmt.Errorf("refusing to %s a cron file for an invalid service name", action)
	}

	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command: "sudo",
		Args:    []string{HelperPath(plugin), action, serviceName},
	}); err != nil {
		return fmt.Errorf("unable to %s the cron file: %w", action, err)
	}

	return nil
}
