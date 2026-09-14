package datastores

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dokku/dokku/plugins/common"
	"mvdan.cc/sh/v3/shell"
)

// RedisService is the service for Redis
type RedisService struct{}

// CreateService creates a new service
func (s *RedisService) CreateService(ctx context.Context, serviceName string) error {
	serviceFolders := Folders(s, serviceName)
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
			Filename:  Files(s, serviceName).Password,
			GroupName: SystemGroup(),
			Mode:      0640,
			Username:  SystemUser(),
		})
		if err != nil {
			return fmt.Errorf("unable to write password to %s: %w", Files(s, serviceName).Password, err)
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

// taggedImage returns the image a service runs, falling back to the datastore
// default when the service has not pinned one
func (s *RedisService) taggedImage(serviceName string) string {
	serviceFiles := Files(s, serviceName)
	serviceProperties := s.Properties()

	image := common.ReadFirstLine(serviceFiles.Image)
	if image == "" {
		image = serviceProperties.DefaultImage
	}

	imageVersion := common.ReadFirstLine(serviceFiles.ImageVersion)
	if imageVersion == "" {
		imageVersion = serviceProperties.DefaultImageVersion
	}

	return fmt.Sprintf("%s:%s", image, imageVersion)
}

// exportTimeoutSeconds bounds the wait for a background save to finish
const exportTimeoutSeconds = 120

// persistenceField reads a single field out of the redis persistence info section
func (s *RedisService) persistenceField(ctx context.Context, serviceName string, field string) string {
	result, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args: []string{
			"container", "exec", ContainerName(s, serviceName),
			"redis-cli", "--no-auth-warning", "-a", Password(s, serviceName),
			"INFO", "persistence",
		},
	})
	if err != nil {
		return ""
	}

	return persistenceFieldFrom(result.Stdout, field)
}

// persistenceFieldFrom pulls a single field out of a redis INFO section. The
// section is CRLF delimited and carries comment lines starting with a #.
func persistenceFieldFrom(info string, field string) string {
	for _, line := range strings.Split(info, "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if found && name == field {
			return value
		}
	}

	return ""
}

// ExportService writes a dump of the service's data to a writer
func (s *RedisService) ExportService(ctx context.Context, input ExportServiceInput) error {
	containerName := ContainerName(s, input.ServiceName)
	password := Password(s, input.ServiceName)

	// redis-cli exits zero even when the server rejects the command, so the reply
	// has to be inspected rather than just the exit status
	result, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args: []string{
			"container", "exec", containerName,
			"redis-cli", "--no-auth-warning", "-a", password, "BGSAVE",
		},
	})
	if err != nil || !strings.Contains(result.Stdout, "Background saving") {
		return fmt.Errorf("unable to start a background save: %s", strings.TrimSpace(result.Stdout+result.Stderr))
	}

	// BGSAVE returns as soon as the child is forked, so wait on the completion
	// flag rather than on LASTSAVE. LASTSAVE only has second resolution and is
	// seeded with the server start time, so a save that finishes in the same
	// second the container started is indistinguishable from no save at all.
	for waited := 0; s.persistenceField(ctx, input.ServiceName, "rdb_bgsave_in_progress") == "1"; waited++ {
		if waited >= exportTimeoutSeconds {
			return fmt.Errorf("background save did not complete within %d seconds", exportTimeoutSeconds)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}

	if s.persistenceField(ctx, input.ServiceName, "rdb_last_bgsave_status") != "ok" {
		return fmt.Errorf("background save failed, check the %s service logs", s.Title())
	}

	dump, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "exec", containerName, "cat", "/data/dump.rdb"},
	})
	if err != nil {
		return fmt.Errorf("unable to read the dump file: %w", err)
	}

	if _, err := io.WriteString(input.Writer, dump.Stdout); err != nil {
		return fmt.Errorf("unable to write the dump: %w", err)
	}

	return nil
}

// ImportService replaces the service's data with what is read from a reader
func (s *RedisService) ImportService(ctx context.Context, input ImportServiceInput) error {
	serviceFolders := Folders(s, input.ServiceName)
	taggedImage := s.taggedImage(input.ServiceName)
	volume := fmt.Sprintf("%s:/data", serviceFolders.HostData)

	if err := RemoveServiceContainer(ctx, RemoveServiceContainerInput{
		Datastore:   s,
		ServiceName: input.ServiceName,
	}); err != nil {
		return err
	}

	// the dump belongs to the datastore user inside the container, so it is
	// removed and later chowned from a container rather than from the host
	if _, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "run", "--rm", "--volume", volume, taggedImage, "bash", "-c", "rm -f /data/dump.rdb"},
	}); err != nil {
		return fmt.Errorf("unable to remove the existing dump file: %w", err)
	}

	dumpFile := filepath.Join(serviceFolders.Data, "dump.rdb")
	handle, err := os.Create(dumpFile)
	if err != nil {
		return fmt.Errorf("unable to create %s: %w", dumpFile, err)
	}

	if _, err := io.Copy(handle, input.Reader); err != nil {
		handle.Close()
		return fmt.Errorf("unable to write %s: %w", dumpFile, err)
	}

	if err := handle.Close(); err != nil {
		return fmt.Errorf("unable to close %s: %w", dumpFile, err)
	}

	if _, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
		Command: common.DockerBin(),
		Args:    []string{"container", "run", "--rm", "--volume", volume, taggedImage, "bash", "-c", "chown redis: /data/dump.rdb"},
	}); err != nil {
		return fmt.Errorf("unable to take ownership of the dump file: %w", err)
	}

	return Start(ctx, StartInput{
		Datastore:   s,
		ServiceName: input.ServiceName,
	})
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
		taggedImage = s.taggedImage(input.ServiceName)
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
		AltAlias:            "DOKKU_REDIS",
		ConfigVariable:      "REDIS_CONFIG_OPTIONS",
		DefaultAlias:        "REDIS",
		DefaultImage:        "redis",
		DefaultImageVersion: "latest",
		EnvVariable:         "REDIS_CUSTOM_ENV",
		ImagePullVariable:   "REDIS_DISABLE_PULL",
		PluginVariable:      "REDIS",
		Ports:               []int{6379},
		Scheme:              "redis",
		WaitPort:            6379,
	}
}

// ServiceType returns the type of service
func (s *RedisService) ServiceType() string {
	return "redis"
}

// Title returns the service name in title case
func (s *RedisService) Title() string {
	return "Redis"
}

// URL gets the url for a service
func (s *RedisService) URL(serviceName string, schemeOverride string) string {
	scheme := s.Properties().Scheme
	if schemeOverride != "" {
		scheme = schemeOverride
	}
	return fmt.Sprintf("%s://:%s@%s:%d", scheme, Password(s, serviceName), DNSHostname(s, serviceName), s.Properties().Ports[0])
}
