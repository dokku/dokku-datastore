package service

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// ExposeAddressProperty is the address an exposed service's ports are
// published on when the port file gives them none of their own. Empty
// publishes them on every interface, which is what every exposed service did
// before there was anything to say here.
//
// Read when the ambassador is made, so a change reaches the running service
// on the next reexpose, or whenever the ambassador is next replaced.
const ExposeAddressProperty = "expose-address"

// ExposeSourceRangeProperty is the only range of client addresses an exposed
// service accepts connections from. Empty accepts every client.
//
// Read when the ambassador is made, like the expose address.
const ExposeSourceRangeProperty = "expose-source-range"

// ValidateExposeAddress reports whether a value is an address an exposed
// service can be published on: an IPv4 or IPv6 address, written without the
// brackets a port gives an IPv6 one. An empty value is valid and means every
// interface.
//
// A hostname such as localhost is refused, as it is in a port: docker only
// publishes on an address.
func ValidateExposeAddress(value string) error {
	if value == "" {
		return nil
	}

	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" {
		return fmt.Errorf("invalid %s value %q, must be an IPv4 or IPv6 address", ExposeAddressProperty, value)
	}

	return nil
}

// ValidateExposeSourceRange reports whether a value is a range of client
// addresses: one IP address or CIDR. An empty value is valid and means every
// client.
//
// Only one range is taken because the ambassador enforces it with socat, which
// honors a single range for each port it listens on.
func ValidateExposeSourceRange(value string) error {
	if value == "" {
		return nil
	}

	invalid := fmt.Errorf("invalid %s value %q, must be a single IP address or CIDR", ExposeSourceRangeProperty, value)
	if strings.Contains(value, "/") {
		if _, err := netip.ParsePrefix(value); err != nil {
			return invalid
		}

		return nil
	}

	if _, err := netip.ParseAddr(value); err != nil {
		return invalid
	}

	return nil
}

// ServiceExposeAddress is the address a service's ports are published on when
// they have none of their own, empty for every interface.
func ServiceExposeAddress(s *Datastore, serviceName string) string {
	return strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, ExposeAddressProperty))
}

// ServiceExposeSourceRange is the only range of client addresses a service
// accepts connections from, empty for every client.
func ServiceExposeSourceRange(s *Datastore, serviceName string) string {
	return strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, ExposeSourceRangeProperty))
}
