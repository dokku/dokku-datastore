package datastores

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// AmbassadorContainerName gets the name of the ambassador container for a service
func AmbassadorContainerName(s Datastore, serviceName string) string {
	commandPrefix := s.Properties().CommandPrefix
	return fmt.Sprintf("dokku.%s.%s.ambassador", commandPrefix, serviceName)
}

// ConfigOptions gets the config options for a service
func ConfigOptions(s Datastore, serviceName string) string {
	serviceRoot := Folders(s, serviceName).Root
	return common.ReadFirstLine(filepath.Join(serviceRoot, "CONFIG_OPTIONS"))
}

// ContainerExists checks to see if a container exists
func ContainerExists(ctx context.Context, containerID string) bool {
	result, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "inspect", containerID},
	})
	if err != nil {
		return false
	}
	return result.ExitCode == 0
}

// ContainerID gets the container ID for a service
func ContainerID(s Datastore, serviceName string) string {
	serviceFiles := Files(s, serviceName)
	return common.ReadFirstLine(serviceFiles.ID)
}

// ContainerIPInput is the input for the ContainerIP function
type ContainerIPInput struct {
	// ContainerID is the ID of the container to get the IP for
	ContainerID string

	// Datastore is the service to get the IP for
	Datastore Datastore

	// ServiceName is the name of the service to get the IP for
	ServiceName string
}

// ContainerIP gets the container IP for a service
func ContainerIP(ctx context.Context, input ContainerIPInput) string {
	if input.ContainerID == "" {
		input.ContainerID = LiveContainerID(ctx, LiveContainerIDInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
		})
	}

	containerIP, _ := common.DockerInspect(input.ContainerID, "{{ .NetworkSettings.IPAddress }}")
	return containerIP
}

// ContainerName gets the name of a service
func ContainerName(s Datastore, serviceName string) string {
	commandPrefix := s.Properties().CommandPrefix
	return fmt.Sprintf("dokku.%s.%s", commandPrefix, serviceName)
}

// DNSHostname gets the DNS hostname for a service
func DNSHostname(s Datastore, serviceName string) string {
	serviceName = ContainerName(s, serviceName)
	return strings.NewReplacer(".", "-", "_", "-").Replace(serviceName)
}

// EnterServiceContainerInput is the input for the EnterServiceContainer function
type EnterServiceContainerInput struct {
	// Datastore is the service to enter
	Datastore Datastore

	// ServiceName is the name of the service to enter
	ServiceName string
}

// EnterServiceContainer enters a service container
func EnterServiceContainer(ctx context.Context, input EnterServiceContainerInput) error {
	containerID := LiveContainerID(ctx, LiveContainerIDInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if containerID == "" {
		return fmt.Errorf("%s container %s does not exist", input.Datastore.Properties().CommandPrefix, input.ServiceName)
	}

	if !ContainerExists(ctx, containerID) {
		return fmt.Errorf("%s container %s does not exist", input.Datastore.Properties().CommandPrefix, input.ServiceName)
	}

	status := Status(ctx, StatusInput{ContainerID: containerID})
	if strings.ToLower(status) != "running" {
		return fmt.Errorf("%s container %s is not running", input.Datastore.Properties().CommandPrefix, input.ServiceName)
	}

	_, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command:      common.DockerBin(),
		Args:         []string{"container", "exec", "-it", containerID, "/bin/bash"},
		Stdin:        os.Stdin,
		StdoutWriter: os.Stdout,
		StderrWriter: os.Stderr,
	})
	if err != nil {
		return fmt.Errorf("failed to exec container: %w", err)
	}

	return nil
}

// Exists checks if a service exists
func Exists(ctx context.Context, s Datastore, serviceName string) bool {
	serviceFolders := Folders(s, serviceName)
	return common.DirectoryExists(serviceFolders.Root)
}

