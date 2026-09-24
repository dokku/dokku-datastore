package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dokku/docker-port-forward/portforward"
	"github.com/dokku/dokku-datastore/internal/backend"
	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/execx"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/render"
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
		_, err := execx.Run(ctx, common.ExecCommandInput{
			Command: common.DockerBin(),
			Args:    []string{"network", "connect", "--alias", input.NetworkAlias, network, input.ContainerID},
		})
		if err != nil {
			return fmt.Errorf("failed to connect to network %s: %w", network, err)
		}
	}
	return nil
}

// CommitServiceConfigInput is the input for the CommitServiceConfig function
type CommitServiceConfigInput struct {
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

	// Datastore is the service to commit the service config for
	Datastore *Datastore

	// ServiceName is the name of the service to commit the service config for
	ServiceName string

	// ShmSize is the shared memory size to commit for the service
	ShmSize string

	// InitialNetwork is the initial network to commit for the service
	InitialNetwork string

	// PostCreateNetworks is the networks to attach the service container to after service creation
	PostCreateNetworks []string

	// PostStartNetworks is the networks to attach the service container to after service start
	PostStartNetworks []string

	// LogDriver is the docker logging driver to run the service container with
	LogDriver string

	// LogOptions are the docker log options for the service container
	LogOptions []string

	// RestartPolicy is the docker restart policy for the service container
	RestartPolicy string

	// Mounts are the mounts for the service container beyond the definition's
	Mounts []Mount
}

// CommitServiceConfig commits the service config for a given service
func CommitServiceConfig(input CommitServiceConfigInput) error {
	if input.ServiceName == "" {
		return fmt.Errorf("service name is required")
	}

	serviceFiles := Files(input.Datastore, input.ServiceName)

	// both can carry credentials the user handed over, so they are kept from
	// other users the way the service's own secrets are
	lines := strings.Split(input.CustomEnv, ";")
	if err := ReplaceFileAtomically(serviceFiles.Env, strings.Join(lines, "\n"), PrivateFileMode); err != nil {
		return fmt.Errorf("failed to write env to %s: %w", serviceFiles.Env, err)
	}

	if err := ReplaceFileAtomically(serviceFiles.ConfigOptions, input.ConfigOptions, PrivateFileMode); err != nil {
		return fmt.Errorf("failed to write config options to %s: %w", serviceFiles.ConfigOptions, err)
	}

	err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   strconv.Itoa(input.Memory),
		Filename:  serviceFiles.Memory,
		GroupName: hostenv.SystemGroup(),
		Mode:      0644,
		Username:  hostenv.SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write memory to %s: %w", serviceFiles.Memory, err)
	}

	err = common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   input.ShmSize,
		Filename:  serviceFiles.ShmSize,
		GroupName: hostenv.SystemGroup(),
		Mode:      0644,
		Username:  hostenv.SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write shm size to %s: %w", serviceFiles.ShmSize, err)
	}

	err = RecordImage(RecordImageInput{
		Datastore:    input.Datastore,
		Image:        input.Image,
		ImageVersion: input.ImageVersion,
		ServiceName:  input.ServiceName,
	})
	if err != nil {
		return err
	}

	// recorded beside the version it was resolved from, so that a release adding
	// a newer definition does not move a service that already exists onto it
	if err := PinDefinition(input.Datastore, input.ServiceName); err != nil {
		return err
	}

	properties := input.Datastore.Properties()
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

	err = common.PropertyWrite(properties.CommandPrefix, input.ServiceName, LogDriverProperty, input.LogDriver)
	if err != nil {
		return fmt.Errorf("failed to write %s property: %w", LogDriverProperty, err)
	}

	err = common.PropertyWrite(properties.CommandPrefix, input.ServiceName, LogOptProperty, strings.Join(input.LogOptions, ","))
	if err != nil {
		return fmt.Errorf("failed to write %s property: %w", LogOptProperty, err)
	}

	err = common.PropertyWrite(properties.CommandPrefix, input.ServiceName, RestartPolicyProperty, input.RestartPolicy)
	if err != nil {
		return fmt.Errorf("failed to write %s property: %w", RestartPolicyProperty, err)
	}

	if err := WriteMounts(input.Datastore, input.ServiceName, input.Mounts); err != nil {
		return err
	}

	return nil
}

