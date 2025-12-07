package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// AttachNetworksToContainerInput is the input for the AttachNetworksToContainer function
type AttachNetworksToContainerInput struct {
	// ContainerID is the ID of the container to attach networks to
	ContainerID string

	// Networks is the networks to attach to the container
	Networks []string

	// NetworkAlias is the alias to use for the networks
	NetworkAlias string
}

// AttachNetworksToContainer attaches networks to a container
func AttachNetworksToContainer(ctx context.Context, input AttachNetworksToContainerInput) error {
	for _, network := range input.Networks {
		_, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
			Command: common.DockerBin(),
			Args:    []string{"network", "connect", "--alias", input.NetworkAlias, network, input.ContainerID},
		})
		if err != nil {
			return fmt.Errorf("failed to connect to network %s: %w", network, err)
		}
	}
	return nil
}

// CallExecCommandWithContext calls a command with a context
func CallExecCommandWithContext(ctx context.Context, input common.ExecCommandInput) (common.ExecCommandResponse, error) {
	result, err := common.CallExecCommandWithContext(ctx, input)
	if err != nil {
		return result, err
	}
	if result.ExitCode != 0 {
		if input.StreamStderr {
			return result, errors.New("command exited non-zero")
		}

		return result, fmt.Errorf("command exited non-zero: %s", result.StderrContents())
	}

	return result, nil
}

// CommitServiceConfigInput is the input for the CommitServiceConfig function
type CommitServiceConfigInput struct {
	// DatastoreType is the type of datastore to commit the service config for
	DatastoreType string
	// ServiceName is the name of the service to commit the service config for
	ServiceName string
	// CustomEnv is the custom environment variables to commit for the service
	CustomEnv string
	// ConfigOptions is the configuration options to commit for the service
	ConfigOptions string
	// Image is the image to commit for the service
	Image string
	// ImageVersion is the image version to commit for the service
	ImageVersion string
	// Memory is the memory limit to commit for the service
	Memory int
	// ShmSize is the shared memory size to commit for the service
	ShmSize string
	// InitialNetwork is the initial network to commit for the service
	InitialNetwork string
	// PostCreateNetworks is the networks to attach the service container to after service creation
	PostCreateNetworks []string
	// PostStartNetworks is the networks to attach the service container to after service start
	PostStartNetworks []string
}

// CommitServiceConfig commits the service config for a given service
func CommitServiceConfig(input CommitServiceConfigInput) error {
	if input.DatastoreType == "" {
		return fmt.Errorf("datastore type is required")
	}

	if input.ServiceName == "" {
		return fmt.Errorf("service name is required")
	}

	serviceWrapper, ok := Services[input.DatastoreType]
	if !ok {
		return fmt.Errorf("datastore type %s is not supported", input.DatastoreType)
	}

	serviceFiles := Files(serviceWrapper, input.ServiceName)

	lines := strings.Split(input.CustomEnv, ";")
	err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   strings.Join(lines, "\n"),
		Filename:  serviceFiles.Env,
		GroupName: SystemGroup(),
		Mode:      0644,
		Username:  SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write env to %s: %w", serviceFiles.Env, err)
	}

	err = common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   input.ConfigOptions,
		Filename:  serviceFiles.ConfigOptions,
		GroupName: SystemGroup(),
		Mode:      0644,
		Username:  SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write config options to %s: %w", serviceFiles.ConfigOptions, err)
	}

	err = common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   strconv.Itoa(input.Memory),
		Filename:  serviceFiles.Memory,
		GroupName: SystemGroup(),
		Mode:      0644,
		Username:  SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write memory to %s: %w", serviceFiles.Memory, err)
	}

	err = common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   input.ShmSize,
		Filename:  serviceFiles.ShmSize,
		GroupName: SystemGroup(),
		Mode:      0644,
		Username:  SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write shm size to %s: %w", serviceFiles.ShmSize, err)
	}

	err = common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   input.Image,
		Filename:  serviceFiles.Image,
		GroupName: SystemGroup(),
		Mode:      0644,
		Username:  SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write image to %s: %w", serviceFiles.Image, err)
	}

	err = common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   input.ImageVersion,
		Filename:  serviceFiles.ImageVersion,
		GroupName: SystemGroup(),
		Mode:      0644,
		Username:  SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write image version to %s: %w", serviceFiles.ImageVersion, err)
	}

	properties := serviceWrapper.Properties()
	err = common.PropertyWrite(properties.CommandPrefix, input.ServiceName, "initial-network", input.InitialNetwork)
	if err != nil {
		return fmt.Errorf("failed to write initial-network property: %w", err)
	}

	err = common.PropertyWrite(properties.CommandPrefix, input.ServiceName, "post-create-network", strings.Join(input.PostCreateNetworks, ","))
	if err != nil {
		return fmt.Errorf("failed to write post create network property: %w", err)
	}

	err = common.PropertyWrite(properties.CommandPrefix, input.ServiceName, "post-start-network", strings.Join(input.PostStartNetworks, ","))
	if err != nil {
		return fmt.Errorf("failed to write post start network property: %w", err)
	}

	return nil
}

