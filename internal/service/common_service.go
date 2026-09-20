package service

import (
	"context"
	"fmt"
	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dokku/dokku-datastore/internal/backend"
	"github.com/dokku/dokku-datastore/internal/cron"

	"github.com/dokku/dokku/plugins/common"
)

// AmbassadorContainerName gets the name of the ambassador container for a service
func AmbassadorContainerName(s *Datastore, serviceName string) string {
	commandPrefix := s.Properties().CommandPrefix
	return fmt.Sprintf("dokku.%s.%s.ambassador", commandPrefix, serviceName)
}

// containerNames are the containers that make up a service, which is all the
// backend needs to know about it.
func containerNames(s *Datastore, serviceName string) backend.Names {
	return backend.Names{
		Container:  ContainerName(s, serviceName),
		Ambassador: AmbassadorContainerName(s, serviceName),
	}
}

// ConfigOptions gets the config options for a service
func ConfigOptions(s *Datastore, serviceName string) string {
	serviceRoot := Folders(s, serviceName).Root
	return common.ReadFirstLine(filepath.Join(serviceRoot, "CONFIG_OPTIONS"))
}

// ContainerExists checks to see if a container exists
func ContainerExists(ctx context.Context, containerID string) bool {
	return backend.Exists(ctx, containerID)
}

// ContainerID gets the container ID for a service
func ContainerID(s *Datastore, serviceName string) string {
	serviceFiles := Files(s, serviceName)
	return common.ReadFirstLine(serviceFiles.ID)
}

// ContainerIPInput is the input for the ContainerIP function
type ContainerIPInput struct {
	// ContainerID is the ID of the container to get the IP for
	ContainerID string

	// Datastore is the service to get the IP for
	Datastore *Datastore

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

	return backend.IP(ctx, input.ContainerID)
}

// ContainerName gets the name of a service
func ContainerName(s *Datastore, serviceName string) string {
	commandPrefix := s.Properties().CommandPrefix
	return fmt.Sprintf("dokku.%s.%s", commandPrefix, serviceName)
}

// DNSHostname gets the DNS hostname for a service
func DNSHostname(s *Datastore, serviceName string) string {
	serviceName = ContainerName(s, serviceName)
	return strings.NewReplacer(".", "-", "_", "-").Replace(serviceName)
}

