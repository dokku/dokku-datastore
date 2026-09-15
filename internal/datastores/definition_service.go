package datastores

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dokku/dokku-datastore/internal/backend"
	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/image"
	"github.com/dokku/dokku-datastore/internal/render"
	"github.com/dokku/dokku-datastore/internal/seed"
	"github.com/dokku/dokku-datastore/internal/verb"

	"github.com/dokku/dokku/plugins/common"
	"mvdan.cc/sh/v3/shell"
)

// DefinitionService is a datastore described by a docker-compose.yml rather than
// by Go code. It satisfies the same interface the hand written services did, so
// the forty command files that look a datastore up in the map need no change: a
// datastore becomes data, and this is the only thing that has to know that.
type DefinitionService struct {
	// Definition is the parsed definition backing this datastore.
	Definition definition.Definition
}

// CreateService writes the credentials and config files a service needs before
// its container exists.
func (s *DefinitionService) CreateService(ctx context.Context, serviceName string) error {
	if err := s.writeSecrets(serviceName); err != nil {
		return err
	}

	configs, err := render.Configs(render.Input{
		Definition: s.Definition,
		Scope:      s.scope(serviceName),
	})
	if err != nil {
		return fmt.Errorf("unable to resolve the config files: %w", err)
	}

	return seed.Configs(seed.Input{
		Configs:   configs,
		Username:  SystemUser(),
		GroupName: SystemGroup(),
	})
}

// writeSecrets generates and persists the credentials the definition declares.
// They are generated once, in Go, and read off disk everywhere else: a value
// four things read must not be produced by a template that runs again each time.
func (s *DefinitionService) writeSecrets(serviceName string) error {
	serviceFolders := Folders(s, serviceName)

	for name, secret := range s.Definition.Dokku.Secrets {
		filename := fmt.Sprintf("%s/%s", serviceFolders.Root, secret.File)
		if common.FileExists(filename) {
			continue
		}

		value := ""
		if secret.Env != "" {
			value = os.Getenv(secret.Env)
		}

		if value == "" {
			generated, err := GenerateRandomHexString(secret.Length)
			if err != nil {
				return fmt.Errorf("unable to generate the %s secret: %w", name, err)
			}

			value = generated
		}

		err := common.WriteStringToFile(common.WriteStringToFileInput{
			Content:   value,
			Filename:  filename,
			GroupName: SystemGroup(),
			Mode:      0640,
			Username:  SystemUser(),
		})
		if err != nil {
			return fmt.Errorf("unable to write the %s secret to %s: %w", name, filename, err)
		}
	}

	return nil
}