// GenerateRandomHexString generates a random hex string
func GenerateRandomHexString(length int) (string, error) {
	bytes := make([]byte, length/2)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// ImageForServiceInput is the input for the ImageForService function
type ImageForServiceInput struct {
	// DatastoreType is the type of datastore to get the image for
	DatastoreType string
	// ServiceName is the name of the service to get the image for
	ServiceName string
	// ImageOverride is the image to use for the service
	ImageOverride string
	// ImageVersionOverride is the image version to use for the service
	ImageVersionOverride string
}

// ImageForService retrieves the image for a service
func ImageForService(input ImageForServiceInput) (string, error) {
	if input.DatastoreType == "" {
		return "", fmt.Errorf("datastore type is required")
	}

	if input.ServiceName == "" {
		return "", fmt.Errorf("service name is required")
	}

	serviceWrapper, ok := Services[input.DatastoreType]
	if !ok {
		return "", fmt.Errorf("datastore type %s is not supported", input.DatastoreType)
	}

	serviceProperties := serviceWrapper.Properties()
	serviceFiles := Files(serviceWrapper, input.ServiceName)

	image := serviceProperties.DefaultImage
	imageVersion := serviceProperties.DefaultImageVersion

	// check if the IMAGE file exists
	if _, err := os.Stat(serviceFiles.Image); err == nil {
		content, err := os.ReadFile(serviceFiles.Image)
		if err != nil {
			return "", err
		}
		diskSpecifiedImage := strings.TrimSpace(string(content))
		if diskSpecifiedImage != "" {
			image = diskSpecifiedImage
		}
	}

	// check if the IMAGE_VERSION file exists
	if _, err := os.Stat(serviceFiles.ImageVersion); err == nil {
		content, err := os.ReadFile(serviceFiles.ImageVersion)
		if err != nil {
			return "", err
		}
		diskSpecifiedImageVersion := strings.TrimSpace(string(content))
		if diskSpecifiedImageVersion != "" {
			imageVersion = diskSpecifiedImageVersion
		}
	}

	if input.ImageOverride != "" {
		image = input.ImageOverride
	}

	if input.ImageVersionOverride != "" {
		imageVersion = input.ImageVersionOverride
	}

	return fmt.Sprintf("%s:%s", image, imageVersion), nil
}

// PullTaggedImage pulls a tagged image
func PullTaggedImage(ctx context.Context, taggedImage string) (bool, error) {
	result, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command:      common.DockerBin(),
		Args:         []string{"image", "pull", taggedImage},
		StreamStderr: true,
	})
	if err != nil {
		return false, err
	}
	if result.ExitCode == 0 {
		return true, nil
	}

	return false, errors.New("unspecified error")
}

// FilterServicesInput is the input for the FilterServices function
type FilterServicesInput struct {
	// DatastoreType is the type of datastore to filter services for
	DatastoreType string
	// Services is the services to filter
	Services []string
	// Trace is whether to enable trace output
	Trace bool
}

// FilterServices filters out services that are not allowed by the user-auth-service trigger
func FilterServices(ctx context.Context, input FilterServicesInput) ([]string, error) {
	if len(input.Services) == 0 {
		return input.Services, nil
	}

	// check if there are plugins with the user-auth-service trigger
	triggers, err := filepath.Glob(filepath.Join(PluginPath, "enabled", "*", "user-auth-service"))
	if err != nil {
		if os.IsNotExist(err) {
			return input.Services, nil
		}
		return input.Services, fmt.Errorf("failed to glob plugins with user-auth-service trigger: %w", err)
	}

	if len(triggers) == 0 {
		return input.Services, nil
	}

	// check if there is only one trigger and if the file  `PLUGIN_PATH/enabled/20_events/user-auth-service` exists
	if len(triggers) == 1 {
		if _, err := os.Stat(filepath.Join(PluginPath, "enabled", "20_events", "user-auth-service")); err == nil {
			return input.Services, nil
		}
	}

	// the output of this trigger should be all the services a user has access to
	defaultSShUser := os.Getenv("SSH_USER")
	defaultSShName := os.Getenv("SSH_NAME")
	if defaultSShUser == "" {
		defaultSShUser = os.Getenv("USER")
	}
	if defaultSShName == "" {
		defaultSShName = "default"
	}

	serviceWrapper, ok := Services[input.DatastoreType]
	if !ok {
		return input.Services, fmt.Errorf("datastore type %s is not supported", input.DatastoreType)
	}
	pluginCommandPrefix := serviceWrapper.Properties().CommandPrefix

	// call the user-auth-service trigger
	results, err := CallPlugnTriggerWithContext(ctx, common.PlugnTriggerInput{
		Trigger: "user-auth-app",
		Args:    append([]string{defaultSShUser, defaultSShName, pluginCommandPrefix}, input.Services...),
		Env: map[string]string{
			"SSH_NAME": defaultSShName,
			"SSH_USER": defaultSShUser,
			"TRACE":    strconv.FormatBool(input.Trace),
		},
	})
	if err != nil {
		return input.Services, fmt.Errorf("failed to call user-auth-service trigger: %w", err)
	}

	filteredServices := make([]string, 0)
	for line := range strings.SplitSeq(results.StderrContents(), "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}

		filteredServices = append(filteredServices, trimmedLine)
	}

	return filteredServices, nil
}

