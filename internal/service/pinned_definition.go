package service

import (
	"fmt"
	"sort"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/hostenv"

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
// this says, which is every datastore but postgres and solr.
func (s *Datastore) ForService(serviceName string) *Datastore {
	if s == nil || s.registry == nil || serviceName == "" {
		return s
	}

	serviceFiles := Files(s, serviceName)

	// the pin is the whole point: a service keeps what it was created with even
	// once the datastore has a newer definition
	pinned := common.ReadFirstLine(serviceFiles.Definition)
	if pinned != "" {
		if found, ok := s.registry.Definition(pinned); ok {
			return s.withDefinition(found)
		}
	}

	// a service created before the pin existed, or by a plugin that has since
	// dropped the definition it named, is placed by the image it recorded, which
	// is what the pin would have held
	return s.ForImageVersion(common.ReadFirstLine(serviceFiles.ImageVersion))
}

// ForImageVersion returns the datastore as a service on a given image version
// runs it. Create has no service to read a pin from yet, and upgrade is moving
// one from under its old pin.
func (s *Datastore) ForImageVersion(imageVersion string) *Datastore {
	if s == nil || s.registry == nil || imageVersion == "" {
		return s
	}

	found, err := s.registry.For(s.Definition.Dokku.Plugin, imageVersion)
	if err != nil {
		return s
	}

	return s.withDefinition(found)
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