// CreateServiceContainer creates and starts the service container.
func (s *DefinitionService) CreateServiceContainer(ctx context.Context, input CreateServiceContainerInput) error {
	serviceFiles := Files(input.Datastore, input.ServiceName)
	cidFilename := serviceFiles.ID

	lines, err := common.FileToSlice(serviceFiles.ConfigOptions)
	if err != nil {
		return fmt.Errorf("unable to read config options from %s: %w", serviceFiles.ConfigOptions, err)
	}

	configOptions, err := shell.Fields(strings.Join(lines, "\n"), func(name string) string {
		return ""
	})
	if err != nil {
		return fmt.Errorf("unable to parse config options: %w", err)
	}

	// remove the ID file if it exists
	if err := os.RemoveAll(cidFilename); err != nil {
		return fmt.Errorf("unable to remove ID file from %s: %w", cidFilename, err)
	}

	if err := s.writePayload(input.ServiceName); err != nil {
		return err
	}

	if err := s.writeScripts(input.ServiceName); err != nil {
		return err
	}

	scope := s.scope(input.ServiceName)
	scope.InitialNetwork = InitialNetwork(input.Datastore, input.ServiceName)
	if input.TaggedImage != "" {
		scope.TaggedImage = input.TaggedImage
		scope.Image, scope.ImageVersion = cutTaggedImage(input.TaggedImage)
	}

	// a definition that bakes tooling into the image runs the built one, since
	// that is where its verbs live. The base is still what the service pinned
	// and what its IMAGE and IMAGE_VERSION files record.
	runImage, err := s.buildImage(ctx, scope.TaggedImage)
	if err != nil {
		return err
	}

	scope.TaggedImage = runImage
	scope.Image, scope.ImageVersion = cutTaggedImage(runImage)

	arguments, err := render.ContainerArgs(render.Input{
		Definition:    s.Definition,
		Scope:         scope,
		ConfigOptions: configOptions,
		EnvFile:       serviceFiles.Env,
		IDFile:        cidFilename,
	})
	if err != nil {
		return err
	}

	if err := s.writeCompose(input.ServiceName, scope, configOptions, serviceFiles.Env, cidFilename); err != nil {
		return err
	}

	if err := s.createContainer(ctx, input.ServiceName, arguments, cidFilename); err != nil {
		return err
	}

	networkAlias := DNSHostname(input.Datastore, input.ServiceName)
	postCreateNetworks := common.PropertyGet(s.Definition.Dokku.Plugin, input.ServiceName, "post-create-network")
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

	if err := s.startContainer(ctx, input.ServiceName, containerID); err != nil {
		return err
	}

	err = ServicePortReconcileStatus(ctx, ServicePortReconcileStatusInput{
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile port status: %w", err)
	}

	postStartNetworks := common.PropertyGet(s.Definition.Dokku.Plugin, input.ServiceName, "post-start-network")
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

// writePayload writes the scripts the definition mounts into the container.
//
// Unlike a config, these are always overwritten: a config is the operator's file
// and seeding it twice would discard their edits, while a script ships with the
// binary and a stale one left after an upgrade would be a verb running last
// release's code.
func (s *DefinitionService) writePayload(serviceName string) error {
	files := render.RootfsFiles(render.Input{
		Definition: s.Definition,
		Scope:      s.scope(serviceName),
	})

	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file.Path), 0755); err != nil {
			return fmt.Errorf("unable to create %s: %w", filepath.Dir(file.Path), err)
		}

		if err := os.WriteFile(file.Path, file.Contents, file.Mode); err != nil {
			return fmt.Errorf("unable to write %s: %w", file.Path, err)
		}

		// applied explicitly because WriteFile does not change the mode of a
		// file that already exists, and the mount carries it into the container
		if err := os.Chmod(file.Path, file.Mode); err != nil {
			return fmt.Errorf("unable to set the mode on %s: %w", file.Path, err)
		}
	}

	return nil
}

// backend reports which execution backend a service is driven with.
func (s *DefinitionService) backend(serviceName string) string {
	return backend.Select(backend.SelectInput{
		Recorded: common.ReadFirstLine(Files(s, serviceName).Backend),
		Default:  hostenv.Backend(),
	})
}

// composeInput addresses a service's rendered compose file.
func (s *DefinitionService) composeInput(serviceName string) backend.ComposeInput {
	return backend.ComposeInput{
		File:    Files(s, serviceName).Compose,
		Project: fmt.Sprintf("dokku-%s-%s", s.Definition.Dokku.Plugin, serviceName),
	}
}

// createContainer makes the service container with whichever backend the
// service is driven by, and records which one that was.
//
// The two backends are handed the same resolved values: the argv and the
// compose file are rendered together, so what they produce is the same
// container addressed by the same name.
func (s *DefinitionService) createContainer(ctx context.Context, serviceName string, arguments render.ContainerArgsInput, idFile string) error {
	selected := s.backend(serviceName)

	if err := s.recordBackend(serviceName, selected); err != nil {
		return err
	}

	if selected != backend.Compose {
		_, err := CallExecCommandWithContext(ctx, common.ExecCommandInput{
			Command: common.DockerBin(),
			Args:    render.DockerCreateArgs(arguments),
		})

		return err
	}

	if err := backend.ComposeCreate(ctx, s.composeInput(serviceName)); err != nil {
		return err
	}

	// compose writes no cidfile, so the id every read path reads is taken from
	// the container it just made
	containerID := backend.LiveContainerID(ctx, backend.LiveContainerIDInput{
		ContainerName: arguments.ContainerName,
	})
	if containerID == "" {
		return fmt.Errorf("failed to find the container compose created for %s", serviceName)
	}

	return common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   containerID,
		Filename:  idFile,
		GroupName: SystemGroup(),
		Mode:      0644,
		Username:  SystemUser(),
	})
}

