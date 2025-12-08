package datastores

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dokku/dokku/plugins/common"
	"mvdan.cc/sh/v3/shell"
)

// RedisService is the service for Redis
type RedisService struct {
	CommonService
}

// CreateService creates a new service
func (s *RedisService) CreateService(ctx context.Context, serviceName string) error {
	serviceFolders := Folders(s, serviceName)
	serviceRoot := serviceFolders.Root
	redisServiceConfig := filepath.Join(serviceFolders.Config, "redis.conf")

	redisConfigPath := os.Getenv("REDIS_CONFIG_PATH")
	if redisConfigPath == "" {
		err := common.WriteStringToFile(common.WriteStringToFileInput{
			Content:   "# requirepass",
			Filename:  redisServiceConfig,
			GroupName: SystemGroup(),
			Mode:      0644,
			Username:  SystemUser(),
		})
		if err != nil {
			return fmt.Errorf("unable to write to %s: %w", redisServiceConfig, err)
		}
	} else {
		if err := common.Copy(redisConfigPath, redisServiceConfig); err != nil {
			return fmt.Errorf("unable to copy %s to %s: %w", redisConfigPath, redisServiceConfig, err)
		}
	}

	password := os.Getenv("SERVICE_PASSWORD")
	if password == "" {
		var err error
		password, err = GenerateRandomHexString(64)
		if err != nil {
			return fmt.Errorf("unable to generate random hex string: %w", err)
		}
	}

	if password != "" {
		err := common.WriteStringToFile(common.WriteStringToFileInput{
			Content:   password,
			Filename:  filepath.Join(serviceRoot, "PASSWORD"),
			GroupName: SystemGroup(),
			Mode:      0640,
			Username:  SystemUser(),
		})
		if err != nil {
			return fmt.Errorf("unable to write password to %s: %w", filepath.Join(serviceRoot, "PASSWORD"), err)
		}
	}

	// replace any lines that start with "# requirepass" with "requirepass <password>"
	lines, err := common.FileToSlice(redisServiceConfig)
	if err != nil {
		return fmt.Errorf("unable to read %s: %w", redisServiceConfig, err)
	}

	newLines := make([]string, 0)
	for _, line := range lines {
		if strings.HasPrefix(line, "# requirepass") {
			newLines = append(newLines, "requirepass "+password)
		} else {
			newLines = append(newLines, line)
		}
	}
	err = common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   strings.Join(newLines, "\n"),
		Filename:  redisServiceConfig,
		GroupName: SystemGroup(),
		Mode:      0644,
		Username:  SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("unable to write to %s: %w", redisServiceConfig, err)
	}

	return nil
}