// RecordImageInput is the input for the RecordImage function
type RecordImageInput struct {
	// Datastore is the datastore the service belongs to
	Datastore *Datastore

	// Image is the image the service runs
	Image string

	// ImageVersion is the version of that image
	ImageVersion string

	// ServiceName is the name of the service to record the image for
	ServiceName string
}

// RecordImage writes the image a service runs.
//
// These two files are what a container rebuilt later is placed by, so they are
// written whenever the image a service runs changes rather than only at create:
// an upgrade that left them alone would come back on the old image the next time
// the container had to be made again.
func RecordImage(input RecordImageInput) error {
	serviceFiles := Files(input.Datastore, input.ServiceName)

	err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   input.Image,
		Filename:  serviceFiles.Image,
		GroupName: hostenv.SystemGroup(),
		Mode:      0644,
		Username:  hostenv.SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write image to %s: %w", serviceFiles.Image, err)
	}

	err = common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   input.ImageVersion,
		Filename:  serviceFiles.ImageVersion,
		GroupName: hostenv.SystemGroup(),
		Mode:      0644,
		Username:  hostenv.SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write image version to %s: %w", serviceFiles.ImageVersion, err)
	}

	return nil
}

// RecoverRecordedImageInput is the input for the RecoverRecordedImage function
type RecoverRecordedImageInput struct {
	// ContainerID is the container to read the image from, looked up when empty
	ContainerID string

	// Datastore is the datastore the service belongs to
	Datastore *Datastore

	// ServiceName is the service whose record is being settled
	ServiceName string
}

// RecoverRecordedImage writes down what a service's container runs when its
// files do not say, and reports the record as it then stands.
//
// This is how a service that predates these files, or that lost one of them,
// gets placed by what it is actually running rather than by whatever the plugin
// now ships. It is called wherever the truth is still available and about to
// stop being: before a container is removed, before a container is made, and
// when a plugin is installed.
//
// A record that is already complete is left alone rather than rewritten. That
// is not only an optimisation: Start runs from the pre-start trigger and dokku
// restores apps in parallel, so two deploys can reach one service at once, and
// a rewrite that truncates before it writes gives a concurrent reader an empty
// file to fall back from.
func RecoverRecordedImage(ctx context.Context, input RecoverRecordedImageInput) (RecordedImage, error) {
	recorded := ReadRecordedImage(input.Datastore, input.ServiceName)
	if recorded.Complete() {
		return recorded, nil
	}

	running := Version(ctx, VersionInput{
		ContainerID: input.ContainerID,
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
	})

	repaired, changed := recoveredImage(recorded, running)
	if !changed {
		return repaired, nil
	}

	if err := writeRecordedImage(input.Datastore, input.ServiceName, repaired); err != nil {
		return recorded, err
	}

	return repaired, nil
}

// recoveredImage decides what a service should record, given what its files say
// and what its container runs, and whether that is a change worth writing.
//
// The record wins wherever it has something to say: a container running
// something other than what the service recorded is the case Start rebuilds,
// not a correction to be written down. Pure, so the decision is pinned by a
// test rather than by a docker daemon.
func recoveredImage(recorded RecordedImage, running string) (RecordedImage, bool) {
	if recorded.Complete() {
		return recorded, false
	}

	image, imageVersion, found := definition.CutImage(running)
	if !found || image == "" || imageVersion == "" {
		return recorded, false
	}

	repaired := recorded
	if repaired.Image == "" {
		repaired.Image = image
	}
	if repaired.ImageVersion == "" {
		repaired.ImageVersion = imageVersion
	}

	return repaired, repaired != recorded
}

// writeRecordedImage writes a repaired record through a temporary file in the
// same directory, so that a reader never sees a half written one.
//
// RecordImage writes in place, which is right where the service is being
// changed by the command doing the writing. A repair happens underneath
// commands that are only passing through, including the trigger that starts a
// service while an app deploys, so here the swap is atomic.
func writeRecordedImage(s *Datastore, serviceName string, recorded RecordedImage) error {
	serviceFiles := Files(s, serviceName)

	files := []struct {
		filename string
		content  string
	}{
		{filename: serviceFiles.Image, content: recorded.Image},
		{filename: serviceFiles.ImageVersion, content: recorded.ImageVersion},
	}

	for _, file := range files {
		if file.content == "" {
			continue
		}

		if err := ReplaceFileAtomically(file.filename, file.content, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", file.filename, err)
		}
	}

	return nil
}