// startContainer starts the service container with the service's backend.
func (s *DefinitionService) startContainer(ctx context.Context, serviceName string, containerID string) error {
	if s.backend(serviceName) != backend.Compose {
		return backend.Start(ctx, containerID)
	}

	return backend.ComposeStart(ctx, s.composeInput(serviceName))
}

// recordBackend writes which backend made a service, so that a later change to
// the host default does not address it the other way.
func (s *DefinitionService) recordBackend(serviceName string, selected string) error {
	filename := Files(s, serviceName).Backend
	err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   selected,
		Filename:  filename,
		GroupName: SystemGroup(),
		Mode:      0644,
		Username:  SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("unable to write %s: %w", filename, err)
	}

	return nil
}

// writeScripts writes the definition's hook scripts into the service directory.
// They run on the host rather than in the container, which is why they are kept
// apart from the payload and are not mounted anywhere.
func (s *DefinitionService) writeScripts(serviceName string) error {
	if len(s.Definition.Scripts) == 0 {
		return nil
	}

	root := filepath.Join(Folders(s, serviceName).Root, "bin")
	if err := os.MkdirAll(root, 0755); err != nil {
		return fmt.Errorf("unable to create %s: %w", root, err)
	}

	for name, contents := range s.Definition.Scripts {
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("unable to create %s: %w", filepath.Dir(target), err)
		}

		if err := os.WriteFile(target, contents, 0755); err != nil {
			return fmt.Errorf("unable to write %s: %w", target, err)
		}

		// applied explicitly, because WriteFile leaves the mode of a file that
		// already exists alone and a hook that cannot run is a create that fails
		if err := os.Chmod(target, 0755); err != nil {
			return fmt.Errorf("unable to set the mode on %s: %w", target, err)
		}
	}

	return nil
}

// writeCompose writes the compose file describing the container about to be
// created.
//
// Nothing reads it yet. It is written here rather than whenever a property
// changes so that it describes the container that exists rather than the one a
// later create would make, and it is rendered from the same resolved values as
// the argv beside it, which is what keeps the two from drifting.
func (s *DefinitionService) writeCompose(serviceName string, scope definition.Scope, configOptions []string, envFile string, idFile string) error {
	environment, err := common.FileToSlice(envFile)
	if err != nil {
		return fmt.Errorf("unable to read the custom environment from %s: %w", envFile, err)
	}

	rendered, err := render.Compose(render.Input{
		Definition:    s.Definition,
		Scope:         scope,
		ConfigOptions: configOptions,
		EnvFile:       envFile,
		IDFile:        idFile,
		Environment:   environment,
	})
	if err != nil {
		return err
	}

	filename := filepath.Join(Folders(s, serviceName).Root, "docker-compose.yml")
	err = common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   string(rendered),
		Filename:  filename,
		GroupName: SystemGroup(),
		Mode:      0644,
		Username:  SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("unable to write %s: %w", filename, err)
	}

	return nil
}

// buildImage builds the definition's image when it has one to build, and reports
// the tag the container should run. A definition that only declares its base is
// left on the pulled image, exactly as every datastore is today.
func (s *DefinitionService) buildImage(ctx context.Context, taggedImage string) (string, error) {
	if !s.Definition.Builds {
		return taggedImage, nil
	}

	base, version := cutTaggedImage(taggedImage)
	input := image.BuildInput{
		Definition:   s.Definition,
		Image:        base,
		ImageVersion: version,
	}

	// rebuilding an image that is already there would put a build in the way of
	// every create, which is the cost the trivial Dockerfile fast path exists
	// to avoid
	if image.Exists(ctx, input.Tag()) {
		return input.Tag(), nil
	}

	return image.Build(ctx, input)
}

