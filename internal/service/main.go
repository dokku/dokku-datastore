package service

import (
	"context"
	"os"
	"path/filepath"
)

// ServiceStruct is the structure for a service
type ServiceStruct struct {
	// CommandPrefix is the command prefix for a service
	CommandPrefix string
	// ConfigVariable is the configuration variable for a service
	ConfigVariable string
	// ConfigSuffix is the suffix for the configuration directory
	ConfigSuffix string
	// DefaultImage is the default image for a service
	DefaultImage string
	// DefaultImageVersion is the default image version for a service
	DefaultImageVersion string
	// EnvVariable is the environment variable for a service
	EnvVariable string
	// ImagePullVariable is the image pull variable for a service
	ImagePullVariable string
	// Ports is the ports for a service
	Ports []int
	// WaitPort is the port to wait for a service to be ready
	WaitPort int
}

// Service is the interface for a service
type Service interface {
	// CreateService creates a new service
	CreateService(ctx context.Context, serviceName string) error
	// CreateServiceContainer creates a new service container
	CreateServiceContainer(ctx context.Context, serviceName string) error
	// Properties returns the properties of a service
	Properties() ServiceStruct
	// URL returns the url for a service
	URL(serviceName string) string
}

// Services is the map of services
var Services = map[string]Service{}

var (
	// PluginDataRoot is the root of the plugin data
	PluginDataRoot string
	// PluginPath is the path to the plugin
	PluginPath string
	// DokkuLibRoot is the root of the dokku library
	DokkuLibRoot string
	// DokkuLibHostRoot is the root of the dokku library host
	DokkuLibHostRoot string
)

// PluginAmbassadorImage is the ambassador image
var PluginAmbassadorImage = "dokku/ambassador:0.8.2"

// PluginBusyboxImage is the busybox image
var PluginBusyboxImage = "busybox:1.37.0-uclibc"

// PluginWaitImage is the wait image
var PluginWaitImage = "dokku/wait:0.9.3"

// init initializes the services
func init() {
	DokkuLibRoot = os.Getenv("DOKKU_LIB_ROOT")
	if DokkuLibRoot == "" {
		DokkuLibRoot = "/var/lib/dokku"
	}

	DokkuLibHostRoot = os.Getenv("DOKKU_LIB_HOST_ROOT")
	if DokkuLibHostRoot == "" {
		DokkuLibHostRoot = DokkuLibRoot
	}

	PluginPath = filepath.Join(DokkuLibRoot, "plugins")
	PluginDataRoot = filepath.Join(DokkuLibRoot, "services")

	Services["redis"] = &RedisService{}
}