// CallPlugnTrigger calls a plugin trigger
func CallPlugnTriggerWithContext(ctx context.Context, input common.PlugnTriggerInput) (common.ExecCommandResponse, error) {
	if os.Getenv("PLUGIN_PATH") == "" {
		return common.ExecCommandResponse{
			ExitCode: 0,
		}, nil
	}

	return common.CallPlugnTriggerWithContext(ctx, input)
}

// ServicePortReconcileStatusInput is the input for the ServicePortReconcileStatus function
type ServicePortReconcileStatusInput struct {
	// DatastoreType is the type of datastore to reconcile the port for
	DatastoreType string
	// ServiceName is the name of the service to reconcile the port for
	ServiceName string
}

// ServicePortReconcileStatus reconciles the port for a service
func ServicePortReconcileStatus(ctx context.Context, input ServicePortReconcileStatusInput) error {
	serviceWrapper, ok := Services[input.DatastoreType]
	if !ok {
		return fmt.Errorf("datastore type %s is not supported", input.DatastoreType)
	}

	serviceProperties := serviceWrapper.Properties()
	serviceFiles := Files(serviceWrapper, input.ServiceName)
	portFile := serviceFiles.Port
	containerName := ContainerName(serviceWrapper, input.ServiceName)
	exposedName := fmt.Sprintf("%s.ambassador", containerName)

	if !common.FileExists(portFile) || common.ReadFirstLine(portFile) == "" {
		if common.ContainerExists(exposedName) {
			_, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
				Command: common.DockerBin(),
				Args:    []string{"container", "stop", exposedName},
			})
			if err != nil {
				return fmt.Errorf("failed to stop container %s: %w", exposedName, err)
			}
		}

		return nil
	}

	if common.ContainerIsRunning(exposedName) {
		return nil
	}

	if common.ContainerExists(exposedName) {
		_, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
			Command: common.DockerBin(),
			Args:    []string{"container", "start", exposedName},
		})
		if err != nil {
			return fmt.Errorf("failed to start container %s: %w", exposedName, err)
		}
		return nil
	}

	portContents, err := common.FileToSlice(portFile)
	if err != nil {
		return fmt.Errorf("failed to read port file %s: %w", portFile, err)
	}
	if len(portContents) == 0 {
		return fmt.Errorf("port file %s is empty", portFile)
	}

	dockerRunOptions := []string{
		"container",
		"run",
		"-d",
		"--link=" + fmt.Sprintf("%s:%s", containerName, serviceProperties.CommandPrefix),
		"--name=" + exposedName,
		"--restart=always",
		"--label=dokku=ambassador",
		"--label=dokku.ambassador=" + serviceProperties.CommandPrefix,
	}

	for _, port := range portContents {
		dockerRunOptions = append(dockerRunOptions, "--port="+port)
	}

	dockerRunOptions = append(dockerRunOptions, PluginAmbassadorImage)

	_, err = CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    dockerRunOptions,
	})
	if err != nil {
		return fmt.Errorf("failed to run container %s: %w", exposedName, err)
	}
	return nil
}

// SystemGroup returns the system group
func SystemGroup() string {
	systemGroup := os.Getenv("DOKKU_SYSTEM_GROUP")
	if systemGroup == "" {
		systemGroup = "dokku"
	}
	return systemGroup
}

// SystemUser returns the system user
func SystemUser() string {
	systemUser := os.Getenv("DOKKU_SYSTEM_USER")
	if systemUser == "" {
		systemUser = "dokku"
	}
	return systemUser
}

// ValidateServiceName validates a service name
func ValidateServiceName(serviceName string) error {
	if serviceName == "" {
		return fmt.Errorf("service name is required")
	}

	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(serviceName) {
		return fmt.Errorf("service name must contain only letters, numbers, underscores, and hyphens")
	}

	return nil
}

// ValidateTaggedImageExists checks if the image exists
func ValidateTaggedImageExists(taggedImage string) error {
	if common.VerifyImage(taggedImage) {
		return nil
	}

	return fmt.Errorf("image %s does not exist", taggedImage)
}

// WriteDatabaseName writes the database name to the service
func WriteDatabaseName(datastoreType string, serviceName string) error {
	serviceWrapper, ok := Services[datastoreType]
	if !ok {
		return fmt.Errorf("datastore type %s is not supported", datastoreType)
	}

	serviceFiles := Files(serviceWrapper, serviceName)
	sanitizedDatabaseName := strings.ReplaceAll(serviceName, ".", "_")
	sanitizedDatabaseName = strings.ReplaceAll(sanitizedDatabaseName, "-", "_")
	err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   sanitizedDatabaseName,
		Filename:  serviceFiles.DatabaseName,
		GroupName: SystemGroup(),
		Mode:      0644,
		Username:  SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write database name to %s: %w", serviceFiles.DatabaseName, err)
	}

	return nil
}
