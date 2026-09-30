package service

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// BackupMailtoProperty is who cron mails the output of a service's scheduled
// backup to, as a comma-separated list of email addresses or local users. Empty
// leaves the output to the global MAILTO dokku writes its crontab with.
//
// Read when dokku writes its crontab rather than when a backup runs, so setting
// it has dokku write the crontab again.
const BackupMailtoProperty = "backup-mailto"

// backupMailtoRecipientPattern is what each recipient may be made of: an email
// address, or a local user on the host. The recipients are written into a
// MAILTO line in the dokku crontab, so anything that would end that line or
// that cron reads specially is refused.
var backupMailtoRecipientPattern = regexp.MustCompile(`^[A-Za-z0-9._+-]+(@[A-Za-z0-9.-]+)?$`)

// ValidateBackupMailto reports whether a value can be written into the MAILTO
// line of a service's scheduled backup. An empty value is valid, and leaves the
// output to the global MAILTO.
func ValidateBackupMailto(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	for _, recipient := range strings.Split(value, ",") {
		if !backupMailtoRecipientPattern.MatchString(recipient) {
			return fmt.Errorf("invalid %s value %q, must be a comma-separated list of email addresses or local users, without spaces", BackupMailtoProperty, value)
		}
	}

	return nil
}

// BackupMailto is who cron mails the output of a service's scheduled backup to,
// empty when it was given no one
func BackupMailto(s *Datastore, serviceName string) string {
	return strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, BackupMailtoProperty))
}