// PrivateFileMode is the mode of a service file holding something other users
// on the host must not read: a secret, a credential, or a file either can end
// up in. Only the dokku user and group read these.
const PrivateFileMode os.FileMode = 0640

// ReplaceFileAtomically writes a service file by renaming a temporary one over
// it. The temporary file is made in the same directory so the rename stays
// within one filesystem, and it is removed on every path that does not rename
// it away.
//
// The temporary file is created readable by its owner alone and only given its
// mode once written, so contents meant for PrivateFileMode are never readable
// by anyone else, even when the file being replaced was.
func ReplaceFileAtomically(filename string, content string, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(filename), "."+filepath.Base(filename)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())

	if _, err := temporary.WriteString(content); err != nil {
		temporary.Close()
		return err
	}

	if err := temporary.Close(); err != nil {
		return err
	}

	if err := common.SetPermissions(common.SetPermissionInput{
		Filename:  temporary.Name(),
		GroupName: hostenv.SystemGroup(),
		Mode:      mode,
		Username:  hostenv.SystemUser(),
	}); err != nil {
		return err
	}

	return os.Rename(temporary.Name(), filename)
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

// GenerateRandomPorts generates random ports
func GenerateRandomPorts(iterations int) ([]int, error) {
	var ports []int
	for i := 0; i < iterations; i++ {
		port := GetAvailablePort()
		if port == 0 {
			return nil, fmt.Errorf("failed to get available port")
		}
		ports = append(ports, port)
	}
	return ports, nil
}

// GetAvailablePort gets an available port
func GetAvailablePort() int {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0
	}

	for {
		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			return 0
		}
		defer l.Close()

		port := l.Addr().(*net.TCPAddr).Port
		if port >= 1025 && port <= 65535 {
			return port
		}
	}
}

// RecordedImage is what a service's files say it runs, with no fallback of any
// kind applied.
//
// The halves are separate because a service can have recorded one and not the
// other, and half a record places a container as surely as none of it does: a
// service that kept its IMAGE and lost its IMAGE_VERSION would take the
// definition's current version for the missing half, which is the drift these
// files exist to prevent.
type RecordedImage struct {
	// Image is the image the service recorded, without its tag
	Image string

	// ImageVersion is the tag the service recorded
	ImageVersion string
}

// Complete reports whether the service recorded both halves, which is what it
// takes to place a container without inventing anything.
func (r RecordedImage) Complete() bool {
	return r.Image != "" && r.ImageVersion != ""
}

// Tagged is the image reference the record names.
func (r RecordedImage) Tagged() string {
	return fmt.Sprintf("%s:%s", r.Image, r.ImageVersion)
}

// ReadRecordedImage reads what a service recorded. Nothing is substituted: an
// empty field means the service said nothing, which is the one thing a caller
// placing a container has to be able to tell apart from a version.
//
// Nothing is written either. A record is not repaired on read, for the reason
// PinDefinition gives for not writing the definition pin on read: every read
// path would then need to be able to write. RecoverRecordedImage is the repair,
// and it is called from the paths that are allowed to write.
func ReadRecordedImage(s *Datastore, serviceName string) RecordedImage {
	serviceFiles := Files(s, serviceName)

	return RecordedImage{
		Image:        common.ReadFirstLine(serviceFiles.Image),
		ImageVersion: common.ReadFirstLine(serviceFiles.ImageVersion),
	}
}

// ErrNoImageVersion is what a reference that cannot be settled is, underneath
// the message naming the image it could not be settled for.
var ErrNoImageVersion = errors.New("no image version")

// noImageVersionMessage is the operator-facing wording, which unwraps to the
// sentinel. Wrapping would print the sentinel's own text in front of it, and
// what this says is already what an operator has to read - the same reason
// pullDisabledMessage is shaped this way.
type noImageVersionMessage string

func (message noImageVersionMessage) Error() string { return string(message) }

func (message noImageVersionMessage) Unwrap() error { return ErrNoImageVersion }

