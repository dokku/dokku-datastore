package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dokku/dokku-datastore/internal/backend"
	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/hostenv"
	"github.com/dokku/dokku-datastore/internal/registry"

	"github.com/dokku/dokku/plugins/common"
)

// ForService returns the datastore as one of its services runs it.
//
// A datastore split by major version has more than one definition, and which one
// a service was created with is not a presentation detail: postgres-17 mounts its
// data at /var/lib/postgresql/data and postgres-18 at /var/lib/postgresql. A
// service that changed definition under an upgrade of the plugin would come back
// pointed at an empty directory with its data still on disk beside it.
//
// So the definition is read from the service rather than decided for it. A
// datastore with a single definition resolves to that definition whatever any of
// this says, which is every datastore but postgres, solr and elasticsearch.
//
// A service naming a definition this binary does not have is reported rather than
// quietly placed on another one. That happens when a plugin ships its own
// definitions and drops one a service is still pinned to, and running such a
// service on the newest instead is the exact failure the pin exists to prevent.
// The datastore that comes back is still usable, because the commands that
// inspect a service and the command that removes one have to work on a service
// that cannot be run; every other caller treats the error as fatal.
func (s *Datastore) ForService(serviceName string) (*Datastore, error) {
	if s == nil || s.registry == nil || serviceName == "" {
		return s, nil
	}

	serviceFiles := Files(s, serviceName)

	// the pin is the whole point: a service keeps what it was created with even
	// once the datastore has a newer definition
	pinned := common.ReadFirstLine(serviceFiles.Definition)
	if pinned != "" {
		if found, ok := s.registry.Definition(pinned); ok {
			return s.withDefinition(found), nil
		}

		return s.ForImage(common.ReadFirstLine(serviceFiles.Image), common.ReadFirstLine(serviceFiles.ImageVersion)),
			fmt.Errorf("service %s runs the %s definition, which this %s plugin does not ship",
				serviceName, pinned, s.Definition.Dokku.Plugin)
	}

	// a service created before the pin existed is placed by the image it
	// recorded, which is what the pin would have held
	return s.ForImage(common.ReadFirstLine(serviceFiles.Image), common.ReadFirstLine(serviceFiles.ImageVersion)), nil
}

// MisplacedPin returns the datastore as a service pinned to the wrong major of
// its own image belongs on, along with the definition it is pinned to, and
// false for a service whose pin is to be kept.
//
// Before postgres had definitions for fourteen, fifteen and sixteen, a service
// running postgres:15.7 resolved to the newest definition and was pinned to it
// at the next install. That definition mounts its data where postgres 18 keeps
// it, so the server came up on an empty directory with its cluster beside it.
//
// Only a pin that is wrong on its face is moved: the service records the image
// the pinned definition ships, and its version names, outright, the major of
// another definition shipping that image. A service placed with --definition on
// an image of its own, on a flavor, or on a version naming no major keeps its
// pin, since nothing it recorded says the pin is wrong. A pin to a definition
// this plugin does not ship is left for ForService to report.
//
// Where the two definitions mount a volume in different places, the service's
// container has to be the evidence as well, mounting each of those volumes
// where the definition it is moved onto would. A service adopted from the bash
// plugin still runs the container that plugin made, which mounts its data
// where its own major keeps it. One created on this binary with postgres:15.7
// was placed on the newest definition from the start, so its server made its
// cluster under that definition's mount, and moving the pin would hide it. So
// would moving the pin of a service whose container is gone, which says
// nothing either way, and upgrade --no-migrate is left to settle those.
func (s *Datastore) MisplacedPin(ctx context.Context, serviceName string) (*Datastore, string, bool) {
	corrected, pinned, ok := s.misplacedPin(serviceName)
	if !ok {
		return s, pinned, false
	}

	current, _ := s.registry.Definition(pinned)
	mounts := func() map[string]string {
		containerID := LiveContainerID(ctx, LiveContainerIDInput{Datastore: s, ServiceName: serviceName})
		return backend.Mounts(ctx, containerID)
	}
	if !mountsAgree(current, corrected.Definition, scopeVolumeTargets(s, serviceName), Folders(s, serviceName).HostRoot, mounts) {
		return s, pinned, false
	}

	return corrected, pinned, true
}

