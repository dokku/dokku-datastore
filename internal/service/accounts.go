package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku/plugins/common"
)

// LinkDatabaseProperty records, for each app linked to a service on a database
// other than the one named for it, which database that is.
//
// The url an app holds is found by rendering it again, so a link made on
// another database is only found by rendering it on that database. An app
// linked on the service's own database records nothing, which is what every
// link made before a service could hold more than one looks like.
const LinkDatabaseProperty = "link-database"

// LinkUserProperty records, for each app linked to a service as an account
// other than the service's own, which account that is, for the same reason
// LinkDatabaseProperty records the database.
const LinkUserProperty = "link-user"

// accountNamePattern is what a database or account made in a service may be
// called. It is written into statements and urls without being escaped, so it
// is held to what every datastore takes as an identifier as it is, and to the
// shortest limit among them, which is mysql's thirty two characters for an
// account.
var accountNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,31}$`)

// Account is the database and user a link or a command addresses. An empty
// field is the service's own.
type Account struct {
	// Database is the database, empty for the one named for the service
	Database string

	// User is the account, empty for the one the service was created with
	User string
}

// IsDefault reports whether the account is the service's own database and user
func (a Account) IsDefault() bool {
	return a.Database == "" && a.User == ""
}

// SupportsDatabases reports whether a service can be addressed on a database
// other than the one named for it
func (s *Datastore) SupportsDatabases() bool {
	return s.Definition.Dokku.Databases != nil
}

// SupportsUsers reports whether a service can be addressed as an account other
// than the one it was created with
func (s *Datastore) SupportsUsers() bool {
	return s.Definition.Dokku.Users != nil
}

// DefaultDatabase is the database a link is on when it names none: the one
// named for the service, or the default index of a datastore with a fixed set
func (s *Datastore) DefaultDatabase(serviceName string) string {
	if databases := s.Definition.Dokku.Databases; databases != nil && !databases.Makes() {
		return databases.Default
	}

	return DatabaseName(s, serviceName)
}

// DefaultUsername is the account the service was created with, empty for a
// datastore that cannot make any other
func (s *Datastore) DefaultUsername(serviceName string) string {
	return s.scope(serviceName).DefaultUsername
}

// CreatedDatabases are the databases made in a service beside the one named
// for it, sorted
func CreatedDatabases(s *Datastore, serviceName string) []string {
	return recordedNames(Folders(s, serviceName).Databases)
}

// CreatedUsers are the accounts made in a service beside its own, sorted
func CreatedUsers(s *Datastore, serviceName string) []string {
	return recordedNames(Folders(s, serviceName).Auth)
}

// recordedNames are the names a record directory holds a file for, sorted. A
// file the tool did not write, such as one a rename left behind, is passed
// over rather than handed to a datastore as a name.
func recordedNames(directory string) []string {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return []string{}
	}

	names := []string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() && accountNamePattern.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)

	return names
}

// Databases are every database a service holds that a link can be on: the one
// named for it and every one made beside it, or every index of a datastore with
// a fixed set
func Databases(s *Datastore, serviceName string) []string {
	databases := s.Definition.Dokku.Databases
	if databases == nil {
		return []string{}
	}

	if !databases.Makes() {
		indexes := make([]string, 0, databases.Fixed)
		for index := range databases.Fixed {
			indexes = append(indexes, strconv.Itoa(index))
		}

		return indexes
	}

	return append([]string{DatabaseName(s, serviceName)}, CreatedDatabases(s, serviceName)...)
}

// Users are every account a service holds: the one it was created with and
// every one made beside it
func Users(s *Datastore, serviceName string) []string {
	if !s.SupportsUsers() {
		return []string{}
	}

	return append([]string{s.DefaultUsername(serviceName)}, CreatedUsers(s, serviceName)...)
}

// DatabasesSummary is the databases a service holds as info reports them: a
// range for a fixed set, which would otherwise be every index spelled out
func DatabasesSummary(s *Datastore, serviceName string) string {
	databases := s.Definition.Dokku.Databases
	if databases == nil {
		return ""
	}

	if !databases.Makes() {
		return fmt.Sprintf("0-%d", databases.Fixed-1)
	}

	return strings.Join(Databases(s, serviceName), " ")
}

// UserPassword is the password of an account a service holds, empty for one it
// does not
func UserPassword(s *Datastore, serviceName string, user string) string {
	users := s.Definition.Dokku.Users
	if users == nil {
		return ""
	}

	if user == "" || user == s.DefaultUsername(serviceName) {
		return s.SecretValue(serviceName, users.Secret)
	}

	if !accountNamePattern.MatchString(user) {
		return ""
	}

	return common.ReadFirstLine(filepath.Join(Folders(s, serviceName).Auth, user))
}

// ValidateAccount reports whether a service holds the database and user an
// account names, for a link or a command about to address it
func ValidateAccount(s *Datastore, serviceName string, account Account) error {
	if account.Database != "" {
		if !s.SupportsDatabases() {
			return fmt.Errorf("%s does not support linking or addressing a database other than the service's own", s.Title())
		}

		if !slices.Contains(Databases(s, serviceName), account.Database) {
			if databases := s.Definition.Dokku.Databases; !databases.Makes() {
				return fmt.Errorf("database %s is not one of the %d databases of service %s, which are numbered from 0", account.Database, databases.Fixed, serviceName)
			}

			return fmt.Errorf("service %s has no database %s", serviceName, account.Database)
		}
	}

	if account.User != "" {
		if !s.SupportsUsers() {
			return fmt.Errorf("%s does not support linking or addressing a user other than the service's own", s.Title())
		}

		if !slices.Contains(Users(s, serviceName), account.User) {
			return fmt.Errorf("service %s has no user %s", serviceName, account.User)
		}
	}

	return nil
}

// ValidateNewDatabase reports whether a database may be made in a service
// under a name
func ValidateNewDatabase(s *Datastore, serviceName string, name string) error {
	if !s.Definition.Dokku.Databases.Makes() {
		return fmt.Errorf("%s does not support creating databases", s.Title())
	}

	if !accountNamePattern.MatchString(name) {
		return fmt.Errorf("invalid database name %s, it may only contain lowercase letters, digits and '_', start with a letter or '_', and be at most 32 characters long", name)
	}

	for _, reserved := range s.Definition.Dokku.ReservedNames {
		if name == reserved {
			return fmt.Errorf("%s is a database the %s datastore keeps for itself", name, s.Definition.Dokku.Plugin)
		}
	}

	if slices.Contains(Databases(s, serviceName), name) {
		return fmt.Errorf("service %s already has a database %s", serviceName, name)
	}

	return nil
}

// ValidateNewUser reports whether an account may be made in a service under a
// name
func ValidateNewUser(s *Datastore, serviceName string, name string) error {
	users := s.Definition.Dokku.Users
	if users == nil {
		return fmt.Errorf("%s does not support creating users", s.Title())
	}

	if !accountNamePattern.MatchString(name) {
		return fmt.Errorf("invalid user name %s, it may only contain lowercase letters, digits and '_', start with a letter or '_', and be at most 32 characters long", name)
	}

	for _, reserved := range users.Reserved {
		if name == reserved {
			return fmt.Errorf("%s is an account the %s datastore keeps for itself", name, s.Definition.Dokku.Plugin)
		}
	}

	if slices.Contains(Users(s, serviceName), name) {
		return fmt.Errorf("service %s already has a user %s", serviceName, name)
	}

	return nil
}

// ValidateCreatedDatabase reports whether a database is one made in a service,
// which is the only kind that can be destroyed
func ValidateCreatedDatabase(s *Datastore, serviceName string, name string) error {
	if !s.Definition.Dokku.Databases.Makes() {
		return fmt.Errorf("%s does not support destroying databases", s.Title())
	}

	if name == DatabaseName(s, serviceName) {
		return fmt.Errorf("database %s is the one service %s was created with, which is only removed with the service", name, serviceName)
	}

	if !slices.Contains(CreatedDatabases(s, serviceName), name) {
		return fmt.Errorf("service %s has no database %s", serviceName, name)
	}

	return nil
}

// ValidateCreatedUser reports whether an account is one made in a service,
// which is the only kind that can be destroyed or given a password by name
func ValidateCreatedUser(s *Datastore, serviceName string, name string) error {
	if !s.SupportsUsers() {
		return fmt.Errorf("%s does not support users", s.Title())
	}

	if name == s.DefaultUsername(serviceName) {
		return fmt.Errorf("user %s is the one service %s was created with, which is only removed with the service, and whose password is changed with `dokku %s:set %s password`", name, serviceName, s.Properties().CommandPrefix, serviceName)
	}

	if !slices.Contains(CreatedUsers(s, serviceName), name) {
		return fmt.Errorf("service %s has no user %s", serviceName, name)
	}

	return nil
}

// recordFile writes a record of a database or account, private because an
// account's holds its password
func recordFile(directory string, name string, content string) error {
	if err := os.MkdirAll(directory, 0750); err != nil {
		return fmt.Errorf("unable to make %s: %w", directory, err)
	}

	if err := common.SetPermissions(common.SetPermissionInput{
		Filename:  directory,
		GroupName: hostenv.SystemGroup(),
		Mode:      0750,
		Username:  hostenv.SystemUser(),
	}); err != nil {
		return fmt.Errorf("unable to set the permissions of %s: %w", directory, err)
	}

	filename := filepath.Join(directory, name)
	if err := ReplaceFileAtomically(filename, content, PrivateFileMode); err != nil {
		return fmt.Errorf("unable to write %s: %w", filename, err)
	}

	return nil
}

// forgetFile removes a record, which is already gone when there is nothing to
// remove
func forgetFile(directory string, name string) error {
	filename := filepath.Join(directory, name)
	if err := os.Remove(filename); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("unable to remove %s: %w", filename, err)
	}

	return nil
}

// accountArguments are what the databases and users commands are handed: the
// names they act on, and every database and account the service holds, space
// separated, so a datastore can give one access to the others
func (s *Datastore) accountArguments(serviceName string, values map[string]string) map[string]string {
	arguments := map[string]string{
		"databases": strings.Join(Databases(s, serviceName), " "),
		"users":     strings.Join(Users(s, serviceName), " "),
	}
	for key, value := range values {
		arguments[key] = value
	}

	return arguments
}

// runAccountCommand runs one of the databases or users commands against the
// running service
func (s *Datastore) runAccountCommand(ctx context.Context, serviceName string, name string, command *definition.Command, arguments map[string]string, newSecret string) error {
	if command == nil {
		return fmt.Errorf("the %s datastore declares no %s command", s.Definition.Dokku.Plugin, name)
	}

	switch Status(ctx, StatusInput{Datastore: s, ServiceName: serviceName}) {
	case "running", "restarting":
	default:
		return fmt.Errorf("service %s is not running: start it with `dokku %s:start %s`", serviceName, s.Properties().CommandPrefix, serviceName)
	}

	return s.run(ctx, serviceName, name, runOptions{
		Arguments: arguments,
		Command:   command,
		NewSecret: newSecret,
		Stdout:    os.Stderr,
		Stderr:    os.Stderr,
	})
}

// CreateDatabase makes a database in a running service, and records it once
// the datastore has made it
func (s *Datastore) CreateDatabase(ctx context.Context, serviceName string, name string) error {
	if err := ValidateNewDatabase(s, serviceName, name); err != nil {
		return err
	}

	databases := s.Definition.Dokku.Databases
	if err := s.runAccountCommand(ctx, serviceName, definition.DatabasesCreateName, databases.Create, map[string]string{"database": name}, ""); err != nil {
		return fmt.Errorf("unable to create database %s: %w", name, err)
	}

	return recordFile(Folders(s, serviceName).Databases, name, name)
}

// DestroyDatabase removes a database made in a running service, and forgets it
// once the datastore has removed it
func (s *Datastore) DestroyDatabase(ctx context.Context, serviceName string, name string) error {
	if err := ValidateCreatedDatabase(s, serviceName, name); err != nil {
		return err
	}

	databases := s.Definition.Dokku.Databases
	if err := s.runAccountCommand(ctx, serviceName, definition.DatabasesDestroyName, databases.Destroy, map[string]string{"database": name}, ""); err != nil {
		return fmt.Errorf("unable to destroy database %s: %w", name, err)
	}

	return forgetFile(Folders(s, serviceName).Databases, name)
}

// CreateUser makes an account in a running service, with access to every
// database it holds, and records its password once the datastore has made it
func (s *Datastore) CreateUser(ctx context.Context, serviceName string, name string, password string) error {
	if err := ValidateNewUser(s, serviceName, name); err != nil {
		return err
	}

	users := s.Definition.Dokku.Users
	if err := s.runAccountCommand(ctx, serviceName, definition.UsersCreateName, users.Create, map[string]string{"user": name}, password); err != nil {
		return fmt.Errorf("unable to create user %s: %w", name, err)
	}

	if err := recordFile(Folders(s, serviceName).Auth, name, password); err != nil {
		return fmt.Errorf("user %s was created, but its password could not be recorded: %w", name, err)
	}

	return nil
}

// DestroyUser removes an account made in a running service, and forgets it
// once the datastore has removed it
func (s *Datastore) DestroyUser(ctx context.Context, serviceName string, name string) error {
	if err := ValidateCreatedUser(s, serviceName, name); err != nil {
		return err
	}

	users := s.Definition.Dokku.Users
	if err := s.runAccountCommand(ctx, serviceName, definition.UsersDestroyName, users.Destroy, map[string]string{"user": name}, ""); err != nil {
		return fmt.Errorf("unable to destroy user %s: %w", name, err)
	}

	return forgetFile(Folders(s, serviceName).Auth, name)
}

// ChangeUserPassword gives an account made in a running service a new
// password, and records it once the datastore takes it
func (s *Datastore) ChangeUserPassword(ctx context.Context, serviceName string, name string, password string) error {
	if err := ValidateCreatedUser(s, serviceName, name); err != nil {
		return err
	}

	users := s.Definition.Dokku.Users
	if err := s.runAccountCommand(ctx, serviceName, definition.UsersChangeName, users.Change, map[string]string{"user": name}, password); err != nil {
		return fmt.Errorf("unable to change the password of user %s: %w", name, err)
	}

	if err := recordFile(Folders(s, serviceName).Auth, name, password); err != nil {
		return fmt.Errorf("the datastore now takes the new password of user %s, but it could not be recorded: %w", name, err)
	}

	return nil
}

// LinkAccount is the database and user an app is linked to a service on, as
// they were recorded when it was linked
func LinkAccount(s *Datastore, serviceName string, appName string) Account {
	return Account{
		Database: recordedValue(s, serviceName, appName, LinkDatabaseProperty),
		User:     recordedValue(s, serviceName, appName, LinkUserProperty),
	}
}

// recordedValue is what a property records for an app
func recordedValue(s *Datastore, serviceName string, appName string, property string) string {
	entries, err := common.PropertyMapGet(s.Properties().CommandPrefix, serviceName, property)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(entries[appName])
}

// SetLinkDatabase records the database an app is linked to a service on. The
// service's own records nothing, which removes whatever was recorded. A corrupt
// record is replaced, and ErrCorruptLinkConfigKeys returned once the write has
// succeeded.
func SetLinkDatabase(s *Datastore, serviceName string, appName string, database string) error {
	return setRecordedValue(s, serviceName, appName, LinkDatabaseProperty, database)
}

// SetLinkUser records the user an app is linked to a service as, the same way
// SetLinkDatabase records the database
func SetLinkUser(s *Datastore, serviceName string, appName string, user string) error {
	return setRecordedValue(s, serviceName, appName, LinkUserProperty, user)
}

// setRecordedValue records a value for an app in a property, removing the
// entry for an empty one
func setRecordedValue(s *Datastore, serviceName string, appName string, property string, value string) error {
	if value == "" && !common.PropertyExists(s.Properties().CommandPrefix, serviceName, property) {
		return nil
	}

	entries, corrupt, err := linkConfigKeysEntries(s, serviceName, property)
	if err != nil {
		return err
	}

	if value == "" {
		if _, ok := entries[appName]; !ok && !corrupt {
			return nil
		}

		delete(entries, appName)
	} else {
		entries[appName] = value
	}

	return writeLinkConfigKeys(s, serviceName, property, entries, corrupt)
}

// AppsLinkedOn are the apps linked to a service whose link records a database
// or user, sorted, so that one still in use is not destroyed out from under
// them
func AppsLinkedOn(s *Datastore, serviceName string, property string, value string) []string {
	entries, err := common.PropertyMapGet(s.Properties().CommandPrefix, serviceName, property)
	if err != nil {
		return []string{}
	}

	apps := []string{}
	for appName, recorded := range entries {
		if strings.TrimSpace(recorded) == value {
			apps = append(apps, appName)
		}
	}
	slices.Sort(apps)

	return apps
}

// AccountURL is the url a linked app receives for an account
func (s *Datastore) AccountURL(serviceName string, account Account, schemeOverride string) string {
	scope := s.scopeFor(serviceName, account)
	if schemeOverride != "" {
		scope.Scheme = schemeOverride
	}

	url, err := definition.Render(s.Definition.Dokku.DSN, scope)
	if err != nil {
		return ""
	}

	return url
}

// AccountJDBC is what an app linked with --jdbc receives for an account, with
// the same guarantees as JDBC
func (s *Datastore) AccountJDBC(serviceName string, account Account) (JDBCValues, bool) {
	jdbc := s.Definition.Dokku.JDBC
	if jdbc == nil {
		return JDBCValues{}, false
	}

	scope := s.scopeFor(serviceName, account)
	rendered := make([]string, 0, 3)
	for _, body := range []string{jdbc.URL, jdbc.Username, jdbc.Password} {
		value, err := definition.Render(body, scope)
		if err != nil || value == "" {
			return JDBCValues{}, false
		}
		rendered = append(rendered, value)
	}

	return JDBCValues{
		URL:      rendered[0],
		Username: rendered[1],
		Password: rendered[2],
	}, true
}

// LinkURL is the url an app linked to a service holds, on the account its link
// recorded
func (s *Datastore) LinkURL(serviceName string, appName string, schemeOverride string) string {
	return s.AccountURL(serviceName, LinkAccount(s, serviceName, appName), schemeOverride)
}

// LinkJDBC is what an app linked to a service with --jdbc holds, on the account
// its link recorded
func (s *Datastore) LinkJDBC(serviceName string, appName string) (JDBCValues, bool) {
	return s.AccountJDBC(serviceName, LinkAccount(s, serviceName, appName))
}

// scopeFor is the scope a service's templates are rendered against for an
// account: the service's own, with the database and user it names in place of
// the service's
func (s *Datastore) scopeFor(serviceName string, account Account) definition.Scope {
	scope := s.scope(serviceName)
	if account.Database != "" {
		scope.Database = account.Database
	}

	if account.User != "" {
		scope.Username = account.User
		scope.Password = UserPassword(s, serviceName, account.User)
	}

	return scope
}