// NoImageVersionError names the image that has no version to fall back on, and
// the flag that gives it one. Separate from resolveImage so the wording is
// pinned by a test, and shared with upgradeVersion's refusal so a service being
// moved onto such an image is told the same thing as one already on it.
func NoImageVersionError(image string, plugin string) error {
	return noImageVersionMessage(fmt.Sprintf("%s is not the image the %s definition ships, so it has no version to fall back on; name one with --image-version",
		image, plugin))
}

// resolveImage is the single decision about which image a service runs: what it
// recorded, then any override the caller was given, then the definition's
// default for a half it still does not have.
//
// One function rather than two, because there were two and they disagreed. The
// other read the files with os.ReadFile and trimmed the whole thing, so a file
// with a second line produced a reference with a newline in the middle of it
// that docker cannot resolve, while this one takes the first line the way every
// other file in a service root is read.
//
// The definition's image and the definition's version are one pair rather than
// two halves to be drawn on separately. A version belongs to the repository that
// published it, so pasting the definition's onto another repository names a tag
// nobody ever built - and the command then fails saying that image is missing,
// which is a fair description of a reference this invented. So a version is
// substituted only for the definition's own image, and where it cannot be the
// caller is told which image has no version rather than handed a made up one.
//
// What could be settled comes back alongside the refusal, because a caller about
// to make a container has to stop where one that is only reporting on a service
// still needs something to say.
func resolveImage(s *Datastore, serviceName string, imageOverride string, imageVersionOverride string) (RecordedImage, error) {
	recorded := ReadRecordedImage(s, serviceName)

	settled := RecordedImage{
		Image:        recorded.Image,
		ImageVersion: recorded.ImageVersion,
	}

	if imageOverride != "" {
		settled.Image = imageOverride
	}
	if settled.Image == "" {
		settled.Image = s.Definition.DefaultImage
	}

	if imageVersionOverride != "" {
		settled.ImageVersion = imageVersionOverride
	}
	if settled.ImageVersion != "" {
		return settled, nil
	}

	// a service created before the IMAGE file existed recorded only its version,
	// and the definition's image is the one it has been running all along. That
	// is the half this fallback is for, and it is why the pair is only refused
	// the other way around
	if settled.Image == s.Definition.DefaultImage {
		settled.ImageVersion = s.Definition.DefaultImageVersion
		return settled, nil
	}

	return settled, NoImageVersionError(settled.Image, s.Definition.Dokku.Plugin)
}

// ImageForServiceInput is the input for the ImageForService function
type ImageForServiceInput struct {
	// Datastore is the service to get the image for
	Datastore *Datastore

	// ServiceName is the name of the service to get the image for
	ServiceName string

	// ImageOverride is the image to use for the service
	ImageOverride string

	// ImageVersionOverride is the image version to use for the service
	ImageVersionOverride string
}

// ImageForService is the reference create and upgrade place a container by.
//
// Both are about to fetch and run what comes back, so a version that could not
// be settled is refused here rather than filled in from the definition. Start
// does not come through this at all: it runs what the service recorded, and
// refuses on its own account when that record is half there.
func ImageForService(input ImageForServiceInput) (string, error) {
	if input.ServiceName == "" {
		return "", fmt.Errorf("service name is required")
	}

	settled, err := resolveImage(input.Datastore, input.ServiceName, input.ImageOverride, input.ImageVersionOverride)
	if err != nil {
		return "", err
	}

	return settled.Tagged(), nil
}

