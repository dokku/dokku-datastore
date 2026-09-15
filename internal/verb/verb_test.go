package verb

import (
	"errors"
	"strings"
	"testing"

	"github.com/dokku/dokku-datastore/internal/backend"
	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/registry"
)

// redisScope is the state of a redis service named lollipop, matching the scope
// the renderer's tests use.
func redisScope() definition.Scope {
	return definition.Scope{
		ServiceName:   "lollipop",
		ContainerName: "dokku.redis.lollipop",
		Host:          "dokku-redis-lollipop",
		Database:      "lollipop",
		Plugin:        "redis",
		Title:         "Redis",
		Variable:      "REDIS",
		Image:         "redis",
		ImageVersion:  "8.8.0",
		TaggedImage:   "redis:8.8.0",
		ServiceRoot:   "/var/lib/dokku/services/redis/lollipop",
		HostRoot:      "/var/lib/dokku/services/redis/lollipop",
		Scheme:        "redis",
		Secret:        map[string]string{"password": "hunter2"},
		Port:          map[string]int{"native": 6379},
	}
}

func redisDefinition(t *testing.T) definition.Definition {
	t.Helper()

	loaded, err := registry.Load(registry.LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	redis, ok := loaded.Definition("redis")
	if !ok {
		t.Fatal("expected an embedded redis definition")
	}

	return redis
}

func redisInput(t *testing.T, name string) RunInput {
	t.Helper()

	return RunInput{
		Definition: redisDefinition(t),
		Scope:      redisScope(),
		Name:       name,
		Container:  "dokku.redis.lollipop",
	}
}

func TestResolveRedisConnect(t *testing.T) {
	resolved, err := Resolve(redisInput(t, "connect"))
	if err != nil {
		t.Fatalf("unable to resolve connect: %s", err)
	}

	expected := "container exec --env=LANG=C.UTF-8 --env=LC_ALL=C.UTF-8 --env=REDISCLI_AUTH=hunter2 -i dokku.redis.lollipop redis-cli --no-auth-warning"
	if actual := strings.Join(backend.ExecArgs(resolved), " "); actual != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, actual)
	}
}

// The Go implementation passes the password as `-a <password>`, where any user
// on the host can read it out of the process table. The definition passes it in
// the environment instead, and that difference is the point rather than an
// accident, so it is asserted rather than left to the argv comparison above.
func TestResolveKeepsTheRedisPasswordOutOfArgv(t *testing.T) {
	resolved, err := Resolve(redisInput(t, "connect"))
	if err != nil {
		t.Fatalf("unable to resolve connect: %s", err)
	}

	for _, argument := range resolved.Argv {
		if strings.Contains(argument, "hunter2") {
			t.Errorf("the password reached argv as %q", argument)
		}
	}

	if resolved.Env["REDISCLI_AUTH"] != "hunter2" {
		t.Errorf("expected the password in REDISCLI_AUTH, got %q", resolved.Env["REDISCLI_AUTH"])
	}
}

func TestResolveReportsAnUndeclaredVerb(t *testing.T) {
	// redis declares no export until the dump script is baked into the image,
	// which is what makes redis:export unimplemented rather than broken
	_, err := Resolve(redisInput(t, "export"))

	notImplemented := ErrNotImplemented{}
	if !errors.As(err, &notImplemented) {
		t.Fatalf("expected an ErrNotImplemented, got %v", err)
	}

	if notImplemented.Plugin != "redis" || notImplemented.Name != "export" {
		t.Errorf("expected redis/export, got %s/%s", notImplemented.Plugin, notImplemented.Name)
	}

	if expected := "redis does not implement export"; err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestResolveRendersTemplates(t *testing.T) {
	input := RunInput{
		Definition: definition.Definition{
			Dokku: definition.Dokku{
				Plugin: "postgres",
				Commands: map[string]definition.Command{
					"connect": {
						Exec: []string{"psql", "--username={{ .ServiceName }}", "{{ .Database }}"},
						Env:  map[string]string{"PGPASSWORD": "{{ .Secret.password }}"},
						User: "postgres",
					},
				},
			},
		},
		Scope: definition.Scope{
			ServiceName: "lollipop",
			Database:    "lollipop",
			Secret:      map[string]string{"password": "hunter2"},
		},
		Name:      "connect",
		Container: "dokku.postgres.lollipop",
	}

	resolved, err := Resolve(input)
	if err != nil {
		t.Fatalf("unable to resolve connect: %s", err)
	}

	if expected := "psql --username=lollipop lollipop"; strings.Join(resolved.Argv, " ") != expected {
		t.Errorf("expected %q, got %q", expected, strings.Join(resolved.Argv, " "))
	}

	if resolved.User != "postgres" {
		t.Errorf("expected the command to run as postgres, got %q", resolved.User)
	}
}

// An element guarded on an unset value disappears, which is what the bash
// plugins' [[ -n "$X" ]] did around the same flag.
func TestResolveDropsAnElementThatRendersEmpty(t *testing.T) {
	resolved, err := Resolve(commandInput(definition.Command{
		Exec: []string{"mysql", "{{ if .Database }}--database={{ .Database }}{{ end }}"},
	}, definition.Scope{}))
	if err != nil {
		t.Fatalf("unable to resolve: %s", err)
	}

	if expected := "mysql"; strings.Join(resolved.Argv, " ") != expected {
		t.Errorf("expected %q, got %q", expected, strings.Join(resolved.Argv, " "))
	}
}

func TestResolveRejectsACommandThatRendersToNothing(t *testing.T) {
	// with every element dropped there is no program left to run, and saying so
	// beats handing docker exec a container name and no command
	_, err := Resolve(commandInput(definition.Command{
		Exec: []string{"{{ .Memory }}"},
	}, definition.Scope{}))

	if err == nil {
		t.Fatal("expected an empty command to be an error")
	}
}

func TestResolveReportsATemplateError(t *testing.T) {
	_, err := Resolve(commandInput(definition.Command{
		Exec: []string{"psql", "{{ .NotAField }}"},
	}, definition.Scope{}))

	if err == nil {
		t.Fatal("expected a template naming a missing field to be an error")
	}
}

func TestRunRefusesAModeItCannotHonour(t *testing.T) {
	// running an offline command in the service container would apply it to a
	// running server, which for redis's import means writing a dump file the
	// server has already read and will overwrite on shutdown
	for _, mode := range []string{definition.ModeOffline, definition.ModeSidecar, definition.ModeHost} {
		t.Run(mode, func(t *testing.T) {
			input := commandInput(definition.Command{
				Exec: []string{"true"},
				Mode: mode,
			}, definition.Scope{})

			err := Run(t.Context(), input)
			if err == nil {
				t.Fatalf("expected mode %s to be refused", mode)
			}

			if !strings.Contains(err.Error(), mode) {
				t.Errorf("expected the error to name the mode, got %q", err)
			}
		})
	}
}

func commandInput(command definition.Command, scope definition.Scope) RunInput {
	return RunInput{
		Definition: definition.Definition{
			Dokku: definition.Dokku{
				Plugin:   "example",
				Commands: map[string]definition.Command{"connect": command},
			},
		},
		Scope:     scope,
		Name:      "connect",
		Container: "dokku.example.lollipop",
	}
}
