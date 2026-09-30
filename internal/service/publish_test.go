package service

import (
	"reflect"
	"testing"

	"github.com/dokku/dokku/plugins/common"
)

func TestDirectPublishSpecs(t *testing.T) {
	tests := []struct {
		name     string
		input    directPublishSpecsInput
		expected []string
	}{
		{
			name:     "on every interface",
			input:    directPublishSpecsInput{Mode: ExposeModeDirect, HostPorts: []string{"1234"}, Ports: []int{6379}},
			expected: []string{"1234:6379"},
		},
		{
			name:     "on the port-bind-address",
			input:    directPublishSpecsInput{Mode: ExposeModeDirect, BindAddress: "127.0.0.1", HostPorts: []string{"1234"}, Ports: []int{6379}},
			expected: []string{"127.0.0.1:1234:6379"},
		},
		{
			// docker reads the brackets, which is what tells an address from a
			// port in a spec with this many colons
			name:     "on an IPv6 port-bind-address",
			input:    directPublishSpecsInput{Mode: ExposeModeDirect, BindAddress: "::1", HostPorts: []string{"1234"}, Ports: []int{6379}},
			expected: []string{"[::1]:1234:6379"},
		},
		{
			name:     "a port with an address of its own wins over the port-bind-address",
			input:    directPublishSpecsInput{Mode: ExposeModeDirect, BindAddress: "127.0.0.1", HostPorts: []string{"10.0.0.5:1234", "[::1]:1235"}, Ports: []int{6379, 6380}},
			expected: []string{"10.0.0.5:1234:6379", "[::1]:1235:6380"},
		},
		{
			name:     "a udp port is published over udp",
			input:    directPublishSpecsInput{Mode: ExposeModeDirect, HostPorts: []string{"1234", "1235"}, Ports: []int{80, 8125}, Protocols: []string{"tcp", "udp"}},
			expected: []string{"1234:80", "1235:8125/udp"},
		},
		{
			name:  "through an ambassador",
			input: directPublishSpecsInput{Mode: ExposeModeAmbassador, HostPorts: []string{"1234"}, Ports: []int{6379}},
		},
		{
			name:  "not exposed",
			input: directPublishSpecsInput{Mode: ExposeModeDirect, Ports: []int{6379}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := directPublishSpecs(test.input); !reflect.DeepEqual(actual, test.expected) {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

// the mode and port-bind-address are the service's own, and the host ports are
// the ones asked about rather than the port file's
func TestDirectPublishSpecsReadsTheService(t *testing.T) {
	redis := redisDatastore(t)
	withServiceRoot(t, redis, "lollipop")
	t.Setenv("DOKKU_LIB_ROOT", DokkuLibRoot)

	if specs := DirectPublishSpecs(redis, "lollipop", []string{"1234"}); len(specs) != 0 {
		t.Errorf("expected nothing published by a service using the ambassador, got %v", specs)
	}

	commandPrefix := redis.Properties().CommandPrefix
	if err := common.PropertyWrite(commandPrefix, "lollipop", ExposeModeProperty, ExposeModeDirect); err != nil {
		t.Fatalf("failed to write the property: %v", err)
	}
	if err := common.PropertyWrite(commandPrefix, "lollipop", PortBindAddressProperty, "127.0.0.1"); err != nil {
		t.Fatalf("failed to write the property: %v", err)
	}

	expected := []string{"127.0.0.1:1234:6379"}
	if specs := DirectPublishSpecs(redis, "lollipop", []string{"1234"}); !reflect.DeepEqual(specs, expected) {
		t.Errorf("expected %v, got %v", expected, specs)
	}
}

func TestPublishedPortsDiffer(t *testing.T) {
	tests := []struct {
		name     string
		current  []string
		wanted   []string
		expected bool
	}{
		{name: "nothing either way", expected: false},
		{name: "nothing and an empty set", current: nil, wanted: []string{}, expected: false},
		{name: "the same ports", current: []string{"1234:6379"}, wanted: []string{"1234:6379"}, expected: false},
		{name: "a different host port", current: []string{"1234:6379"}, wanted: []string{"1235:6379"}, expected: true},
		{name: "a new port-bind-address", current: []string{"1234:6379"}, wanted: []string{"127.0.0.1:1234:6379"}, expected: true},
		{name: "moved to being published directly", current: nil, wanted: []string{"1234:6379"}, expected: true},
		{name: "moved back to an ambassador", current: []string{"1234:6379"}, wanted: nil, expected: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := publishedPortsDiffer(test.current, test.wanted); actual != test.expected {
				t.Errorf("expected %t, got %t", test.expected, actual)
			}
		})
	}
}

func TestSplitPublishedPorts(t *testing.T) {
	if actual := splitPublishedPorts(""); actual != nil {
		t.Errorf("expected nothing from an unset label, got %v", actual)
	}

	expected := []string{"1234:80", "1235:8125/udp"}
	if actual := splitPublishedPorts("1234:80,1235:8125/udp\n"); !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}