// ExposedPorts gets the exposed ports for a service
func ExposedPorts(s Datastore, serviceName string) string {
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

	// CronFile is the cron file for the service
	CronFile string

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
func Files(s Datastore, serviceName string) ServiceFiles {
	folders := Folders(s, serviceName)
	return ServiceFiles{
		ConfigOptions: filepath.Join(folders.Root, "CONFIG_OPTIONS"),
		CronFile:      fmt.Sprintf("/etc/cron.d/dokku-%s-%s", s.Properties().CommandPrefix, serviceName),
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
func Folders(s Datastore, serviceName string) ServiceFolders {
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

// InfoInput is the input for the Info function
type InfoInput struct {
	// Datastore is the service to get the information for
	Datastore Datastore

	// ServiceName is the name of the service to get the information for
	ServiceName string
}

// Info returns the information about a service
func Info(ctx context.Context, input InfoInput) map[string]string {
	serviceFolders := Folders(input.Datastore, input.ServiceName)

	containerID := LiveContainerID(ctx, LiveContainerIDInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	return map[string]string{
		"config-dir":          serviceFolders.Config,
		"config-options":      ConfigOptions(input.Datastore, input.ServiceName),
		"data-dir":            serviceFolders.Data,
		"dsn":                 input.Datastore.URL(input.ServiceName),
		"exposed-ports":       ExposedPorts(input.Datastore, input.ServiceName),
		"id":                  containerID,
		"internal-ip":         ContainerIP(ctx, ContainerIPInput{ContainerID: containerID}),
		"initial-network":     InitialNetwork(input.Datastore, input.ServiceName),
		"links":               strings.Join(LinkedApps(input.Datastore, input.ServiceName), ","),
		"post-create-network": PostCreateNetwork(input.Datastore, input.ServiceName),
		"post-start-network":  PostStartNetwork(input.Datastore, input.ServiceName),
		"service-root":        serviceFolders.Root,
		"status":              Status(ctx, StatusInput{ContainerID: containerID}),
		"version":             Version(ctx, VersionInput{ContainerID: containerID}),
	}
}

// InitialNetwork gets the initial network for a service
func InitialNetwork(s Datastore, serviceName string) string {
	return common.PropertyGet(s.Properties().CommandPrefix, serviceName, "initial-network")
}

// LinkedApps returns the linked apps for a service
func LinkedApps(s Datastore, serviceName string) []string {
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

// LiveContainerIDInput is the input for the LiveContainerID function
type LiveContainerIDInput struct {
	// Datastore is the service to get the live container ID for
	Datastore Datastore

	// ServiceName is the name of the service to get the live container ID for
	ServiceName string

	Filter string
}

// LiveContainerID gets the live container ID for a service, regardless of what is set in the ID file
func LiveContainerID(ctx context.Context, input LiveContainerIDInput) string {
	containerName := ContainerName(input.Datastore, input.ServiceName)
	arguments := []string{"container", "ps", "-aq", "--no-trunc", "--filter", fmt.Sprintf("name=^/%s$", containerName)}
	if input.Filter != "" {
		arguments = append(arguments, "--filter", input.Filter)
	}

	result, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    arguments,
	})
	if err != nil {
		return ""
	}

	id := result.StdoutContents()
	if id == "true" {
		return ""
	}

	return id
}

// PauseServiceContainerInput is the input for the PauseServiceContainer function
type PauseServiceContainerInput struct {
	// Datastore is the service to pause
	Datastore Datastore

	// ServiceName is the name of the service to pause
	ServiceName string

	// ContainerID is the ID of the container to pause
	ContainerID string
}

// PauseServiceContainer pauses a service container
func PauseServiceContainer(ctx context.Context, input PauseServiceContainerInput) error {
	if input.ContainerID == "" {
		input.ContainerID = LiveContainerID(ctx, LiveContainerIDInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
		})
	}

	_, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "stop", input.ContainerID},
	})
	if err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}

	ambassadorContainerName := AmbassadorContainerName(input.Datastore, input.ServiceName)
	if ContainerExists(ctx, ambassadorContainerName) {
		_, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
			Command: common.DockerBin(),
			Args:    []string{"container", "stop", ambassadorContainerName},
		})
		if err != nil {
			return fmt.Errorf("failed to stop ambassador container: %w", err)
		}
	}

	return nil
}

// PostCreateNetwork gets the post create network for a service
func PostCreateNetwork(s Datastore, serviceName string) string {
	return common.PropertyGet(s.Properties().CommandPrefix, serviceName, "post-create-network")
}

// PostStartNetwork gets the post start network for a service
func PostStartNetwork(s Datastore, serviceName string) string {
	return common.PropertyGet(s.Properties().CommandPrefix, serviceName, "post-start-network")
}

// RemoveBackupScheduleInput is the input for the RemoveBackupSchedule function
type RemoveBackupScheduleInput struct {
	// Datastore is the service to remove the backup schedule for
	Datastore Datastore

	// ServiceName is the name of the service to remove the backup schedule for
	ServiceName string
}