// EnterServiceContainerInput is the input for the EnterServiceContainer function
type EnterServiceContainerInput struct {
	// Datastore is the service to enter
	Datastore *Datastore

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

	_, err := execx.Run(ctx, common.ExecCommandInput{
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
func Exists(ctx context.Context, s *Datastore, serviceName string) bool {
	serviceFolders := Folders(s, serviceName)
	return common.DirectoryExists(serviceFolders.Root)
}

// Password gets the password for a service, or an empty string when it has none
func Password(s *Datastore, serviceName string) string {
	return common.ReadFirstLine(Files(s, serviceName).Password)
}

// ExposedHostPorts gets the host ports a service is exposed on. The port file
// holds them whitespace delimited, in the same order as the datastore's own
// ports, which is the format the bash datastore plugins write.
func ExposedHostPorts(s *Datastore, serviceName string) []string {
	serviceFiles := Files(s, serviceName)
	portFile := serviceFiles.Port

	if !common.FileExists(portFile) {
		return []string{}
	}

	lines, err := common.FileToSlice(portFile)
	if err != nil {
		return []string{}
	}

	ports := []string{}
	for _, line := range lines {
		ports = append(ports, strings.Fields(line)...)
	}

	return ports
}

// ExposedPorts gets the exposed ports for a service
func ExposedPorts(s *Datastore, serviceName string) string {
	hostPorts := ExposedHostPorts(s, serviceName)
	if len(hostPorts) == 0 {
		return "-"
	}

	datastorePorts := s.Properties().Ports
	output := []string{}
	for i, hostPort := range hostPorts {
		if i >= len(datastorePorts) {
			break
		}

		output = append(output, fmt.Sprintf("%d->%s", datastorePorts[i], hostPort))
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

	// Definition is the file naming the definition a service runs. A datastore
	// split by major version has several, and which one a service was created
	// with decides where its data is mounted, so it is recorded rather than
	// worked out again each time from the image tag.
	Definition string

	// ImageVersion is the image version file for the service
	ImageVersion string

	// Memory is the memory file for the service
	Memory string

	// Password is the password file for the service
	Password string

	// Port is the port file for the service
	Port string

	// ShmSize is the shared memory size file for the service
	ShmSize string

	// Backend records which execution backend the service was created with, so
	// that changing the host default does not move a service that already exists
	Backend string

	// Compose is the rendered compose file describing the service
	Compose string
}

// Files returns the files for a service
func Files(s *Datastore, serviceName string) ServiceFiles {
	folders := Folders(s, serviceName)
	return ServiceFiles{
		ConfigOptions: filepath.Join(folders.Root, "CONFIG_OPTIONS"),
		CronFile:      fmt.Sprintf("/etc/cron.d/dokku-%s-%s", s.Properties().CommandPrefix, serviceName),
		DatabaseName:  filepath.Join(folders.Root, "DATABASE_NAME"),
		Env:           filepath.Join(folders.Root, "ENV"),
		ID:            filepath.Join(folders.Root, "ID"),
		Links:         filepath.Join(folders.Root, "LINKS"),
		Image:         filepath.Join(folders.Root, "IMAGE"),
		Definition:    filepath.Join(folders.Root, "DEFINITION"),
		ImageVersion:  filepath.Join(folders.Root, "IMAGE_VERSION"),
		Memory:        filepath.Join(folders.Root, "MEMORY"),
		Password:      filepath.Join(folders.Root, "PASSWORD"),
		Port:          filepath.Join(folders.Root, "PORT"),
		ShmSize:       filepath.Join(folders.Root, "SHM_SIZE"),
		Backend:       filepath.Join(folders.Root, "BACKEND"),
		Compose:       filepath.Join(folders.Root, "docker-compose.yml"),
	}
}

// ServiceFolders is the folders for a service
type ServiceFolders struct {
	// Root is the root folder for the service
	Root string

	// Config is the config folder for the service
	Config string

	// Backup is the folder holding the service's backup credentials
	Backup string

	// BackupEncryption is the folder holding the service's backup encryption settings
	BackupEncryption string

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
func Folders(s *Datastore, serviceName string) ServiceFolders {
	directory := s.Properties().DataDirectory
	serviceRoot := filepath.Join(DokkuLibRoot, "services", directory, serviceName)
	return ServiceFolders{
		Root:             serviceRoot,
		Backup:           filepath.Join(serviceRoot, "backup"),
		BackupEncryption: filepath.Join(serviceRoot, "backup-encryption"),
		Config:           filepath.Join(serviceRoot, "config"),
		Data:             filepath.Join(serviceRoot, "data"),
		HostRoot:         filepath.Join(DokkuLibHostRoot, "services", directory, serviceName),
		HostConfig:       filepath.Join(DokkuLibHostRoot, "services", directory, serviceName, "config"),
		HostData:         filepath.Join(DokkuLibHostRoot, "services", directory, serviceName, "data"),
	}
}

// InfoInput is the input for the Info function
type InfoInput struct {
	// Datastore is the service to get the information for
	Datastore *Datastore

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
		"dsn":                 input.Datastore.URL(input.ServiceName, ""),
		"exposed-ports":       ExposedPorts(input.Datastore, input.ServiceName),
		"id":                  containerID,
		"internal-ip":         ContainerIP(ctx, ContainerIPInput{ContainerID: containerID, Datastore: input.Datastore, ServiceName: input.ServiceName}),
		"initial-network":     InitialNetwork(input.Datastore, input.ServiceName),
		"links":               strings.Join(LinkedApps(ctx, LinkedAppsInput{Datastore: input.Datastore, ServiceName: input.ServiceName}), ","),
		"post-create-network": PostCreateNetwork(input.Datastore, input.ServiceName),
		"post-start-network":  PostStartNetwork(input.Datastore, input.ServiceName),
		"service-root":        serviceFolders.Root,
		"status":              Status(ctx, StatusInput{ContainerID: containerID, Datastore: input.Datastore, ServiceName: input.ServiceName}),
		"version":             Version(ctx, VersionInput{ContainerID: containerID, Datastore: input.Datastore, ServiceName: input.ServiceName}),
	}
}

// InitialNetwork gets the initial network for a service
func InitialNetwork(s *Datastore, serviceName string) string {
	return common.PropertyGet(s.Properties().CommandPrefix, serviceName, "initial-network")
}

// LinkedAppsInput is the input for the LinkedApps function
type LinkedAppsInput struct {
	// Datastore is the service to get the linked apps for
	Datastore *Datastore

	// ServiceName is the name of the service to get the linked apps for
	ServiceName string
}

// LinkedApps returns the linked apps for a service
func LinkedApps(ctx context.Context, input LinkedAppsInput) []string {
	linksFile := Files(input.Datastore, input.ServiceName).Links
	if !common.FileExists(linksFile) {
		return []string{}
	}

	lines, err := common.FileToSlice(linksFile)
	if err != nil {
		return []string{}
	}
	if len(lines) == 0 {
		return []string{}
	}
	return lines
}

// AppExists reports whether an app is still on disk.
//
// Deliberately a directory check rather than common.VerifyAppName, which also
// applies the user-auth filtering: an app the current user cannot see is still
// an app, and treating it as gone would let one user destroy a datastore that
// another user's app is using.
func AppExists(appName string) bool {
	return common.DirectoryExists(common.AppRoot(appName))
}

// LiveLinkedApps returns the linked apps that still exist.
//
// An app deleted while this plugin could not see it - because the plugin was
// disabled, or because the app was removed outside dokku - leaves its name in
// the links file. Counting that as a live link makes the service impossible to
// destroy, since unlink refuses to act on an app that is not there and destroy
// refuses while the file names one.
func LiveLinkedApps(ctx context.Context, input LinkedAppsInput) []string {
	linkedApps := LinkedApps(ctx, input)
	live := make([]string, 0, len(linkedApps))
	for _, appName := range linkedApps {
		if AppExists(appName) {
			live = append(live, appName)
		}
	}

	return live
}

// writeLinkedApps writes the links file for a service, deduplicated and sorted,
// matching what the bash datastore plugins produce
func writeLinkedApps(s *Datastore, serviceName string, apps []string) error {
	slices.Sort(apps)
	apps = slices.Compact(apps)

	return common.WriteSliceToFile(common.WriteSliceToFileInput{
		Filename:  Files(s, serviceName).Links,
		GroupName: hostenv.SystemGroup(),
		Lines:     apps,
		Mode:      0644,
		Username:  hostenv.SystemUser(),
	})
}

// AddLinkedApp records an app as linked to a service
func AddLinkedApp(ctx context.Context, input LinkedAppsInput, appName string) error {
	apps := LinkedApps(ctx, input)
	if slices.Contains(apps, appName) {
		return nil
	}

	return writeLinkedApps(input.Datastore, input.ServiceName, append(apps, appName))
}

// RemoveLinkedApp drops an app from the set of apps linked to a service
func RemoveLinkedApp(ctx context.Context, input LinkedAppsInput, appName string) error {
	linksFile := Files(input.Datastore, input.ServiceName).Links
	if !common.FileExists(linksFile) {
		return nil
	}

	apps := LinkedApps(ctx, input)
	remaining := []string{}
	for _, app := range apps {
		if app != appName {
			remaining = append(remaining, app)
		}
	}

	return writeLinkedApps(input.Datastore, input.ServiceName, remaining)
}

// LiveContainerIDInput is the input for the LiveContainerID function
type LiveContainerIDInput struct {
	// Datastore is the service to get the live container ID for
	Datastore *Datastore

	// ServiceName is the name of the service to get the live container ID for
	ServiceName string

	Filter string
}

// LiveContainerID gets the live container ID for a service, regardless of what is set in the ID file
//
// A lookup with no datastore to name a container with finds nothing, which is
// the same answer a stopped service gives. It is reported that way rather than
// crashing, because the callers that reach here without one are asking about a
// service whose container is already gone.
func LiveContainerID(ctx context.Context, input LiveContainerIDInput) string {
	if input.Datastore == nil || input.ServiceName == "" {
		return ""
	}

	return backend.LiveContainerID(ctx, backend.LiveContainerIDInput{
		ContainerName: ContainerName(input.Datastore, input.ServiceName),
		Filter:        input.Filter,
	})
}

// PauseServiceContainerInput is the input for the PauseServiceContainer function
type PauseServiceContainerInput struct {
	// Datastore is the service to pause
	Datastore *Datastore

	// ServiceName is the name of the service to pause
	ServiceName string

	// ContainerID is the ID of the container to pause
	ContainerID string
}

// PauseServiceContainer pauses a service container
func PauseServiceContainer(ctx context.Context, input PauseServiceContainerInput) error {
	return backend.Pause(ctx, backend.PauseInput{
		Names:       containerNames(input.Datastore, input.ServiceName),
		ContainerID: input.ContainerID,
	})
}

// PostCreateNetwork gets the post create network for a service
func PostCreateNetwork(s *Datastore, serviceName string) string {
	return common.PropertyGet(s.Properties().CommandPrefix, serviceName, "post-create-network")
}

// PostStartNetwork gets the post start network for a service
func PostStartNetwork(s *Datastore, serviceName string) string {
	return common.PropertyGet(s.Properties().CommandPrefix, serviceName, "post-start-network")
}

// RemoveBackupScheduleInput is the input for the RemoveBackupSchedule function
type RemoveBackupScheduleInput struct {
	// Datastore is the service to remove the backup schedule for
	Datastore *Datastore

	// ServiceName is the name of the service to remove the backup schedule for
	ServiceName string
}

// RemoveBackupSchedule removes the backup schedule for a service
func RemoveBackupSchedule(ctx context.Context, input RemoveBackupScheduleInput) error {
	serviceFiles := Files(input.Datastore, input.ServiceName)
	if !common.FileExists(serviceFiles.CronFile) {
		return nil
	}

	// the cron directory belongs to root, so the removal goes through the helper
	// the plugin installs and the dokku group is granted
	return cron.Remove(ctx, input.Datastore.Properties().CommandPrefix, input.ServiceName)
}

// RemoveContainer removes a container
func RemoveContainer(ctx context.Context, containerID string) error {
	return backend.Remove(ctx, containerID)
}

// RemoveServiceContainerInput is the input for the RemoveServiceContainer function
type RemoveServiceContainerInput struct {
	// Datastore is the service to remove the container for
	Datastore *Datastore

	// ServiceName is the name of the service to remove the container for
	ServiceName string
}

// RemoveServiceContainer removes the service container for a service
func RemoveServiceContainer(ctx context.Context, input RemoveServiceContainerInput) error {
	return backend.Down(ctx, containerNames(input.Datastore, input.ServiceName))
}

// StatusInput is the input for the Status function
type StatusInput struct {
	// ContainerID is the ID of the container to get the status for
	ContainerID string

	// Datastore is the service to get the status for
	Datastore *Datastore

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

	return backend.Status(ctx, input.ContainerID)
}

// StartInput is the input for the Start function
type StartInput struct {
	// Datastore is the service to start
	Datastore *Datastore

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
			GroupName: hostenv.SystemGroup(),
			Mode:      0644,
			Username:  hostenv.SystemUser(),
		})
	}

	previousContainerID := LiveContainerID(ctx, LiveContainerIDInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		Filter:      "status=exited",
	})
	if previousContainerID != "" {
		_, err := execx.Run(ctx, common.ExecCommandInput{
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
	Datastore *Datastore

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

	running := backend.Image(ctx, input.ContainerID)

	// a definition that bakes tooling into the image runs a tag dokku built,
	// which is an implementation detail: asked for a version, an operator wants
	// to know which redis is running, not which wrapper was built around it
	return input.Datastore.PinnedImage(input.ServiceName, running)
}
