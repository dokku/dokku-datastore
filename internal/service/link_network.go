package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku/plugins/common"
)

const (
	// LinkNetworkLabel marks a network as one a link runs over
	LinkNetworkLabel = "dokku=service-link"

	// LinkNetworkServiceTypeLabel names the datastore a link network belongs to
	LinkNetworkServiceTypeLabel = "dokku.service"

	// LinkNetworkServiceNameLabel names the service a link network belongs to
	LinkNetworkServiceNameLabel = "dokku.service.name"

	// LinkNetworkAppNameLabel names the app a link network belongs to
	LinkNetworkAppNameLabel = "dokku.service.app"

	// AppNameContainerLabel is the label dokku gives every container of an app,
	// whatever phase it was made for
	AppNameContainerLabel = "com.dokku.app-name"
)

// ErrHostNetworkContainer is returned for a container sharing the host's
// network, which cannot be attached to any other network
var ErrHostNetworkContainer = errors.New("container uses the host network")

// ErrAddressPoolsExhausted is returned when docker has no address pool left to
// give a link network a subnet from. Every link has a network of its own, and
// docker's default pools only hold about 31, so a host with many links runs
// out of them.
var ErrAddressPoolsExhausted = errors.New("docker has no address pool left to create it from")

// addressPoolsExhaustedMessage is what docker answers a network create with
// once every subnet of its address pools is in use
const addressPoolsExhaustedMessage = "all predefined address pools have been fully subnetted"

// LinkNetworksReadme is the readme section that explains link networks and how
// to give docker more address pools for them
const LinkNetworksReadme = "https://github.com/dokku/dokku-datastore#link-networks"

// addressPoolsError explains how to give docker more address pools. Changing
// default-address-pools needs docker restarted, since a reload does not apply
// it. The restart stops every container unless live-restore is on, which a
// reload does apply, so that is suggested first when it is off.
type addressPoolsError struct {
	// network is the network that could not be made
	network string

	// appName is the app the network is for
	appName string

	// recreated is whether the network had been made before and went missing
	recreated bool

	// liveRestore is whether docker keeps containers running across a restart
	liveRestore bool
}

func (e *addressPoolsError) Error() string {
	lines := []string{
		fmt.Sprintf("%s: %s", networkFailurePrefix(e.recreated, e.network, e.appName), ErrAddressPoolsExhausted),
		"Give docker smaller pools by adding this to /etc/docker/daemon.json:",
		`  "default-address-pools": [{"base": "172.16.0.0/12", "size": 24}]`,
		"then restart docker with: sudo systemctl restart docker (a reload does not apply it)",
	}

	if !e.liveRestore {
		lines = append(lines, `Set "live-restore": true and run sudo systemctl reload docker first to keep containers running across the restart`)
	}

	lines = append(lines, "See "+LinkNetworksReadme)
	return strings.Join(lines, "\n")
}

func (e *addressPoolsError) Unwrap() error {
	return ErrAddressPoolsExhausted
}

// networkFailurePrefix starts the message for a network that could not be made.
// One that has to be recreated was made when the app was linked, so it is said
// to have gone missing rather than to be new.
func networkFailurePrefix(recreated bool, network string, appName string) string {
	if recreated {
		return fmt.Sprintf("Unable to recreate network %s for the link to app %s, which was removed since the app was linked", network, appName)
	}

	return fmt.Sprintf("Unable to create network %s for the link to app %s", network, appName)
}

// liveRestoreEnabled reports whether docker keeps containers running across a
// restart of the daemon. A daemon that cannot be asked is taken not to, as the
// advice that follows from it is only advice.
func liveRestoreEnabled(ctx context.Context) bool {
	result, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"info", "--format", "{{ .LiveRestoreEnabled }}"},
	})
	if err != nil {
		return false
	}

	return strings.TrimSpace(result.StdoutContents()) == "true"
}

// LinkNetworkName returns the network a link between a service and an app runs
// over. An app name cannot hold a dot, so no two links share one.
//
// Every link has a network of its own rather than every app linked to a
// service sharing one, as docker resolves the name of every container on a
// network for every other container on it: apps sharing a service would be
// able to find each other through it.
func LinkNetworkName(s *Datastore, serviceName string, appName string) string {
	return fmt.Sprintf("%s.%s", ContainerName(s, serviceName), appName)
}