// runTaggedImage is the image a service's container runs, which is the built one
// for a definition that builds. It does not build: the create path does that,
// and a verb running against a service that exists has one already.
func (s *DefinitionService) runTaggedImage(serviceName string) string {
	taggedImage := s.taggedImage(serviceName)
	if !s.Definition.Builds {
		return taggedImage
	}

	_, version := cutTaggedImage(taggedImage)
	return image.Tag(s.Definition.Name, version)
}

// PinnedImage reports the image a service pinned rather than the one its
// container runs. The two differ only for a definition that builds, and only
// the exact built tag is unmapped: a container running anything else is
// reported verbatim, so this never hides what is actually there.
func (s *DefinitionService) PinnedImage(serviceName string, running string) string {
	if !s.Definition.Builds || running != s.runTaggedImage(serviceName) {
		return running
	}

	return s.taggedImage(serviceName)
}

// ConnectToService opens an interactive session against a service.
func (s *DefinitionService) ConnectToService(ctx context.Context, input ConnectToServiceInput) error {
	return s.run(ctx, input.ServiceName, "connect", runOptions{
		TTY:    backend.HasTerminal(os.Stdin),
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
}

// ExportService writes a dump of the service's data to a writer.
func (s *DefinitionService) ExportService(ctx context.Context, input ExportServiceInput) error {
	// the dump is streamed to the writer rather than buffered into a string,
	// which is what makes it safe for binary data of any size
	return s.run(ctx, input.ServiceName, "export", runOptions{
		Stdout: input.Writer,
		Stderr: os.Stderr,
	})
}

// ImportService replaces the service's data with what is read from a reader.
func (s *DefinitionService) ImportService(ctx context.Context, input ImportServiceInput) error {
	return s.run(ctx, input.ServiceName, "import", runOptions{
		Stdin:  input.Reader,
		Stderr: os.Stderr,
	})
}

// runOptions are the streams and terminal a verb runs with.
type runOptions struct {
	TTY    bool
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// run executes one of the definition's declared commands.
func (s *DefinitionService) run(ctx context.Context, serviceName string, name string, options runOptions) error {
	return verb.Run(ctx, verb.RunInput{
		Definition: s.Definition,
		Scope:      s.scope(serviceName),
		Name:       name,
		Names: backend.Names{
			Container:  ContainerName(s, serviceName),
			Ambassador: AmbassadorContainerName(s, serviceName),
		},
		Image:   s.runTaggedImage(serviceName),
		Volumes: s.volumes(serviceName),
		TTY:     options.TTY,
		Stdin:   options.Stdin,
		Stdout:  options.Stdout,
		Stderr:  options.Stderr,
	})
}

// volumes are the mounts a throwaway container needs to stand in for the
// service container: the service's own data, and the payload.
//
// The payload is easy to forget here and impossible to miss at runtime: an
// offline verb is one of the mounted scripts, so without it there is nothing to
// exec and import fails with "not found".
func (s *DefinitionService) volumes(serviceName string) []string {
	serviceFolders := Folders(s, serviceName)

	volumes := make([]string, 0, len(s.Definition.Service.Volumes))
	for _, volume := range s.Definition.Service.Volumes {
		source := strings.Replace(volume.Source, definition.HostRootTemplate, serviceFolders.HostRoot, 1)
		volumes = append(volumes, source+":"+volume.Target)
	}

	for _, file := range render.RootfsFiles(render.Input{Definition: s.Definition, Scope: s.scope(serviceName)}) {
		volumes = append(volumes, file.Mount)
	}

	return volumes
}

// Properties projects the definition into the shape the rest of the binary
// reads a datastore's metadata from.
func (s *DefinitionService) Properties() ServiceStruct {
	dokku := s.Definition.Dokku

	ports := make([]int, 0, len(s.Definition.Service.Ports))
	for _, port := range s.Definition.Service.Ports {
		ports = append(ports, port.Target)
	}

	waitPort := 0
	if port, ok := s.Definition.PortFor(dokku.Wait); ok {
		waitPort = port.Target
	} else if port, ok := s.Definition.PrimaryPort(); ok {
		waitPort = port.Target
	}

	return ServiceStruct{
		AltAlias:            dokku.AltAlias,
		CommandPrefix:       dokku.Plugin,
		ConfigSuffix:        "config",
		ConfigVariable:      dokku.Variable + "_CONFIG_OPTIONS",
		DefaultAlias:        dokku.Alias,
		DefaultImage:        s.Definition.DefaultImage,
		DefaultImageVersion: s.Definition.DefaultImageVersion,
		EnvVariable:         dokku.Variable + "_CUSTOM_ENV",
		// derived from the plugin name rather than the variable: graphite's is
		// GRAPHITE_DISABLE_PULL while its variable is STATSD, and the two
		// derivation rules look identical without being it
		ImagePullVariable: strings.ToUpper(dokku.Plugin) + "_DISABLE_PULL",
		PluginVariable:    dokku.Variable,
		Ports:             ports,
		Scheme:            dokku.Scheme,
		WaitPort:          waitPort,
	}
}

// ServiceType returns the type of service.
func (s *DefinitionService) ServiceType() string {
	return s.Definition.Dokku.Plugin
}

// Title returns the service name in title case.
func (s *DefinitionService) Title() string {
	return s.Definition.Dokku.Title
}

// URL returns the url a linked app receives.
func (s *DefinitionService) URL(serviceName string, schemeOverride string) string {
	scope := s.scope(serviceName)
	if schemeOverride != "" {
		scope.Scheme = schemeOverride
	}

	url, err := definition.Render(s.Definition.Dokku.DSN, scope)
	if err != nil {
		return ""
	}

	return url
}

// taggedImage is the image a service runs, which is what it pinned at create
// time and the definition's default otherwise.
func (s *DefinitionService) taggedImage(serviceName string) string {
	serviceFiles := Files(s, serviceName)

	image := common.ReadFirstLine(serviceFiles.Image)
	if image == "" {
		image = s.Definition.DefaultImage
	}

	imageVersion := common.ReadFirstLine(serviceFiles.ImageVersion)
	if imageVersion == "" {
		imageVersion = s.Definition.DefaultImageVersion
	}

	return fmt.Sprintf("%s:%s", image, imageVersion)
}

// scope assembles what the definition's templates are rendered against. Nothing
// in it is computed by a template: it is read from the service's files on disk
// and the datastore's own metadata.
//
// The initial network is deliberately not read here. It is a plugin property
// rather than a file, and looking one up needs a configured dokku, which
// rendering a connection string must not: only the create path sets it.
func (s *DefinitionService) scope(serviceName string) definition.Scope {
	serviceFolders := Folders(s, serviceName)
	serviceFiles := Files(s, serviceName)
	dokku := s.Definition.Dokku

	taggedImage := s.taggedImage(serviceName)
	image, imageVersion := cutTaggedImage(taggedImage)

	secrets := map[string]string{}
	for name, secret := range dokku.Secrets {
		secrets[name] = common.ReadFirstLine(fmt.Sprintf("%s/%s", serviceFolders.Root, secret.File))
	}

	ports := map[string]int{}
	for _, port := range s.Definition.Service.Ports {
		ports[port.Name] = port.Target
	}

	database := common.ReadFirstLine(serviceFiles.DatabaseName)
	if database == "" {
		database = serviceName
	}

	return definition.Scope{
		ServiceName:   serviceName,
		ContainerName: ContainerName(s, serviceName),
		Host:          DNSHostname(s, serviceName),
		Database:      database,
		Plugin:        dokku.Plugin,
		Title:         dokku.Title,
		Variable:      dokku.Variable,
		Image:         image,
		ImageVersion:  imageVersion,
		TaggedImage:   taggedImage,
		ServiceRoot:   serviceFolders.Root,
		HostRoot:      serviceFolders.HostRoot,
		Scheme:        dokku.Scheme,
		Secret:        secrets,
		Port:          ports,
		Memory:        common.ReadFirstLine(serviceFiles.Memory),
		ShmSize:       common.ReadFirstLine(serviceFiles.ShmSize),
	}
}

// cutTaggedImage splits an image reference into its name and tag.
func cutTaggedImage(reference string) (string, string) {
	image, version, found := strings.Cut(reference, ":")
	if !found {
		return image, "latest"
	}

	return image, version
}
