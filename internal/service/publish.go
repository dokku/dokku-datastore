package service

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/dokku/dokku-datastore/internal/backend"
	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/render"
	"github.com/dokku/dokku/plugins/common"
)

// DirectPublishSpecs are the docker --publish specs a service's container
// publishes the given host ports with, empty unless the service is exposed
// directly.
//
// The host ports are handed in rather than read from the port file, so that a
// command can work out what a container would publish before it writes
// anything.
func DirectPublishSpecs(s *Datastore, serviceName string, hostPorts []string) []string {
	properties := s.Properties()
	return directPublishSpecs(directPublishSpecsInput{
		Mode:        ServiceExposeMode(s, serviceName),
		BindAddress: ServicePortBindAddress(s, serviceName),
		HostPorts:   hostPorts,
		Ports:       properties.Ports,
		Protocols:   properties.Protocols,
	})
}

// directPublishSpecsInput is the input for directPublishSpecs
type directPublishSpecsInput struct {
	// Mode is the service's expose mode
	Mode string

	// BindAddress is the service's port-bind-address, empty for every
	// interface
	BindAddress string

	// HostPorts are the host ports each container port is published on, in the
	// same order. Each is a port, or an address and port that publishes it on
	// that address alone
	HostPorts []string

	// Ports are the ports the service listens on
	Ports []int

	// Protocols are the protocol each of Ports speaks, in the same order
	Protocols []string
}

// directPublishSpecs builds the docker --publish specs for a service exposed
// directly.
//
// A host port with no address of its own is published on the
// port-bind-address, as the ambassador publishes it, and a udp port is
// published over udp.
func directPublishSpecs(input directPublishSpecsInput) []string {
	if input.Mode != ExposeModeDirect || len(input.HostPorts) == 0 {
		return nil
	}

	specs := make([]string, 0, len(input.HostPorts))
	for i, hostPort := range input.HostPorts {
		if i >= len(input.Ports) {
			break
		}

		if input.BindAddress != "" && !strings.Contains(hostPort, ":") {
			hostPort = net.JoinHostPort(input.BindAddress, hostPort)
		}

		spec := fmt.Sprintf("%s:%d", hostPort, input.Ports[i])
		if i < len(input.Protocols) && input.Protocols[i] == definition.ProtocolUDP {
			spec += "/udp"
		}

		specs = append(specs, spec)
	}

	return specs
}

// MinimumExposedPorts is the fewest host ports a service can be exposed on:
// enough to reach its primary port, which is the one its dsn names.
//
// A definition adds a port at the end of its list, so a service exposed before
// that holds a host port for each port it had then. Those stay where they were
// published and the ports after them are left unexposed, rather than a service
// that was reachable losing every port because the definition grew.
func MinimumExposedPorts(s *Datastore) int {
	primary, ok := s.Definition.PrimaryPort()
	if !ok {
		return len(s.Definition.Service.Ports)
	}

	for i, port := range s.Definition.Service.Ports {
		if port.Name == primary.Name {
			return i + 1
		}
	}

	return len(s.Definition.Service.Ports)
}

// CheckExposedPortCount reports whether a service can be exposed on this many
// host ports: at least enough to reach its primary port, and no more than it
// has ports to publish them on.
func CheckExposedPortCount(s *Datastore, count int) error {
	maximum := len(s.Definition.Service.Ports)
	minimum := MinimumExposedPorts(s)
	if count >= minimum && count <= maximum {
		return nil
	}

	if minimum == maximum {
		return fmt.Errorf("expected %d ports", maximum)
	}

	return fmt.Errorf("expected between %d and %d ports", minimum, maximum)
}

// ContainerPublishedPorts are the --publish specs a service container was made
// with, empty for one that publishes nothing itself.
func ContainerPublishedPorts(containerID string) []string {
	if containerID == "" {
		return nil
	}

	value, _ := common.DockerInspect(containerID, fmt.Sprintf("{{ index .Config.Labels %q }}", render.PublishedPortsLabel))
	return splitPublishedPorts(value)
}

// splitPublishedPorts reads the published ports label back into its specs
func splitPublishedPorts(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	return strings.Split(value, ",")
}

// publishedPortsDiffer reports whether a container publishing one set of specs
// has to be made again to publish another. Nothing and an empty set are the
// same thing.
func publishedPortsDiffer(current []string, wanted []string) bool {
	if len(current) == 0 && len(wanted) == 0 {
		return false
	}

	return !slices.Equal(current, wanted)
}

// PublishState is what is known about the ports a service's container
// publishes itself, against what it would publish with a set of host ports.
type PublishState struct {
	// ContainerID is the service's container, empty when it has none
	ContainerID string

	// Running is whether that container is up, or frozen, which a start thaws
	// rather than replaces. Recreating either one takes the service down
	Running bool

	// Differs is whether the container has to be made again to publish what
	// it should
	Differs bool
}

// ServicePublishState compares what a service's container publishes itself
// with what it would publish given a set of host ports and the service's
// current expose mode.
//
// A service with no container has nothing to compare, since the container it
// is next made with publishes what it should.
func ServicePublishState(ctx context.Context, s *Datastore, serviceName string, hostPorts []string) PublishState {
	containerID := LiveContainerID(ctx, LiveContainerIDInput{
		Datastore:   s,
		ServiceName: serviceName,
	})
	if containerID == "" {
		return PublishState{}
	}

	status := backend.Status(ctx, containerID)
	return PublishState{
		ContainerID: containerID,
		Running:     status == "running" || status == "paused",
		Differs:     publishedPortsDiffer(ContainerPublishedPorts(containerID), DirectPublishSpecs(s, serviceName, hostPorts)),
	}
}
