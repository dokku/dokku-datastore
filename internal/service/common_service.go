package service

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// CommonService is the common service for all services
type CommonService struct{}

// ConfigOptions gets the config options for a service
func ConfigOptions(s Service, serviceName string) string {
	serviceRoot := Folders(s, serviceName).Root
	return common.ReadFirstLine(filepath.Join(serviceRoot, "CONFIG_OPTIONS"))
}

// ContainerID gets the container ID for a service
func ContainerID(s Service, serviceName string) string {
	serviceFiles := Files(s, serviceName)
	return common.ReadFirstLine(serviceFiles.ID)
}

// ContainerIP gets the container IP for a service
func ContainerIP(s Service, serviceName string) string {
	containerIP, _ := common.DockerInspect(ContainerID(s, serviceName), "{{ .NetworkSettings.IPAddress }}")
	return containerIP
}

// ContainerName gets the name of a service
func ContainerName(s Service, serviceName string) string {
	commandPrefix := s.Properties().CommandPrefix
	return fmt.Sprintf("dokku.%s.%s", commandPrefix, serviceName)
}

// DNSHostname gets the DNS hostname for a service
func DNSHostname(s Service, serviceName string) string {
	serviceName = ContainerName(s, serviceName)
	return strings.NewReplacer(".", "-", "_", "-").Replace(serviceName)
}

// ExposedPorts gets the exposed ports for a service
func ExposedPorts(s Service, serviceName string) string {
	serviceFiles := Files(s, serviceName)
	portFile := serviceFiles.Port

	if !common.FileExists(portFile) || common.ReadFirstLine(portFile) == "" {
		return "-"
	}

	datastorePorts := s.Properties().Ports
	ports := strings.Split(common.ReadFirstLine(portFile), ",")
	output := []string{}
	for i := range ports {
		output = append(output, fmt.Sprintf("%d->%s", datastorePorts[i], ports[i]))
	}

	return strings.Join(output, " ")
}

// ServiceFiles is the files for a service
type ServiceFiles struct {
	// ConfigOptions is the config options file for the service
	ConfigOptions string
	// DatabaseName is the database name file for the service
	DatabaseName string
	// Env is the environment file for the service
	Env string
	// ID is the ID file for the service
	ID string
	// Links is the links file for the service
	Links string
	// Image is the image file for the service
	Image string
	// ImageVersion is the image version file for the service
	ImageVersion string
	// Memory is the memory file for the service
	Memory string
	// Port is the port file for the service
	Port string
	// ShmSize is the shared memory size file for the service
	ShmSize string
}

// Files returns the files for a service
func Files(s Service, serviceName string) ServiceFiles {
	folders := Folders(s, serviceName)
	return ServiceFiles{
		ConfigOptions: filepath.Join(folders.Root, "CONFIG_OPTIONS"),
		DatabaseName:  filepath.Join(folders.Root, "DATABASE_NAME"),
		Env:           filepath.Join(folders.Root, "ENV"),
		ID:            filepath.Join(folders.Root, "ID"),
		Links:         filepath.Join(folders.Root, "LINKS"),
		Image:         filepath.Join(folders.Root, "IMAGE"),
		ImageVersion:  filepath.Join(folders.Root, "IMAGE_VERSION"),
		Memory:        filepath.Join(folders.Root, "MEMORY"),
		Port:          filepath.Join(folders.Root, "PORT"),
		ShmSize:       filepath.Join(folders.Root, "SHM_SIZE"),
	}
}

// ServiceFolders is the folders for a service
type ServiceFolders struct {
	// Root is the root folder for the service
	Root string
	// Config is the config folder for the service
	Config string
	// Data is the data folder for the service
	Data string
	// HostRoot is the host root folder for the service
	HostRoot string
	// HostConfig is the host config folder for the service
	HostConfig string
	// HostData is the host data folder for the service
	HostData string
}

// Folders returns the folders for a service
func Folders(s Service, serviceName string) ServiceFolders {
	serviceRoot := filepath.Join(DokkuLibRoot, "services", s.Properties().CommandPrefix, serviceName)
	return ServiceFolders{
		Root:       serviceRoot,
		Config:     filepath.Join(serviceRoot, "config"),
		Data:       filepath.Join(serviceRoot, "data"),
		HostRoot:   filepath.Join(DokkuLibHostRoot, "services", s.Properties().CommandPrefix, serviceName),
		HostConfig: filepath.Join(DokkuLibHostRoot, "services", s.Properties().CommandPrefix, serviceName, "config"),
		HostData:   filepath.Join(DokkuLibHostRoot, "services", s.Properties().CommandPrefix, serviceName, "data"),
	}
}

// Info returns the information about a service
func Info(s Service, serviceName string) map[string]string {
	serviceFolders := Folders(s, serviceName)

	return map[string]string{
		"config-dir":          serviceFolders.Config,
		"config-options":      ConfigOptions(s, serviceName),
		"data-dir":            serviceFolders.Data,
		"dsn":                 s.URL(serviceName),
		"exposed-ports":       ExposedPorts(s, serviceName),
		"id":                  ContainerID(s, serviceName),
		"internal-ip":         ContainerIP(s, serviceName),
		"initial-network":     InitialNetwork(s, serviceName),
		"links":               strings.Join(LinkedApps(s, serviceName), ","),
		"post-create-network": PostCreateNetwork(s, serviceName),
		"post-start-network":  PostStartNetwork(s, serviceName),
		"service-root":        serviceFolders.Root,
		"status":              Status(s, serviceName),
		"version":             Version(s, serviceName),
	}
}

// InitialNetwork gets the initial network for a service
func InitialNetwork(s Service, serviceName string) string {
	return common.PropertyGet(s.Properties().CommandPrefix, serviceName, "initial-network")
}

// LinkedApps returns the linked apps for a service
func LinkedApps(s Service, serviceName string) []string {
	linksFile := Files(s, serviceName).Links
	if !common.FileExists(linksFile) {
		return []string{}
	}

	lines, err := common.FileToSlice(linksFile)
	if err != nil {
		return []string{}
	}
	return lines
}

// PostCreateNetwork gets the post create network for a service
func PostCreateNetwork(s Service, serviceName string) string {
	return common.PropertyGet(s.Properties().CommandPrefix, serviceName, "post-create-network")
}

// PostStartNetwork gets the post start network for a service
func PostStartNetwork(s Service, serviceName string) string {
	return common.PropertyGet(s.Properties().CommandPrefix, serviceName, "post-start-network")
}

// Status gets the status of a service
func Status(s Service, serviceName string) string {
	containerStatus, _ := common.DockerInspect(ContainerID(s, serviceName), "{{ .State.Status }}")
	if containerStatus == "" {
		return "missing"
	}

	return containerStatus
}

// Version gets the version of a service
func Version(s Service, serviceName string) string {
	containerVersion, _ := common.DockerInspect(ContainerID(s, serviceName), "{{ .Config.Image }}")
	return containerVersion
}
