package internal

import (
	"context"
	"errors"
	"fmt"

	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/service"
	"github.com/dokku/dokku/plugins/common"
)

// LegacyLinkOption is the docker option earlier versions of the plugin linked
// an app to a service with. Docker deprecated links on the default bridge in
// 29.6 and removes them in 30.0, so a link now runs over a network instead.
func LegacyLinkOption(s *service.Datastore, serviceName string) string {
	return fmt.Sprintf("--link %s:%s", service.ContainerName(s, serviceName), service.DNSHostname(s, serviceName))
}

// removeLegacyLinkOption removes the docker option an earlier version of the
// plugin linked the app with. dokku does nothing for an option the app does not
// have, so it is safe to repeat.
func removeLegacyLinkOption(ctx context.Context, s *service.Datastore, serviceName string, appName string) error {
	_, err := execx.Run(ctx, common.ExecCommandInput{
		Command: "dokku",
		Args:    []string{"docker-options:remove", appName, "build,deploy,run", LegacyLinkOption(s, serviceName)},
	})
	if err != nil {
		return fmt.Errorf("failed to remove the docker option for app %s: %w", appName, err)
	}

	return nil
}

// connectLink puts an app on the network its link to a service runs over: the
// network is made if it is missing, the service is attached to it, the docker
// option an earlier version linked the app with is removed, and every running
// container of the app joins it. The containers the app makes from now on join
// it from the post-container-create trigger.
func connectLink(ctx context.Context, logger Ui, s *service.Datastore, serviceName string, appName string) error {
	if err := service.EnsureLinkNetwork(ctx, service.EnsureLinkNetworkInput{
		AppName:     appName,
		Datastore:   s,
		ServiceName: serviceName,
	}); err != nil {
		return err
	}

	if err := removeLegacyLinkOption(ctx, s, serviceName, appName); err != nil {
		return err
	}

	containerIDs, err := service.AppContainerIDs(ctx, appName)
	if err != nil {
		return err
	}

	for _, containerID := range containerIDs {
		if err := connectAppContainer(ctx, logger, s, serviceName, appName, containerID); err != nil {
			return err
		}
	}

	return nil
}

// connectAppContainer attaches one container of an app to the network of its
// link to a service. A container on the host's network can join no other, as
// it could not be given a container link either, so it is warned about and
// left as it is.
func connectAppContainer(ctx context.Context, logger Ui, s *service.Datastore, serviceName string, appName string, containerID string) error {
	err := service.ConnectAppContainer(ctx, s, serviceName, appName, containerID)
	if errors.Is(err, service.ErrHostNetworkContainer) {
		logger.Warn(WarnInput{
			Warning: fmt.Sprintf("Container %s of app %s uses the host network, so it cannot reach service %s over the network of its link", containerID, appName, serviceName),
		})
		return nil
	}

	return err
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
