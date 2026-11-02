package internal

import (
	"context"
	"errors"
	"fmt"

	"github.com/dokku/dokku-datastore/internal/service"
)

// connectLink puts an app on the network its link to a service runs over: the
// network is made if it is missing, the service is attached to it, and every
// running container of the app joins it. The containers the app makes from now
// on join it from the post-container-create trigger.
//
// Only once all of that worked is the container link an earlier version gave
// the app removed, so a link that cannot be moved keeps working the way it
// did. A network this call made is removed again when what follows fails,
// while one that was already there is left, as it is what an app that was
// already moved reaches the service over. The app's config is never touched:
// the service answers to the same host on the network as through the link.
func connectLink(ctx context.Context, logger Ui, s *service.Datastore, serviceName string, appName string) error {
	created, err := service.EnsureLinkNetwork(ctx, service.EnsureLinkNetworkInput{
		AppName:     appName,
		Datastore:   s,
		ServiceName: serviceName,
	})
	if err != nil {
		return undoLinkNetwork(ctx, s, serviceName, appName, created, err)
	}

	containerIDs, err := service.AppContainerIDs(ctx, appName)
	if err != nil {
		return undoLinkNetwork(ctx, s, serviceName, appName, created, err)
	}

	for _, containerID := range containerIDs {
		if err := connectAppContainer(ctx, logger, s, serviceName, appName, containerID); err != nil {
			return undoLinkNetwork(ctx, s, serviceName, appName, created, err)
		}
	}

	if service.HasLegacyLink(s, serviceName, appName) {
		logger.Info(fmt.Sprintf("Removing the deprecated docker link from app %s", appName))
		if err := service.RemoveLegacyLinkOption(ctx, s, serviceName, appName); err != nil {
			return err
		}
	}

	return nil
}

// undoLinkNetwork removes the network a failed connectLink made, and returns
// the failure along with any trouble removing it
func undoLinkNetwork(ctx context.Context, s *service.Datastore, serviceName string, appName string, created bool, cause error) error {
	if !created {
		return cause
	}

	if err := service.RemoveLinkNetwork(ctx, service.LinkNetworkName(s, serviceName, appName)); err != nil {
		return errors.Join(cause, err)
	}

	return cause
}

// connectAppContainer attaches one container of an app to the network of its
// link to a service, saying so, since nothing in the app's docker options shows
// the link. A container on the host's network can join no other, as it could
// not be given a container link either, so it is warned about and left as it
// is.
func connectAppContainer(ctx context.Context, logger Ui, s *service.Datastore, serviceName string, appName string, containerID string) error {
	logger.Info(fmt.Sprintf("Attaching container %s to network %s for the link to %s service %s", shortContainerID(containerID), service.LinkNetworkName(s, serviceName, appName), s.Properties().CommandPrefix, serviceName))

	err := service.ConnectAppContainer(ctx, s, serviceName, appName, containerID)
	if errors.Is(err, service.ErrHostNetworkContainer) {
		logger.Warn(WarnInput{
			Warning: fmt.Sprintf("Container %s of app %s uses the host network, so it cannot reach service %s over the network of its link", containerID, appName, serviceName),
		})
		return nil
	}

	return err
}

// shortContainerID is a container id the length docker prints it at
func shortContainerID(containerID string) string {
	if len(containerID) > 12 {
		return containerID[:12]
	}

	return containerID
}

// removeServiceLinkNetworks removes every network a service's links run over,
// including any left behind for an app that is no longer linked
func removeServiceLinkNetworks(ctx context.Context, s *service.Datastore, serviceName string) error {
	networks, err := service.ServiceLinkNetworks(ctx, s, serviceName)
	if err != nil {
		return err
	}

	for _, network := range networks {
		if err := service.RemoveLinkNetwork(ctx, network); err != nil {
			return err
		}
	}

	return nil
}
