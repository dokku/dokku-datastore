package service

import (
	"io"

	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/registry"
)

// ServiceStruct is the structure for a service
type ServiceStruct struct {
	// AltAlias is the prefix used to generate an alternate config url alias when
	// the default alias is already in use
	AltAlias string

	// CommandPrefix is the command prefix for a service
	CommandPrefix string

	// DefaultAlias is the prefix of the config variable a linked app receives the
	// service url on
	DefaultAlias string

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

	// PluginVariable is the plugin variable for a service
	PluginVariable string

	// Ports is the ports for a service
	Ports []int

	// Scheme is the scheme for a service
	Scheme string

	// WaitPort is the port to wait for a service to be ready
	WaitPort int
}

// CreateServiceContainerInput is the input for the CreateServiceContainer function
type CreateServiceContainerInput struct {
	// Datastore is the service to create the container for
	Datastore *Datastore

	// ServiceName is the name of the service to create the container for
	ServiceName string

	// TaggedImage is the tagged image to use for the container
	TaggedImage string
}

// ConnectToServiceInput is the input for the ConnectToService function
type ConnectToServiceInput struct {
	// Datastore is the service to connect to
	Datastore *Datastore

	// ServiceName is the name of the service to connect to
	ServiceName string
}

// ExportServiceInput is the input for the ExportService function
type ExportServiceInput struct {
	// Datastore is the service to export
	Datastore *Datastore

	// ServiceName is the name of the service to export
	ServiceName string

	// Writer receives the exported data
	Writer io.Writer
}

// ImportServiceInput is the input for the ImportService function
type ImportServiceInput struct {
	// Datastore is the service to import into
	Datastore *Datastore

	// Reader supplies the data to import
	Reader io.Reader

	// ServiceName is the name of the service to import into
	ServiceName string
}

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

// Datastores is the map of datastores
var Datastores = map[string]*Datastore{}

// init initializes the services
func init() {
	DokkuLibRoot = hostenv.LibRoot()
	DokkuLibHostRoot = hostenv.LibHostRoot()
	PluginPath = hostenv.PluginPath()
	PluginDataRoot = hostenv.DataRoot()

	loaded, err := registry.Load(registry.LoadInput{PluginDir: hostenv.PluginCheckout()})
	if err != nil {
		// a definition that does not parse is a datastore that cannot be
		// operated, and continuing would report it as an unsupported type
		// rather than as the broken definition it is
		panic(err)
	}

	for _, name := range loaded.Plugins() {
		found, err := loaded.For(name, "")
		if err != nil {
			panic(err)
		}

		Datastores[name] = &Datastore{Definition: found}
	}
}