// mountsAgree reports whether a container mounts every volume two definitions
// mount differently where the second of them would, which is what says its
// data is where that definition looks for it. Two definitions that mount every
// volume in the same place need nothing from the container, since a service
// moved between them keeps its data where it was, and the container is only
// asked where they do not.
func mountsAgree(pinned definition.Definition, corrected definition.Definition, overrides map[string]string, hostRoot string, mounts func() map[string]string) bool {
	from := pinned.VolumeTargets(overrides)
	var mounted map[string]string
	for key, target := range corrected.VolumeTargets(overrides) {
		if from[key] == target {
			continue
		}

		if mounted == nil {
			mounted = mounts()
		}

		if mounted[filepath.Join(hostRoot, key)] != target {
			return false
		}
	}

	return true
}

// misplacedPin is MisplacedPin without asking the container, which is all
// that reads the service's own files.
func (s *Datastore) misplacedPin(serviceName string) (*Datastore, string, bool) {
	if s == nil || s.registry == nil || serviceName == "" {
		return s, "", false
	}

	serviceFiles := Files(s, serviceName)
	pinned := common.ReadFirstLine(serviceFiles.Definition)
	if pinned == "" {
		return s, "", false
	}

	current, ok := s.registry.Definition(pinned)
	if !ok {
		return s, pinned, false
	}

	// a service that never recorded its image runs the one its definition ships
	image := common.ReadFirstLine(serviceFiles.Image)
	if image == "" {
		image = current.DefaultImage
	}

	if !registry.SameRepository(current.DefaultImage, image) {
		return s, pinned, false
	}

	found, ok := s.registry.ForImageMajor(s.Definition.Dokku.Plugin, image, common.ReadFirstLine(serviceFiles.ImageVersion))
	if !ok || found.Name == pinned {
		return s, pinned, false
	}

	return s.withDefinition(found), pinned, true
}

// ForImage returns the datastore as a service on a given image and version runs
// it: the image picks the flavor, such as pgvector/pgvector, and the version the
// major within it. Create has no service to read a pin from yet, and upgrade is
// moving one from under its old pin.
func (s *Datastore) ForImage(image string, imageVersion string) *Datastore {
	if s == nil || s.registry == nil || (image == "" && imageVersion == "") {
		return s
	}

	found, err := s.registry.ForImage(s.Definition.Dokku.Plugin, image, imageVersion)
	if err != nil {
		return s
	}

	return s.withDefinition(found)
}

// WithDefinitionNamed returns the datastore as a service placed on a definition
// by name runs it, whatever its image and version would resolve to.
//
// An image's tags do not always say which major version they are, and a custom
// build of postgres 17 tagged custom-3 would otherwise be placed on the newest
// definition and handed postgres 18's data directory. A definition belonging to
// another datastore is refused rather than borrowed, since the commands, the
// triggers and the service root are all this datastore's.
func (s *Datastore) WithDefinitionNamed(name string) (*Datastore, error) {
	if s == nil {
		return s, fmt.Errorf("definition %s cannot be used without a datastore", name)
	}

	plugin := s.Definition.Dokku.Plugin
	names := []string{s.Definition.Name}
	if s.registry != nil {
		names = s.registry.NamesFor(plugin)
		if found, ok := s.registry.Definition(name); ok && found.Dokku.Plugin == plugin {
			return s.withDefinition(found), nil
		}
	} else if name == s.Definition.Name {
		return s, nil
	}

	return s, fmt.Errorf("definition %s is not a %s definition; choose one of: %s",
		name, plugin, strings.Join(names, ", "))
}