// CreateServiceContainer creates a new service container
func (s *RedisService) CreateServiceContainer(ctx context.Context, input CreateServiceContainerInput) error {
	serviceProperties := s.Properties()
	serviceFolders := Folders(input.Datastore, input.ServiceName)
	serviceFiles := Files(input.Datastore, input.ServiceName)
	containerName := ContainerName(input.Datastore, input.ServiceName)
	cidFilename := serviceFiles.ID

	lines, err := common.FileToSlice(serviceFiles.ConfigOptions)
	if err != nil {
		return fmt.Errorf("unable to read config options from %s: %w", serviceFiles.ConfigOptions, err)
	}
	startArgsToAppend, err := shell.Fields(strings.Join(lines, "\n"), func(name string) string {
		return ""
	})
	if err != nil {
		return fmt.Errorf("unable to parse config options: %w", err)
	}

	// remove the ID file if it exists
	if err := os.RemoveAll(cidFilename); err != nil {
		return fmt.Errorf("unable to remove ID file from %s: %w", cidFilename, err)
	}

	dockerCreateArgs := []string{
		"container",
		"create",
		"--cidfile=" + cidFilename,
		"--env-file=" + serviceFiles.Env,
		"--hostname=" + containerName,
		"--label=dokku.service=" + serviceProperties.CommandPrefix,
		"--label=dokku=service",
		"--name=" + containerName,
		"--restart=always",
		"--volume=" + serviceFolders.HostConfig + ":/usr/local/etc/redis",
		"--volume=" + serviceFolders.HostData + ":/data",
	}

	memory := common.ReadFirstLine(serviceFiles.Memory)
	if memory != "" {
		dockerCreateArgs = append(dockerCreateArgs, "--memory="+memory+"m")
	}

	shmSize := common.ReadFirstLine(serviceFiles.ShmSize)
	if shmSize != "" {
		dockerCreateArgs = append(dockerCreateArgs, "--shm-size="+shmSize)
	}

	networkAlias := DNSHostname(input.Datastore, input.ServiceName)
	initialNetwork := InitialNetwork(input.Datastore, input.ServiceName)
	if err != nil {
		return fmt.Errorf("failed to get initial network: %w", err)
	}
	if initialNetwork != "" {
		dockerCreateArgs = append(dockerCreateArgs, "--network="+initialNetwork)
		dockerCreateArgs = append(dockerCreateArgs, "--network-alias="+networkAlias)
	}

	taggedImage := input.TaggedImage
	if taggedImage == "" {
		image := common.ReadFirstLine(serviceFiles.Image)
		if image == "" {
			image = serviceProperties.DefaultImage
		}

		imageVersion := common.ReadFirstLine(serviceFiles.ImageVersion)
		if imageVersion == "" {
			imageVersion = serviceProperties.DefaultImageVersion
		}
		taggedImage = fmt.Sprintf("%s:%s", image, imageVersion)
	}

	dockerCreateArgs = append(dockerCreateArgs, taggedImage)
	dockerCreateArgs = append(dockerCreateArgs, "redis-server")
	dockerCreateArgs = append(dockerCreateArgs, "/usr/local/etc/redis/redis.conf")
	dockerCreateArgs = append(dockerCreateArgs, []string{"--bind", "0.0.0.0"}...)
	for _, arg := range startArgsToAppend {
		if arg == "" {
			continue
		}

		dockerCreateArgs = append(dockerCreateArgs, arg)
	}

	// create the container
	_, err = CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    dockerCreateArgs,
	})
	if err != nil {
		return err
	}

	postCreateNetworks := common.PropertyGet(serviceProperties.CommandPrefix, input.ServiceName, "post-create-network")
	if postCreateNetworks != "" {
		err := AttachNetworksToContainer(ctx, AttachNetworksToContainerInput{
			ContainerID:  common.ReadFirstLine(cidFilename),
			Networks:     strings.Split(postCreateNetworks, ","),
			NetworkAlias: networkAlias,
		})
		if err != nil {
			return err
		}
	}

	containerID := common.ReadFirstLine(cidFilename)
	if containerID == "" {
		return fmt.Errorf("failed to read container ID from %s", cidFilename)
	}

	// start the container
	_, err = CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "start", containerID},
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

	postStartNetworks := common.PropertyGet(serviceProperties.CommandPrefix, input.ServiceName, "post-start-network")
	if postStartNetworks != "" {
		err := AttachNetworksToContainer(ctx, AttachNetworksToContainerInput{
			ContainerID:  common.ReadFirstLine(cidFilename),
			Networks:     strings.Split(postStartNetworks, ","),
			NetworkAlias: networkAlias,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// Properties returns the properties for a service
func (s *RedisService) Properties() ServiceStruct {
	return ServiceStruct{
		CommandPrefix:       "redis",
		ConfigSuffix:        "config",
		ConfigVariable:      "REDIS_CONFIG_OPTIONS",
		DefaultImage:        "redis",
		DefaultImageVersion: "latest",
		EnvVariable:         "REDIS_CUSTOM_ENV",
		ImagePullVariable:   "REDIS_DISABLE_PULL",
		Ports:               []int{6379},
		WaitPort:            6379,
	}
}

// ServiceType returns the type of service
func (s *RedisService) ServiceType() string {
	return "redis"
}

// URL gets the url for a service
func (s *RedisService) URL(serviceName string) string {
	return fmt.Sprintf("redis://%s:%d", DNSHostname(s, serviceName), s.Properties().Ports[0])
}