// RemoveBackupSchedule removes the backup schedule for a service
func RemoveBackupSchedule(ctx context.Context, input RemoveBackupScheduleInput) error {
	serviceFiles := Files(input.Datastore, input.ServiceName)
	if !common.FileExists(serviceFiles.CronFile) {
		return nil
	}

	// run with sudo
	_, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: "sudo",
		Args:    []string{"rm", "-f", serviceFiles.CronFile},
	})
	if err != nil {
		return fmt.Errorf("failed to remove cron file: %w", err)
	}

	return nil
}

// RemoveContainer removes a container
func RemoveContainer(ctx context.Context, containerID string) error {
	_, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "rm", "-f", containerID},
	})
	if err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	return nil
}

// RemoveServiceContainerInput is the input for the RemoveServiceContainer function
type RemoveServiceContainerInput struct {
	// Datastore is the service to remove the container for
	Datastore Datastore

	// ServiceName is the name of the service to remove the container for
	ServiceName string
}

// RemoveServiceContainer removes the service container for a service
func RemoveServiceContainer(ctx context.Context, input RemoveServiceContainerInput) error {
	containerID := LiveContainerID(ctx, LiveContainerIDInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if containerID == "" {
		return nil
	}

	if err := PauseServiceContainer(ctx, PauseServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		ContainerID: containerID,
	}); err != nil {
		return err
	}

	ambassadorContainerName := AmbassadorContainerName(input.Datastore, input.ServiceName)
	if ContainerExists(ctx, ambassadorContainerName) {
		if err := RemoveContainer(ctx, ambassadorContainerName); err != nil {
			return err
		}
	}

	_, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "update", "--restart=no", containerID},
	})
	if err != nil {
		return fmt.Errorf("failed to update container restart policy: %w", err)
	}

	if err := RemoveContainer(ctx, containerID); err != nil {
		return err
	}

	return nil
}

// StatusInput is the input for the Status function
type StatusInput struct {
	// ContainerID is the ID of the container to get the status for
	ContainerID string

	// Datastore is the service to get the status for
	Datastore Datastore

	// ServiceName is the name of the service to get the status for
	ServiceName string
}

// Status gets the status of a service
func Status(ctx context.Context, input StatusInput) string {
	if input.ContainerID == "" {
		input.ContainerID = LiveContainerID(ctx, LiveContainerIDInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
		})
	}

	containerStatus, _ := common.DockerInspect(input.ContainerID, "{{ .State.Status }}")
	if containerStatus == "" {
		return "missing"
	}

	return containerStatus
}

// StartInput is the input for the Start function
type StartInput struct {
	// Datastore is the service to start
	Datastore Datastore

	// ServiceName is the name of the service to start
	ServiceName string
}

// Start starts a service
func Start(ctx context.Context, input StartInput) error {
	runningContainerID := LiveContainerID(ctx, LiveContainerIDInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		Filter:      "status=running",
	})
	if runningContainerID != "" {
		return common.WriteStringToFile(common.WriteStringToFileInput{
			Content:   runningContainerID,
			Filename:  Files(input.Datastore, input.ServiceName).ID,
			GroupName: SystemGroup(),
			Mode:      0644,
			Username:  SystemUser(),
		})
	}

	previousContainerID := LiveContainerID(ctx, LiveContainerIDInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		Filter:      "status=exited",
	})
	if previousContainerID != "" {
		_, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
			Command: common.DockerBin(),
			Args:    []string{"container", "start", previousContainerID},
		})
		if err != nil {
			return fmt.Errorf("failed to start container: %w", err)
		}

		err = ServicePortReconcileStatus(ctx, ServicePortReconcileStatusInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile port status: %w", err)
		}

		return nil
	}

	taggedImage, err := ImageForService(ImageForServiceInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to get image for service: %w", err)
	}

	if err := ValidateTaggedImageExists(taggedImage); err != nil {
		return err
	}
	return input.Datastore.CreateServiceContainer(ctx, CreateServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		TaggedImage: taggedImage,
	})
}

// VersionInput is the input for the Version function
type VersionInput struct {
	// ContainerID is the ID of the container to get the version for
	ContainerID string

	// Datastore is the service to get the version for
	Datastore Datastore

	// ServiceName is the name of the service to get the version for
	ServiceName string
}

// Version gets the version of a service
func Version(ctx context.Context, input VersionInput) string {
	if input.ContainerID == "" {
		input.ContainerID = LiveContainerID(ctx, LiveContainerIDInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
		})
	}

	containerVersion, _ := common.DockerInspect(input.ContainerID, "{{ .Config.Image }}")
	return containerVersion
}
