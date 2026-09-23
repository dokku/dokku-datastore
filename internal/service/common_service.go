package service

import (
	"context"
	"fmt"
	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"io"
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

	// Warnings is where a record that could not be written is reported. Nil
	// means stderr.
	Warnings io.Writer
}

// RemoveServiceContainer removes the service container for a service.
//
// The image the container runs is written down first, if the service had not
// recorded one. This is the last moment it is knowable: once the container is
// gone, the files in the service root are the only record of the version
// anywhere on the host, and a service that reaches a later start without one
// has nothing to be placed by.
//
// A repair that fails is not fatal. Stopping a service has to keep working on a
// host where these files cannot be written, and the caller asked for the
// container to be removed rather than for the record to be fixed.
func RemoveServiceContainer(ctx context.Context, input RemoveServiceContainerInput) error {
	if _, err := RecoverRecordedImage(ctx, RecoverRecordedImageInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	}); err != nil {
		warn(input.Warnings, fmt.Sprintf("unable to record the image %s runs: %s", input.ServiceName, err))
	}

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

// warn reports something a command carried on past. Nil means stderr, which is
// where a command's own logger would have put it: this package cannot reach the
// Ui type, because the package that holds it imports this one.
func warn(to io.Writer, message string) {
	if to == nil {
		to = os.Stderr
	}

	fmt.Fprintf(to, " !     %s\n", message)
}

// StartInput is the input for the Start function
type StartInput struct {
	// Datastore is the service to start
	Datastore *Datastore

	// ServiceName is the name of the service to start
	ServiceName string

	// Warnings is where a record that could not be written is reported. Nil
	// means stderr.
	Warnings io.Writer
}

// containerAction is what Start does with the container a service already has.
type containerAction int

const (
	// buildContainer makes one, because the service has none
	buildContainer containerAction = iota

	// keepContainer leaves a container that is up or on its way up alone
	keepContainer

	// unpauseContainer thaws one that was frozen
	unpauseContainer

	// resumeContainer starts one that is down, or replaces it where the record
	// and the container disagree about the version
	resumeContainer

	// replaceContainer takes away one nothing can be done with, and builds
	// another in its place
	replaceContainer
)

// actionForStatus maps what docker says about a service's container onto what
// Start does with it.
//
// Every state docker has, rather than the two Start used to ask for. A container
// in created, paused, restarting, removing or dead matched neither the running
// filter nor the exited one, so Start went on to build a container while one of
// that name was still there and died on the name conflict - which is how an
// interrupted create left a service that could not be started again. A state
// this does not know is taken away rather than built beside, for the same
// reason: one docker adds in a later release must not reopen that.
func actionForStatus(status string) containerAction {
	switch status {
	case "missing":
		return buildContainer
	case "running", "restarting":
		return keepContainer
	case "paused":
		return unpauseContainer
	case "created", "exited":
		return resumeContainer
	default:
		return replaceContainer
	}
}

// Start starts a service.
//
// A service runs the version it recorded, and only an upgrade changes that. So
// every branch below settles the record first, from the service's own container
// where there is one, and the record is then what the service is placed by -
// never the definition's current default, which a release bumps under services
// that never asked to move.
func Start(ctx context.Context, input StartInput) error {
	containerID := LiveContainerID(ctx, LiveContainerIDInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})

	status := "missing"
	if containerID != "" {
		status = backend.Status(ctx, containerID)
	}

	switch actionForStatus(status) {
	case keepContainer:
		// a service that is already up is left alone: rebuilding it onto its
		// record would be a restart nobody asked for
		input.recoverRecord(ctx, containerID)

		return input.writeContainerID(containerID)

	case unpauseContainer:
		// docker refuses to start a container it froze, so it is thawed rather
		// than started. Nothing here ever freezes one - this plugin's own pause
		// is a stop - so a container in this state was paused by hand, and
		// thawing it is the only reading of start that does not throw away the
		// container the service already has
		input.recoverRecord(ctx, containerID)

		if err := backend.Unpause(ctx, containerID); err != nil {
			return err
		}

		return input.writeContainerID(containerID)

	case resumeContainer:
		recorded := input.recoverRecord(ctx, containerID)

		// Version rather than backend.Image: a definition that builds runs a tag
		// dokku made, while the record holds the base it was built from, and
		// only this maps the one back to the other. Comparing the raw container
		// image would call every such service a mismatch.
		running := Version(ctx, VersionInput{
			ContainerID: containerID,
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
		})

		if !recorded.Complete() || recorded.Tagged() == running {
			if err := backend.Start(ctx, containerID); err != nil {
				return fmt.Errorf("failed to start container: %w", err)
			}

			if err := ServicePortReconcileStatus(ctx, ServicePortReconcileStatusInput{
				Datastore:   input.Datastore,
				ServiceName: input.ServiceName,
			}); err != nil {
				return fmt.Errorf("failed to reconcile port status: %w", err)
			}

			return nil
		}

		// the container disagrees with the record, so it is the container that
		// is wrong. The image is fetched before it is taken away, so that a pull
		// which fails leaves the service with the container it already had
		// rather than with none.
		if err := EnsureTaggedImage(ctx, EnsureTaggedImageInput{
			Action:      "start",
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
			TaggedImage: recorded.Tagged(),
		}); err != nil {
			return err
		}

		if err := RemoveServiceContainer(ctx, RemoveServiceContainerInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
			Warnings:    input.Warnings,
		}); err != nil {
			return err
		}

	case replaceContainer:
		// dead, removing, or something docker has not shipped yet. There is
		// nothing to start and nothing worth learning from it beyond the version
		// it ran, which removing it records on the way past
		if err := RemoveServiceContainer(ctx, RemoveServiceContainerInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
			Warnings:    input.Warnings,
		}); err != nil {
			return err
		}
	}

	recorded, err := RecoverRecordedImage(ctx, RecoverRecordedImageInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return err
	}

	// both halves, because half a record places a container as surely as none of
	// it does: the missing half would come from the definition, which is the
	// drift this is here to stop
	if !recorded.Complete() {
		return fmt.Errorf("service %s has no recorded image version and no container to recover one from; run `dokku %s:upgrade %s --image-version <version>` to choose the version it runs",
			input.ServiceName, input.Datastore.Properties().CommandPrefix, input.ServiceName)
	}

	taggedImage := recorded.Tagged()
	if err := EnsureTaggedImage(ctx, EnsureTaggedImageInput{
		Action:      "start",
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		TaggedImage: taggedImage,
	}); err != nil {
		return err
	}

	return input.Datastore.CreateServiceContainer(ctx, CreateServiceContainerInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		TaggedImage: taggedImage,
	})
}

// writeContainerID notes which container the service is answering on, which is
// what everything that addresses it by id reads.
func (input StartInput) writeContainerID(containerID string) error {
	return common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   containerID,
		Filename:  Files(input.Datastore, input.ServiceName).ID,
		GroupName: hostenv.SystemGroup(),
		Mode:      0644,
		Username:  hostenv.SystemUser(),
	})
}

// recoverRecord settles the service's record from a container it already has.
//
// Best effort on purpose. Every branch that calls it goes on to start a container
// that exists, which they could do before this file was ever written, and a host
// where the service root cannot be written is not a reason to refuse.
func (input StartInput) recoverRecord(ctx context.Context, containerID string) RecordedImage {
	recorded, err := RecoverRecordedImage(ctx, RecoverRecordedImageInput{
		ContainerID: containerID,
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		warn(input.Warnings, fmt.Sprintf("unable to record the image %s runs: %s", input.ServiceName, err))
	}

	return recorded
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