// withDefinition returns the datastore running a different definition, and the
// datastore itself when it already runs that one. The copy is shallow on purpose:
// everything else on a datastore is shared, and the registry most of all.
func (s *Datastore) withDefinition(found definition.Definition) *Datastore {
	if found.Name == s.Definition.Name {
		return s
	}

	copied := *s
	copied.Definition = found

	return &copied
}

// Definitions is every definition the datastore is made of, oldest first.
//
// What a datastore can do is not a property of the newest of its definitions. A
// service may be running any of them, so anything answered for the plugin rather
// than for one of its services - the commands it offers, the triggers it handles,
// the privileged scripts it installs - is the union of all of them.
func (s *Datastore) Definitions() []definition.Definition {
	if s == nil {
		return nil
	}

	if s.registry == nil {
		return []definition.Definition{s.Definition}
	}

	names := s.registry.NamesFor(s.Definition.Dokku.Plugin)
	found := make([]definition.Definition, 0, len(names))
	for _, name := range names {
		if one, ok := s.registry.Definition(name); ok {
			found = append(found, one)
		}
	}

	return found
}

// CustomCommands is every command the datastore adds for itself, keyed by the
// name it was declared under. Where two definitions declare the same name the
// newest wins, which is the one a plugin generating its files is documenting.
func (s *Datastore) CustomCommands() map[string]definition.Command {
	declared := map[string]definition.Command{}
	for _, found := range s.Definitions() {
		for name, command := range found.Dokku.CustomCommands {
			declared[name] = command
		}
	}

	return declared
}

// Documentation is every readme section the datastore adds, in the order each
// title is first declared. Where two definitions declare the same title the
// newest wins, as with its custom commands.
func (s *Datastore) Documentation() []definition.DocumentationSection {
	sections := []definition.DocumentationSection{}
	index := map[string]int{}
	for _, found := range s.Definitions() {
		for _, section := range found.Dokku.Documentation {
			if i, ok := index[section.Title]; ok {
				sections[i] = section
				continue
			}

			index[section.Title] = len(sections)
			sections = append(sections, section)
		}
	}

	return sections
}

// TriggerNames is every dokku trigger the datastore handles, sorted, so that
// what a plugin ships is the same on every generation.
func (s *Datastore) TriggerNames() []string {
	seen := map[string]bool{}
	names := []string{}
	for _, found := range s.Definitions() {
		for _, name := range found.TriggerNames() {
			if seen[name] {
				continue
			}

			seen[name] = true
			names = append(names, name)
		}
	}

	sort.Strings(names)

	return names
}

// ImplementsCustom reports whether the datastore adds a command by this name.
// Asked before a service is named, for the same reason Implements is.
func (s *Datastore) ImplementsCustom(name string) bool {
	_, ok := s.CustomCommands()[name]
	return ok
}

// HandlesTrigger reports whether the datastore declares anything for a dokku
// trigger. Which of its services that applies to is settled afterwards, when
// each is resolved to the definition it runs.
func (s *Datastore) HandlesTrigger(name string) bool {
	for _, found := range s.Definitions() {
		if _, ok := found.TriggerFor(name); ok {
			return true
		}
	}

	return false
}

// DefinitionName reports which definition the datastore is running.
func (s *Datastore) DefinitionName() string {
	return s.Definition.Name
}

// PinDefinition records which definition a service runs, so that a later release
// adding a newer one does not move the service onto it.
//
// Written whenever the definition a service runs is settled: at create, and again
// at an upgrade that crosses a major version. It is not written on read, because
// every read path would then need to be able to write.
func PinDefinition(s *Datastore, serviceName string) error {
	filename := Files(s, serviceName).Definition
	if common.ReadFirstLine(filename) == s.Definition.Name {
		return nil
	}

	err := common.WriteStringToFile(common.WriteStringToFileInput{
		Content:   s.Definition.Name,
		Filename:  filename,
		GroupName: hostenv.SystemGroup(),
		Mode:      0644,
		Username:  hostenv.SystemUser(),
	})
	if err != nil {
		return fmt.Errorf("failed to write definition to %s: %w", filename, err)
	}

	return nil
}