// LinkNetworkCreateArgs builds the argv that creates the network for a link.
//
// The network is internal, as it only carries traffic between the app and the
// service. An internal network has no gateway, so joining it never changes the
// route either container takes to anything else.
func LinkNetworkCreateArgs(s *Datastore, serviceName string, appName string) []string {
	return []string{
		"network",
		"create",
		"--internal",
		"--label=" + LinkNetworkLabel,
		"--label=" + LinkNetworkServiceTypeLabel + "=" + s.Properties().CommandPrefix,
		"--label=" + LinkNetworkServiceNameLabel + "=" + serviceName,
		"--label=" + LinkNetworkAppNameLabel + "=" + appName,
		LinkNetworkName(s, serviceName, appName),
	}
}

// linkNetworkExists reports whether a network of the given name exists
func linkNetworkExists(ctx context.Context, network string) (bool, error) {
	result, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"network", "ls", "--quiet", "--filter", fmt.Sprintf("name=^%s$", regexp.QuoteMeta(network))},
	})
	if err != nil {
		return false, fmt.Errorf("unable to list the networks: %w", err)
	}

	return strings.TrimSpace(result.StdoutContents()) != "", nil
}

// containerNetworks returns the networks a container is attached to, and
// whether it shares the host's network
func containerNetworks(ctx context.Context, containerID string) ([]string, bool, error) {
	result, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "inspect", "--format", "{{ .HostConfig.NetworkMode }} {{ json .NetworkSettings.Networks }}", containerID},
	})
	if err != nil {
		return nil, false, fmt.Errorf("unable to inspect container %s: %w", containerID, err)
	}

	mode, networksJSON, _ := strings.Cut(strings.TrimSpace(result.StdoutContents()), " ")
	if mode == "host" {
		return nil, true, nil
	}

	networks := map[string]json.RawMessage{}
	if networksJSON != "" && networksJSON != "null" {
		if err := json.Unmarshal([]byte(networksJSON), &networks); err != nil {
			return nil, false, fmt.Errorf("unable to parse the networks of container %s: %w", containerID, err)
		}
	}

	names := make([]string, 0, len(networks))
	for name := range networks {
		names = append(names, name)
	}
	slices.Sort(names)

	return names, false, nil
}

// connectToNetwork attaches a container to a network it is not already on,
// under an alias when one is given
func connectToNetwork(ctx context.Context, network string, containerID string, alias string) error {
	attached, host, err := containerNetworks(ctx, containerID)
	if err != nil {
		return err
	}

	if host {
		return ErrHostNetworkContainer
	}

	if slices.Contains(attached, network) {
		return nil
	}

	args := []string{"network", "connect"}
	if alias != "" {
		args = append(args, "--alias", alias)
	}
	args = append(args, network, containerID)

	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    args,
	}); err != nil {
		return fmt.Errorf("unable to connect container %s to network %s: %w", containerID, network, err)
	}

	return nil
}

// EnsureLinkNetworkInput is the input for the EnsureLinkNetwork function
type EnsureLinkNetworkInput struct {
	// AppName is the app the link is for
	AppName string

	// ContainerID is the service container to attach, its live container when
	// empty
	ContainerID string

	// Datastore is the datastore the service belongs to
	Datastore *Datastore

	// Recreate is whether the network was made when the app was linked, so one
	// that is missing was removed since, which is what a failure says
	Recreate bool

	// ServiceName is the service the link is for
	ServiceName string
}