// pullTaggedImage pulls a tagged image
func pullTaggedImage(ctx context.Context, taggedImage string) (bool, error) {
	result, err := execx.Run(ctx, common.ExecCommandInput{
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

// EnsureTaggedImageInput is the input for the EnsureTaggedImage function
type EnsureTaggedImageInput struct {
	// Action names what could not be done, for the message a disabled pull
	// leaves behind: "creation", "upgrade" or "start"
	Action string

	// Datastore is the datastore the service belongs to
	Datastore *Datastore

	// ServiceName is the service the image is being fetched for
	ServiceName string

	// TaggedImage is the reference the host has to have
	TaggedImage string
}

// EnsureTaggedImage makes sure the host has an image, pulling it when it does
// not and explaining what to run by hand when pulling is turned off.
//
// One copy rather than three. Create and upgrade each carried this block
// verbatim, differing only in the word they put in the failure line, and start
// carried none of it - so a service pinned to a tag the host had pruned could
// not be started at all, and the only way back was an upgrade onto a version
// nobody asked for.
//
// Everything the plugin runs comes through here now, including the images it
// runs beside a service, and the two halves it is made of are unexported so
// that a call site cannot take the existence check without the pull or the pull
// without the permission to make it.
func EnsureTaggedImage(ctx context.Context, input EnsureTaggedImageInput) error {
	if err := validateTaggedImageExists(input.TaggedImage); err == nil {
		return nil
	}

	pullVariable := input.Datastore.Properties().ImagePullVariable
	if os.Getenv(pullVariable) == "true" {
		return pullDisabledError(pullVariable, input.TaggedImage, input.ServiceName, input.Action)
	}

	if _, err := pullTaggedImage(ctx, input.TaggedImage); err != nil {
		return fmt.Errorf("failed to pull image %s: %w", input.TaggedImage, err)
	}

	return nil
}

// ErrPullDisabled is what a disabled pull is, underneath the message that says
// which image and which command. Install is the one caller that carries on past
// it: it fetches every image a plugin will ever need up front, and an operator
// who has turned pulling off means to fetch them by hand rather than to have the
// install fail.
var ErrPullDisabled = errors.New("pulling is disabled")

// pullDisabledMessage is the operator-facing wording, which unwraps to the
// sentinel. A wrapped error would print the sentinel's own text alongside it,
// and what this says is already what an operator has to read.
type pullDisabledMessage string

func (message pullDisabledMessage) Error() string { return string(message) }

func (message pullDisabledMessage) Unwrap() error { return ErrPullDisabled }

// pullDisabledError is what a caller is told when the host has neither the
// image nor permission to fetch it. Separate from EnsureTaggedImage so the
// wording can be pinned by a test that needs no docker daemon.
//
// The last line names a service and what could not be done to it, and is left
// out where there is neither: install fetches what the plugin needs before any
// service exists.
func pullDisabledError(pullVariable string, taggedImage string, serviceName string, action string) error {
	message := []string{
		fmt.Sprintf("%s environment variable detected. Not running pull command.", pullVariable),
		fmt.Sprintf("docker image pull %s", taggedImage),
	}

	if serviceName != "" {
		message = append(message, fmt.Sprintf("%s service %s failed", serviceName, action))
	}

	return pullDisabledMessage(strings.Join(message, "\n"))
}

// FilterServicesInput is the input for the FilterServices function
type FilterServicesInput struct {
	// Datastore is the service to filter services for
	Datastore *Datastore

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

	pluginCommandPrefix := input.Datastore.Properties().CommandPrefix
	results, err := execx.PlugnTrigger(ctx, common.PlugnTriggerInput{
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

// ServicePortReconcileStatusInput is the input for the ServicePortReconcileStatus function
type ServicePortReconcileStatusInput struct {
	// Datastore is the service to reconcile the port for
	Datastore *Datastore

	// ServiceName is the name of the service to reconcile the port for
	ServiceName string
}

// AmbassadorContainerIDLabel is the label an ambassador carries naming the
// service container it was made to front.
const AmbassadorContainerIDLabel = "dokku.ambassador.container-id"

// AmbassadorHalfCloseTimeout is how long an ambassador keeps a connection open
// after one side stops sending. It is the value the ambassador image used to
// run socat with, so a client that half-closes and then waits for its reply is
// not cut off after socat's own half second.
const AmbassadorHalfCloseTimeout = 100000000 * time.Second

// ambassadorAction is what ServicePortReconcileStatus does with a service's
// ambassador.
type ambassadorAction int

const (
	// ambassadorNone leaves things be: the service is not exposed and has no
	// ambassador
	ambassadorNone ambassadorAction = iota

	// ambassadorRemove takes away an ambassador a service that is not exposed
	// was left with
	ambassadorRemove

	// ambassadorKeep leaves a running ambassador that fronts the service's
	// current container alone
	ambassadorKeep

	// ambassadorCreate makes one for an exposed service that has none
	ambassadorCreate

	// ambassadorReplace takes away one that cannot be trusted to publish the
	// service, and makes another in its place
	ambassadorReplace
)

// ambassadorState is what is known about a service and its ambassador when
// deciding what to do with the ambassador.
type ambassadorState struct {
	// Exposed is whether the service has ports to publish
	Exposed bool

	// Status is the ambassador container's status, "missing" when there is none
	Status string

	// Managed is whether the ambassador was made by docker-port-forward. One
	// that was not is an older ambassador that reaches the service through a
	// legacy link
	Managed bool

	// Stale is whether docker-port-forward says the ambassador can no longer
	// reach the service
	Stale bool

	// FrontedID is the service container the ambassador says it fronts
	FrontedID string

	// ServiceID is the service container there is now
	ServiceID string
}

// actionForAmbassador maps the state of a service's ambassador onto what
// ServicePortReconcileStatus does with it.
//
// An ambassador is only ever kept when it is running, was made by
// docker-port-forward, fronts the container the service has now, and can still
// reach it. It holds no state, so anything else is replaced rather than
// started. A stopped one may front a container that has since been removed and
// made again. One made before the label existed says nothing about what it
// fronts. One made with a legacy link cannot find the service at all on docker
// 29, which no longer hands a linked container the environment it reads its
// target from. And one that dials the service by an address the service no
// longer has publishes nothing.
func actionForAmbassador(state ambassadorState) ambassadorAction {
	if !state.Exposed {
		if state.Status == "missing" {
			return ambassadorNone
		}

		return ambassadorRemove
	}

	if state.Status == "missing" {
		return ambassadorCreate
	}

	if state.Status == "running" && state.Managed && !state.Stale && state.FrontedID != "" && state.FrontedID == state.ServiceID {
		return ambassadorKeep
	}

	return ambassadorReplace
}

// ambassadorForwardOptionsInput is the input for ambassadorForwardOptions.
type ambassadorForwardOptionsInput struct {
	// AmbassadorName is the name of the ambassador container
	AmbassadorName string

	// CommandPrefix is the datastore's command prefix
	CommandPrefix string

	// ContainerID is the service container the ambassador fronts
	ContainerID string

	// ContainerPorts are the ports the service listens on
	ContainerPorts []int

	// HostPorts are the host ports each container port is published on, in the
	// same order. Each is a port, or an ip:port that publishes it on that
	// address alone
	HostPorts []string

	// Image is the ambassador image
	Image string

	// LogConfig is the logging the service container was given
	LogConfig LogConfig

	// RestartPolicy is the restart policy the service was given, empty for the
	// default. The ambassador takes the same one: left restarting forever
	// beside a service told not to, it would publish a port with nothing behind
	// it. The default is applied rather than passed on empty, because
	// docker-port-forward reads an empty policy as unless-stopped
	RestartPolicy string
}

// ambassadorForwardOptions builds the docker-port-forward options that run a
// service's ambassador.
func ambassadorForwardOptions(input ambassadorForwardOptionsInput) portforward.Options {
	// the port file holds either a port or an ip:port, both of which are the
	// front half of a docker style port spec. A port with no address of its own
	// is published on every interface, as a plain --publish was
	ports := make([]string, 0, len(input.HostPorts))
	for i, hostPort := range input.HostPorts {
		ports = append(ports, fmt.Sprintf("%s:%d", hostPort, input.ContainerPorts[i]))
	}

	return portforward.Options{
		Target:              "container/" + input.ContainerID,
		Ports:               ports,
		Addresses:           []string{portforward.AllInterfaces},
		Detach:              true,
		RestartPolicy:       render.RestartPolicy(input.RestartPolicy),
		Name:                input.AmbassadorName,
		HelperImage:         input.Image,
		Pull:                portforward.PullNever,
		TCPHalfCloseTimeout: AmbassadorHalfCloseTimeout,

		// the host port is left for docker to claim, as it always was. The check
		// runs as the dokku user, which cannot bind a port below 1024 that the
		// daemon publishes without trouble
		SkipPreflight: true,

		// the same cap the service it fronts is given. It is the only other
		// container a service leaves running, so an unbounded log here is the
		// same bug in a smaller container
		LogDriver: input.LogConfig.Driver,
		LogOpts:   input.LogConfig.Options,

		Labels: map[string]string{
			"dokku":                    "ambassador",
			"dokku.ambassador":         input.CommandPrefix,
			AmbassadorContainerIDLabel: input.ContainerID,
		},
	}
}

// inspectAmbassador reports whether an ambassador was made by
// docker-port-forward and, if so, whether it can still reach its service.
func inspectAmbassador(ctx context.Context, ambassadorName string) (managed bool, stale bool, err error) {
	helpers, err := portforward.List(ctx, portforward.ListOptions{Name: ambassadorName})
	if errors.Is(err, portforward.ErrNotHelper) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("failed to inspect container %s: %w", ambassadorName, err)
	}
	if len(helpers) == 0 {
		return false, false, nil
	}

	return true, helpers[0].Stale, nil
}

// ServicePortReconcileStatus makes a service's ambassador match whether the
// service is exposed: one that fronts the service's current container when it
// is, and none when it is not.
func ServicePortReconcileStatus(ctx context.Context, input ServicePortReconcileStatusInput) error {
	serviceProperties := input.Datastore.Properties()
	portFile := Files(input.Datastore, input.ServiceName).Port
	ambassadorName := AmbassadorContainerName(input.Datastore, input.ServiceName)

	state := ambassadorState{
		Exposed: common.FileExists(portFile) && common.ReadFirstLine(portFile) != "",
		Status:  backend.Status(ctx, ambassadorName),
		ServiceID: LiveContainerID(ctx, LiveContainerIDInput{
			Datastore:   input.Datastore,
			ServiceName: input.ServiceName,
		}),
	}
	if state.Exposed && state.Status != "missing" {
		state.FrontedID, _ = common.DockerInspect(ambassadorName, fmt.Sprintf("{{ index .Config.Labels %q }}", AmbassadorContainerIDLabel))

		var err error
		state.Managed, state.Stale, err = inspectAmbassador(ctx, ambassadorName)
		if err != nil {
			return err
		}
	}

	switch actionForAmbassador(state) {
	case ambassadorNone, ambassadorKeep:
		return nil
	case ambassadorRemove:
		return RemoveAmbassadorContainer(ctx, input.Datastore, input.ServiceName)
	}

	hostPorts := ExposedHostPorts(input.Datastore, input.ServiceName)
	if len(hostPorts) != len(serviceProperties.Ports) {
		return fmt.Errorf("port file %s holds %d ports, expected %d", portFile, len(hostPorts), len(serviceProperties.Ports))
	}

	// checked here rather than left to docker-port-forward, which refuses a
	// target that is not running with an error that names the container rather
	// than the service that is actually down
	if state.ServiceID == "" || backend.Status(ctx, state.ServiceID) != "running" {
		return fmt.Errorf("unable to publish ports for %s: its container is not running", input.ServiceName)
	}

	// only a container that is about to be made needs the image. The
	// ambassador is pinned and fetched when the plugin is installed, which is
	// no help on a host that has been pruned since. Fetched before an
	// ambassador is taken away, so a pull that fails leaves the one there was
	if err := EnsureTaggedImage(ctx, EnsureTaggedImageInput{
		Action:      "port publishing",
		Datastore:   input.Datastore,
		ServiceName: input.ServiceName,
		TaggedImage: hostenv.AmbassadorImage,
	}); err != nil {
		return err
	}

	logConfig, err := ServiceLogConfig(ctx, input.Datastore, input.ServiceName)
	if err != nil {
		return err
	}

	if state.Status != "missing" {
		if err := RemoveAmbassadorContainer(ctx, input.Datastore, input.ServiceName); err != nil {
			return err
		}
	}

	result, err := portforward.Forward(ctx, ambassadorForwardOptions(ambassadorForwardOptionsInput{
		AmbassadorName: ambassadorName,
		CommandPrefix:  serviceProperties.CommandPrefix,
		ContainerID:    state.ServiceID,
		ContainerPorts: serviceProperties.Ports,
		HostPorts:      hostPorts,
		Image:          hostenv.AmbassadorImage,
		LogConfig:      logConfig,
		RestartPolicy:  ServiceRestartPolicy(input.Datastore, input.ServiceName),
	}))
	if err != nil {
		return fmt.Errorf("failed to run container %s: %w", ambassadorName, err)
	}

	// docker-port-forward hands back a helper that already covers the ports
	// rather than making another. One that is not the ambassador is something
	// else publishing the service, which the ambassador would be fighting for
	// the same host ports
	if result.Existing && result.HelperName != ambassadorName {
		return fmt.Errorf("unable to publish ports for %s: they are already forwarded by container %s", input.ServiceName, result.HelperName)
	}

	return nil
}

// MissingServiceNameMessage is the message emitted when a service name is not
// specified. It matches the message the bash datastore plugins emit.
const MissingServiceNameMessage = "Please specify a valid name for the service"

// validServiceNamePattern is every character a service name may use. The
// validation and the message that explains it are both built from it, so the
// message cannot leave out a character the validation accepts.
const validServiceNamePattern = "[A-Za-z0-9_-]+"

// InvalidServiceNameMessage is the message emitted when a service name contains
// unsupported characters. It follows the message the bash datastore plugins
// emit, except that it names the dash they accepted but left out of it.
const InvalidServiceNameMessage = MissingServiceNameMessage + ". Valid characters are: " + validServiceNamePattern

// MissingAppNameMessage is the message emitted when an app name is not
// specified. It matches the message the bash datastore plugins emit.
const MissingAppNameMessage = "Please specify an app to run the command on"

// ErrMissingAppName is returned when an app name is not specified
var ErrMissingAppName = errors.New(MissingAppNameMessage)

// ErrMissingServiceName is returned when a service name is not specified
var ErrMissingServiceName = errors.New(MissingServiceNameMessage)

// ErrInvalidServiceName is returned when a service name contains unsupported characters
var ErrInvalidServiceName = errors.New(InvalidServiceNameMessage)

// ValidateServiceName validates a service name
func ValidateServiceName(serviceName string) error {
	if serviceName == "" {
		return ErrMissingServiceName
	}

	if !regexp.MustCompile("^" + validServiceNamePattern + "$").MatchString(serviceName) {
		return ErrInvalidServiceName
	}

	return nil
}

// validateTaggedImageExists checks if the image exists
func validateTaggedImageExists(taggedImage string) error {
	if common.VerifyImage(taggedImage) {
		return nil
	}

	return fmt.Errorf("image %s does not exist", taggedImage)
}

type WriteDatabaseNameInput struct {
	// Datastore is the datastore to write the database name to
	Datastore *Datastore

	// ServiceName is the name of the service to write the database name to
	ServiceName string
}

// WriteDatabaseName writes the database name to the service
func WriteDatabaseName(input WriteDatabaseNameInput) error {
	// some datastores refuse special characters in a database name, so they are
	// normalised out the way the bash plugins' write_database_name did
	sanitizedDatabaseName := strings.ReplaceAll(input.ServiceName, ".", "_")
	sanitizedDatabaseName = strings.ReplaceAll(sanitizedDatabaseName, "-", "_")
	return writeDatabaseNameFile(input.Datastore, input.ServiceName, sanitizedDatabaseName)
}

// DatabaseName reads the database name a service recorded. A service with no
// record is given one, the way the bash plugins' get_database_name did: the
// service name as it is, unsanitized, since that is the database a service
// made before the name was recorded was created with.
func DatabaseName(s *Datastore, serviceName string) string {
	serviceFiles := Files(s, serviceName)
	if common.FileExists(serviceFiles.DatabaseName) {
		if database := common.ReadFirstLine(serviceFiles.DatabaseName); database != "" {
			return database
		}

		return serviceName
	}

	// only for a service that exists, so that reading a name never makes a
	// service root for one that does not. A record that cannot be written still
	// leaves the name it would have held, which is what the caller needs
	if common.DirectoryExists(Folders(s, serviceName).Root) {
		_ = writeDatabaseNameFile(s, serviceName, serviceName)
	}

	return serviceName
}

func writeDatabaseNameFile(s *Datastore, serviceName string, databaseName string) error {
	serviceFiles := Files(s, serviceName)
	err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   databaseName,
		Filename:  serviceFiles.DatabaseName,
		GroupName: hostenv.SystemGroup(),
		Mode:      0644,
		Username:  hostenv.SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write database name to %s: %w", serviceFiles.DatabaseName, err)
	}

	return nil
}
