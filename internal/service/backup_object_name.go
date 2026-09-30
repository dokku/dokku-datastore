package service

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// BackupObjectNameProperty is the name a service's backups are uploaded under,
// ahead of the timestamp and extension the backup image adds. Empty keeps the
// name every backup was uploaded under before there was anything to say here,
// the command prefix and the service name.
//
// Read when a backup runs rather than when a container is made, so a change
// lands on the next backup, scheduled ones included.
const BackupObjectNameProperty = "backup-object-name"

// BackupTimestampProperty says whether a service's backups are uploaded under
// a key ending in the time they started. Set to false, every backup is uploaded
// to the same key, so that bucket versioning and lifecycle rules can keep and
// rotate them. Empty keeps the timestamp.
const BackupTimestampProperty = "backup-timestamp"

// backupObjectNamePattern is what an object name may be made of: the same
// characters a bucket name may hold, which s3 and every s3 compatible service
// take in a key without escaping
var backupObjectNamePattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// maxBackupObjectNameLength is well under the 1024 bytes s3 allows a key,
// leaving room for a path in the bucket name, the timestamp and the extension
const maxBackupObjectNameLength = 512

// ValidateBackupObjectName reports whether a value can name a service's
// backups. An empty value is valid and means the default name.
func ValidateBackupObjectName(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	if len(value) > maxBackupObjectNameLength {
		return fmt.Errorf("invalid %s value %q, must be at most %d characters", BackupObjectNameProperty, value, maxBackupObjectNameLength)
	}

	if !backupObjectNamePattern.MatchString(value) {
		return fmt.Errorf("invalid %s value %q, only letters, numbers, dots, dashes, underscores and slashes are allowed", BackupObjectNameProperty, value)
	}

	// a path that s3 would store as written but that no one would read back the
	// same way: an empty segment from a leading, trailing or doubled slash, or a
	// segment a client would resolve as the current or parent folder
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("invalid %s value %q, must not start or end with a slash, or hold an empty, . or .. segment", BackupObjectNameProperty, value)
		}
	}

	return nil
}

// BackupObjectName is the name a service's backups are uploaded under, empty
// when it was given none
func BackupObjectName(s *Datastore, serviceName string) string {
	return strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, BackupObjectNameProperty))
}

// ValidateBackupTimestamp reports whether a value says whether backups are
// timestamped. An empty value is valid and keeps the timestamp.
func ValidateBackupTimestamp(value string) error {
	switch strings.TrimSpace(value) {
	case "", "true", "false":
		return nil
	}

	return fmt.Errorf("invalid %s value %q, must be one of [true, false]", BackupTimestampProperty, value)
}

// BackupTimestamp reports whether a service's backups are uploaded under a key
// ending in the time they started, which they are unless it was set to false
func BackupTimestamp(s *Datastore, serviceName string) bool {
	return strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, BackupTimestampProperty)) != "false"
}