// EnsureLinkNetwork creates the network a link runs over when it is missing,
// and attaches the service container to it under the name the service url
// points at. A service with no container yet is attached once one is made.
// It is safe to repeat, and reports whether it made the network, so a caller
// undoing a failed link only removes a network it made.
func EnsureLinkNetwork(ctx context.Context, input EnsureLinkNetworkInput) (bool, error) {
	network := LinkNetworkName(input.Datastore, input.ServiceName, input.AppName)
	exists, err := linkNetworkExists(ctx, network)
	if err != nil {
		return false, err
	}

	created := false
	if !exists {
		result, err := execx.Run(ctx, common.ExecCommandInput{
			Command: common.DockerBin(),
			Args:    LinkNetworkCreateArgs(input.Datastore, input.ServiceName, input.AppName),
		})
		if err != nil {
			// running out of address pools is the one failure a host with many
			// links is likely to see, and nothing in docker's error says what
			// to do about it
			if strings.Contains(result.StderrContents(), addressPoolsExhaustedMessage) {
				return false, &addressPoolsError{
					network:     network,
					appName:     input.AppName,
					recreated:   input.Recreate,
					liveRestore: liveRestoreEnabled(ctx),
				}
			}

			return false, fmt.Errorf("%s: %w", networkFailurePrefix(input.Recreate, network, input.AppName), err)
		}
		created = true
	}

	containerID := input.ContainerID
	if containerID == "" {
		containerID = LiveContainerID(ctx, LiveContainerIDInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
		})
	}

	if containerID == "" {
		return created, nil
	}

	return created, connectToNetwork(ctx, network, containerID, DNSHostname(input.Datastore, input.ServiceName))
}

// AppContainerIDs returns the running containers of an app, whatever phase
// they were made for
func AppContainerIDs(ctx context.Context, appName string) ([]string, error) {
	result, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "ls", "--quiet", "--no-trunc", "--filter", fmt.Sprintf("label=%s=%s", AppNameContainerLabel, appName)},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to list the containers of app %s: %w", appName, err)
	}

	return strings.Fields(result.StdoutContents()), nil
}

// ConnectAppContainer attaches a container of an app to the network of its link
// to a service. A container on the host's network is refused with
// ErrHostNetworkContainer.
func ConnectAppContainer(ctx context.Context, s *Datastore, serviceName string, appName string, containerID string) error {
	return connectToNetwork(ctx, LinkNetworkName(s, serviceName, appName), containerID, "")
}

// RemoveLinkNetwork disconnects everything attached to a network a link runs
// over, the app's containers and the service's alike, and removes it. A network
// that is already gone is not an error.
func RemoveLinkNetwork(ctx context.Context, network string) error {
	exists, err := linkNetworkExists(ctx, network)
	if err != nil {
		return err
	}

	if !exists {
		return nil
	}

	result, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"network", "inspect", "--format", "{{ json .Containers }}", network},
	})
	if err != nil {
		return fmt.Errorf("unable to inspect network %s: %w", network, err)
	}

	containers := map[string]json.RawMessage{}
	if contents := strings.TrimSpace(result.StdoutContents()); contents != "" && contents != "null" {
		if err := json.Unmarshal([]byte(contents), &containers); err != nil {
			return fmt.Errorf("unable to parse the containers on network %s: %w", network, err)
		}
	}

	ids := make([]string, 0, len(containers))
	for id := range containers {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	for _, id := range ids {
		if _, err := execx.Run(ctx, common.ExecCommandInput{
			Command: common.DockerBin(),
			Args:    []string{"network", "disconnect", "--force", network, id},
		}); err != nil {
			return fmt.Errorf("unable to disconnect container %s from network %s: %w", id, network, err)
		}
	}

	if _, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"network", "rm", network},
	}); err != nil {
		return fmt.Errorf("unable to remove network %s: %w", network, err)
	}

	return nil
}

// ServiceLinkNetworks returns every link network a service has, including any
// left behind for an app that is no longer linked
func ServiceLinkNetworks(ctx context.Context, s *Datastore, serviceName string) ([]string, error) {
	result, err := execx.Run(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args: []string{
			"network", "ls", "--format", "{{ .Name }}",
			"--filter", "label=" + LinkNetworkLabel,
			"--filter", "label=" + LinkNetworkServiceTypeLabel + "=" + s.Properties().CommandPrefix,
			"--filter", "label=" + LinkNetworkServiceNameLabel + "=" + serviceName,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to list the link networks of service %s: %w", serviceName, err)
	}

	networks := strings.Fields(result.StdoutContents())
	slices.Sort(networks)

	return networks, nil
}
