package service

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// BackupStorageClassProperty is the s3 storage class a service's backups are
// uploaded with. Empty leaves the upload on the bucket's default, which is what
// every backup was uploaded with before there was anything to say here.
//
// Read when a backup runs rather than when a container is made, so a change
// lands on the next backup, scheduled ones included.
const BackupStorageClassProperty = "backup-storage-class"

// backupStorageClasses are the storage classes `aws s3 cp --storage-class`
// takes in the backup image. The image refuses anything else, and only once
// the service has already been exported.
var backupStorageClasses = []string{"STANDARD", "REDUCED_REDUNDANCY", "STANDARD_IA", "ONEZONE_IA", "INTELLIGENT_TIERING", "GLACIER", "DEEP_ARCHIVE", "GLACIER_IR"}

// ValidateBackupStorageClass reports whether a value is a storage class the
// backup image will upload with. An empty value is valid and means the bucket's
// default.
func ValidateBackupStorageClass(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	if slices.Contains(backupStorageClasses, value) {
		return nil
	}

	return fmt.Errorf("invalid %s value %q, must be one of [%s]", BackupStorageClassProperty, value, strings.Join(backupStorageClasses, ", "))
}

// BackupStorageClass is the storage class a service's backups are uploaded
// with, empty when it was given none
func BackupStorageClass(s *Datastore, serviceName string) string {
	return strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, BackupStorageClassProperty))
}
