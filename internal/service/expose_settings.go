package service

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// PortBindAddressProperty is the address an exposed service's ports are
// published on when the port file gives them none of their own. Empty
// publishes them on every interface, which is what every exposed service did
// before there was anything to say here.
//
// Read when the ambassador is made, so a change reaches the running service
// on the next reexpose, or whenever the ambassador is next replaced.
const PortBindAddressProperty = "port-bind-address"

// PortSourceRangeProperty is the only range of client addresses an exposed
// service accepts connections from. Empty accepts every client.
//
// Read when the ambassador is made, like the port bind address.
const PortSourceRangeProperty = "port-source-range"

// ExposeHostProperty is the host the exposed dsn names, for clients that reach
// the server by a name or address other than its global domain. Empty names
// the first global domain.
//
// It only changes what is reported. Where the ports are bound is the
// port-bind-address, which is never used as the host: the address a port is
// bound on is often one a client elsewhere cannot reach it at.
const ExposeHostProperty = "expose-host"

// ValidatePortBindAddress reports whether a value is an address an exposed
// service can be published on: an IPv4 or IPv6 address, written without the
// brackets a port gives an IPv6 one. An empty value is valid and means every
// interface.
//
// A hostname such as localhost is refused, as it is in a port: docker only
// publishes on an address.
func ValidatePortBindAddress(value string) error {
	if value == "" {
		return nil
	}

	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" {
		return fmt.Errorf("invalid %s value %q, must be an IPv4 or IPv6 address", PortBindAddressProperty, value)
	}

	return nil
}

// ValidateExposeHost reports whether a value is a host the exposed dsn can
// name: an IPv4 or IPv6 address, written without the brackets a port gives an
// IPv6 one, or a hostname. An empty value is valid and means the global domain.
//
// A port, a scheme or a path is refused, since the value is put into the dsn
// as its host and the port comes from the exposed ports.
func ValidateExposeHost(value string) error {
	if value == "" {
		return nil
	}

	if address, err := netip.ParseAddr(value); err == nil {
		if address.Zone() != "" {
			return fmt.Errorf("invalid %s value %q, an IPv6 address must not have a zone", ExposeHostProperty, value)
		}

		return nil
	}

	if !validHostname(value) {
		return fmt.Errorf("invalid %s value %q, must be a hostname or an IPv4 or IPv6 address", ExposeHostProperty, value)
	}

	return nil
}

// validHostname reports whether a value is a dns hostname: dot separated
// labels of letters, digits and hyphens, none starting or ending with a hyphen
func validHostname(value string) bool {
	if len(value) > 253 {
		return false
	}

	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}

		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}

		for _, character := range label {
			isLetter := (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
			isDigit := character >= '0' && character <= '9'
			if !isLetter && !isDigit && character != '-' {
				return false
			}
		}
	}

	return true
}

// ValidatePortSourceRange reports whether a value is a range of client
// addresses: one IP address or CIDR. An empty value is valid and means every
// client.
//
// Only one range is taken because the ambassador enforces it with socat, which
// honors a single range for each port it listens on.
func ValidatePortSourceRange(value string) error {
	if value == "" {
		return nil
	}

	invalid := fmt.Errorf("invalid %s value %q, must be a single IP address or CIDR", PortSourceRangeProperty, value)
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

// ServicePortBindAddress is the address a service's ports are published on when
// they have none of their own, empty for every interface.
func ServicePortBindAddress(s *Datastore, serviceName string) string {
	return strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, PortBindAddressProperty))
}

// ServicePortSourceRange is the only range of client addresses a service
// accepts connections from, empty for every client.
func ServicePortSourceRange(s *Datastore, serviceName string) string {
	return strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, PortSourceRangeProperty))
}

// ServiceExposeHost is the host the exposed dsn names, as it was set, empty
// when it was not
func ServiceExposeHost(s *Datastore, serviceName string) string {
	return strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, ExposeHostProperty))
}

// ExposeModeProperty is how an exposed service's ports are published: through
// an ambassador, a helper container that forwards each port on to the service,
// or directly by the service container itself. Empty is the ambassador, which
// is how every exposed service was published before there was a choice.
//
// Read when the service container is made, since a container cannot change
// the ports it publishes, and when the ambassador is reconciled.
const ExposeModeProperty = "expose-mode"

// ExposeModeAmbassador publishes an exposed service's ports through an
// ambassador, which can limit its clients to a port-source-range and be
// replaced without touching the service.
const ExposeModeAmbassador = "ambassador"

// ExposeModeDirect publishes an exposed service's ports on the service
// container itself. Nothing sits between a client and the service, but the
// container has to be made again for what it publishes to change, and docker
// has no way to limit its clients to a port-source-range.
const ExposeModeDirect = "direct"

// ValidateExposeMode reports whether a value is an expose mode. An empty value
// is valid and means the ambassador.
func ValidateExposeMode(value string) error {
	switch value {
	case "", ExposeModeAmbassador, ExposeModeDirect:
		return nil
	}

	return fmt.Errorf("invalid %s value %q, must be %s or %s", ExposeModeProperty, value, ExposeModeAmbassador, ExposeModeDirect)
}

// CheckExposeModeSourceRange reports whether an expose mode and a
// port-source-range can be used together. A service published directly has no
// ambassador to enforce the range, and docker cannot, so a range would be
// silently ignored.
func CheckExposeModeSourceRange(mode string, sourceRange string) error {
	if mode == ExposeModeDirect && sourceRange != "" {
		return fmt.Errorf("a %s cannot be enforced when the %s is %s, unset one of them", PortSourceRangeProperty, ExposeModeProperty, ExposeModeDirect)
	}

	return nil
}

// ServiceExposeMode is how a service's ports are published when it is
// exposed, the ambassador when it was not set.
func ServiceExposeMode(s *Datastore, serviceName string) string {
	mode := strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, ExposeModeProperty))
	if mode == "" {
		return ExposeModeAmbassador
	}

	return mode
}
