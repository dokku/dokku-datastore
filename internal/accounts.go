package internal

import (
	"context"
	"fmt"
	"strings"

	"github.com/dokku/dokku-datastore/internal/service"
)

// AccountInput is the input for the functions that make and remove the
// databases and users a service holds beside its own
type AccountInput struct {
	// Datastore is the service's datastore
	Datastore *service.Datastore

	// ServiceName is the service the database or user is in
	ServiceName string

	// Name is the database or user
	Name string

	// Password is a new user's password, generated when empty
	Password string

	// Logger reports what is done
	Logger Ui
}

// CreateDatabase makes a database in a service, which every user the service
// holds is given access to
func CreateDatabase(ctx context.Context, input AccountInput) error {
	if err := service.ValidateNewDatabase(input.Datastore, input.ServiceName, input.Name); err != nil {
		return err
	}

	input.Logger.Info(fmt.Sprintf("Creating database %s in %s", input.Name, input.ServiceName))
	return input.Datastore.CreateDatabase(ctx, input.ServiceName, input.Name)
}

// DestroyDatabase removes a database made in a service. One an app is still
// linked to is refused, since the app would be left holding a url to nothing.
func DestroyDatabase(ctx context.Context, input AccountInput) error {
	if err := service.ValidateCreatedDatabase(input.Datastore, input.ServiceName, input.Name); err != nil {
		return err
	}

	if apps := service.AppsLinkedOn(input.Datastore, input.ServiceName, service.LinkDatabaseProperty, input.Name); len(apps) > 0 {
		return fmt.Errorf("database %s is linked to app(s): %s. Unlink them before destroying the database", input.Name, strings.Join(apps, ", "))
	}

	input.Logger.Info(fmt.Sprintf("Destroying database %s in %s", input.Name, input.ServiceName))
	return input.Datastore.DestroyDatabase(ctx, input.ServiceName, input.Name)
}

// CreateUser makes a user in a service, with access to every database it
// holds. A user given no password is given one generated the way the service's
// own was.
func CreateUser(ctx context.Context, input AccountInput) error {
	if err := service.ValidateNewUser(input.Datastore, input.ServiceName, input.Name); err != nil {
		return err
	}

	password, err := accountPassword(input.Datastore, input.Password)
	if err != nil {
		return err
	}

	input.Logger.Info(fmt.Sprintf("Creating user %s in %s", input.Name, input.ServiceName))
	return input.Datastore.CreateUser(ctx, input.ServiceName, input.Name, password)
}

// DestroyUser removes a user made in a service. One an app is still linked as
// is refused, since the app would be left holding a url nothing answers.
func DestroyUser(ctx context.Context, input AccountInput) error {
	if err := service.ValidateCreatedUser(input.Datastore, input.ServiceName, input.Name); err != nil {
		return err
	}

	if apps := service.AppsLinkedOn(input.Datastore, input.ServiceName, service.LinkUserProperty, input.Name); len(apps) > 0 {
		return fmt.Errorf("user %s is linked to app(s): %s. Unlink them before destroying the user", input.Name, strings.Join(apps, ", "))
	}

	input.Logger.Info(fmt.Sprintf("Destroying user %s in %s", input.Name, input.ServiceName))
	return input.Datastore.DestroyUser(ctx, input.ServiceName, input.Name)
}

// accountPassword is the password a user is given: the one asked for, held to
// what a service's own password may be, or one generated at the length the
// service's own is
func accountPassword(s *service.Datastore, password string) (string, error) {
	if password != "" {
		if err := validateSecretValue("password")(password); err != nil {
			return "", err
		}

		return password, nil
	}

	users := s.Definition.Dokku.Users
	if users == nil {
		return "", fmt.Errorf("%s does not support users", s.Title())
	}

	generated, err := service.GenerateRandomHexString(s.Definition.Dokku.Secrets[users.Secret].Length)
	if err != nil {
		return "", fmt.Errorf("unable to generate a password: %w", err)
	}

	return generated, nil
}
