package verb

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

func definitionFor(t *testing.T, name string) definition.Definition {
	t.Helper()

	loaded, err := registry.Load(registry.LoadInput{})
	if err != nil {
		t.Fatalf("unable to load the registry: %s", err)
	}

	loadedDefinition, ok := loaded.Definition(name)
	if !ok {
		t.Fatalf("expected an embedded %s definition", name)
	}

	return loadedDefinition
}

func redisDefinition(t *testing.T) definition.Definition {
	t.Helper()

	return definitionFor(t, "redis")
}

func redisInput(t *testing.T, name string) RunInput {
	t.Helper()

	return RunInput{
		Definition: redisDefinition(t),
		Scope:      redisScope(),
		Name:       name,
		Names:      backend.Names{Container: "dokku.redis.lollipop", Ambassador: "dokku.redis.lollipop.ambassador"},
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

// Over ssh without -t there is no terminal, and the mysql and mariadb clients
// then hold every result until they exit, so a session answers nothing until it
// ends. connect asks them to flush each result instead.
func TestResolveMysqlConnectFlushesEachResult(t *testing.T) {
	for _, name := range []string{"mysql", "mariadb"} {
		t.Run(name, func(t *testing.T) {
			scope := redisScope()
			scope.ContainerName = "dokku." + name + ".lollipop"
			scope.Plugin = name

			resolved, err := Resolve(RunInput{
				Definition: definitionFor(t, name),
				Scope:      scope,
				Name:       "connect",
				Names:      backend.Names{Container: scope.ContainerName},
			})
			if err != nil {
				t.Fatalf("unable to resolve connect: %s", err)
			}

			if !slices.Contains(resolved.Argv, "--unbuffered") {
				t.Errorf("expected --unbuffered in %v", resolved.Argv)
			}

			args := backend.ExecArgs(resolved)
			if slices.Contains(args, "-t") {
				t.Errorf("expected no terminal to be asked for: %v", args)
			}
		})
	}
}

// The client logs in as admin when no user is named, whose password is the
// root password, so connect handed the service's password to the wrong account
// and was always refused.
func TestResolveOmnisciConnectUsesTheDsnAccount(t *testing.T) {
	scope := redisScope()
	scope.ContainerName = "dokku.omnisci.lollipop"
	scope.Plugin = "omnisci"

	resolved, err := Resolve(RunInput{
		Definition: definitionFor(t, "omnisci"),
		Scope:      scope,
		Name:       "connect",
		Names:      backend.Names{Container: scope.ContainerName},
	})
	if err != nil {
		t.Fatalf("unable to resolve connect: %s", err)
	}

	for _, expected := range []string{"--user=omnisci", "--db=lollipop"} {
		if !slices.Contains(resolved.Argv, expected) {
			t.Errorf("expected %s in %v", expected, resolved.Argv)
		}
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
	// connect-admin is mongo's, and redis declaring nothing by that name is
	// what makes it unimplemented rather than broken
	_, err := Resolve(redisInput(t, "connect-admin"))

	notImplemented := ErrNotImplemented{}
	if !errors.As(err, &notImplemented) {
		t.Fatalf("expected an ErrNotImplemented, got %v", err)
	}

	if notImplemented.Plugin != "redis" || notImplemented.Name != "connect-admin" {
		t.Errorf("expected redis/connect-admin, got %s/%s", notImplemented.Plugin, notImplemented.Name)
	}

	if expected := "redis does not implement connect-admin"; err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

// Export runs beside the service, sharing its network namespace, so a client
// addressing localhost reaches the service exactly as it would from inside. A
// service container created before the script was vendored has nothing to exec
// but still has a namespace to join, which is what makes export work without
// recreating it first.
func TestResolveRedisExportRunsInASidecar(t *testing.T) {
	redis := redisDefinition(t)

	command, ok := redis.Dokku.Commands["export"]
	if !ok {
		t.Fatal("expected redis to declare export")
	}

	if command.Mode != definition.ModeSidecar {
		t.Errorf("expected export to run in a sidecar, got mode %q", command.Mode)
	}

	input := redisInput(t, "export")
	input.Image = "redis:8.8.0"
	input.Volumes = []string{"/var/lib/dokku/services/redis/lollipop/data:/data"}

	resolved, err := Resolve(input)
	if err != nil {
		t.Fatalf("unable to resolve export: %s", err)
	}

	expected := "container run --rm --env=DUMP_FILE=/data/dump.rdb --env=REDISCLI_AUTH=hunter2 --network=container:dokku.redis.lollipop --volume=/var/lib/dokku/services/redis/lollipop/data:/data -i redis:8.8.0 dokku-redis-export"
	actual := strings.Join(backend.RunArgs(backend.RunInput{
		Image:   input.Image,
		Argv:    resolved.Argv,
		Env:     resolved.Env,
		Volumes: input.Volumes,
		Network: "container:" + input.Names.Container,
	}), " ")

	if actual != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, actual)
	}
}

func TestRunSidecarNeedsAnImage(t *testing.T) {
	input := commandInput(definition.Command{
		Exec: []string{"telnet", "localhost", "11211"},
		Mode: definition.ModeSidecar,
	}, definition.Scope{})

	err := Run(t.Context(), input)
	if err == nil {
		t.Fatal("expected a sidecar with no image to be refused")
	}

	if !strings.Contains(err.Error(), "image") {
		t.Errorf("expected the error to mention the image, got %q", err)
	}
}

// The dump is replaced with the server down, so import runs in a throwaway
// container on the service's own mounts rather than in the service container.
func TestResolveRedisImportRunsOffline(t *testing.T) {
	redis := redisDefinition(t)

	command, ok := redis.Dokku.Commands["import"]
	if !ok {
		t.Fatal("expected redis to declare import")
	}

	if command.Mode != definition.ModeOffline {
		t.Errorf("expected import to run offline, got mode %q", command.Mode)
	}

	if !command.Stdin {
		t.Error("expected import to consume stdin")
	}

	input := redisInput(t, "import")
	input.Image = "dokku/datastore-redis:8.8.0"
	input.Volumes = []string{"/var/lib/dokku/services/redis/lollipop/data:/data"}

	resolved, err := Resolve(input)
	if err != nil {
		t.Fatalf("unable to resolve import: %s", err)
	}

	expected := "container run --rm --env=DUMP_FILE=/data/dump.rdb --volume=/var/lib/dokku/services/redis/lollipop/data:/data -i dokku/datastore-redis:8.8.0 dokku-redis-import"
	actual := strings.Join(backend.RunArgs(backend.RunInput{
		Image:   input.Image,
		Argv:    resolved.Argv,
		Env:     resolved.Env,
		Volumes: input.Volumes,
	}), " ")

	if actual != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, actual)
	}
}

// Both come from export and import being declared, which is the only statement
// of what a datastore can do.
func TestRedisImplementsTheDumpFamily(t *testing.T) {
	redis := redisDefinition(t)

	for _, subcommand := range []string{"export", "import", "clone", "backup", "backup-schedule"} {
		if !redis.Implements(subcommand) {
			t.Errorf("expected redis to implement %s", subcommand)
		}
	}
}

// clickhouseInput is a verb against a clickhouse service named lollipop.
func clickhouseInput(t *testing.T, name string) RunInput {
	t.Helper()

	scope := redisScope()
	scope.ContainerName = "dokku.clickhouse.lollipop"
	scope.Host = "dokku-clickhouse-lollipop"
	scope.Plugin = "clickhouse"
	scope.Title = "Clickhouse"
	scope.Variable = "CLICKHOUSE"
	scope.Image = "clickhouse/clickhouse-server"
	scope.ImageVersion = "26.9.1.1629"
	scope.TaggedImage = "clickhouse/clickhouse-server:26.9.1.1629"
	scope.ServiceRoot = "/var/lib/dokku/services/clickhouse/lollipop"
	scope.HostRoot = "/var/lib/dokku/services/clickhouse/lollipop"
	scope.Scheme = "clickhouse"
	scope.Port = map[string]int{"native": 9000, "http": 8123}

	return RunInput{
		Definition: definitionFor(t, "clickhouse"),
		Scope:      scope,
		Name:       name,
		Image:      scope.TaggedImage,
		Volumes: []string{
			"/var/lib/dokku/services/clickhouse/lollipop/config:/etc/clickhouse-server",
			"/var/lib/dokku/services/clickhouse/lollipop/data:/var/lib/clickhouse",
		},
		Names: backend.Names{Container: "dokku.clickhouse.lollipop", Ambassador: "dokku.clickhouse.lollipop.ambassador"},
	}
}

// clickhouseSidecarArgs renders a clickhouse verb as the sidecar it runs in.
func clickhouseSidecarArgs(t *testing.T, input RunInput) string {
	t.Helper()

	resolved, err := Resolve(input)
	if err != nil {
		t.Fatalf("unable to resolve %s: %s", input.Name, err)
	}

	return strings.Join(backend.RunArgs(backend.RunInput{
		Image:   input.Image,
		Argv:    resolved.Argv,
		Env:     resolved.Env,
		Volumes: input.Volumes,
		Network: "container:" + input.Names.Container,
	}), " ")
}

// Clickhouse's BACKUP and RESTORE read and write the archive on the server's
// own filesystem, so both dump verbs run beside the service with its volumes
// rather than inside it. The credentials stay out of the CLICKHOUSE_ variables
// the image's entrypoint and the client read for themselves.
func TestResolveClickhouseDumpVerbsRunInASidecar(t *testing.T) {
	tests := []struct {
		name     string
		stdin    bool
		expected string
	}{
		{
			name:     "export",
			expected: "container run --rm --env=BACKUP_CONFIG=/etc/clickhouse-server/config.d/dokku-backups.xml --env=CONFIG_FILE=/etc/clickhouse-server/config.xml --env=DOKKU_CLICKHOUSE_DATABASE=lollipop --env=DOKKU_CLICKHOUSE_PASSWORD=hunter2 --env=DOKKU_CLICKHOUSE_USER=lollipop --network=container:dokku.clickhouse.lollipop --volume=/var/lib/dokku/services/clickhouse/lollipop/config:/etc/clickhouse-server --volume=/var/lib/dokku/services/clickhouse/lollipop/data:/var/lib/clickhouse -i clickhouse/clickhouse-server:26.9.1.1629 dokku-clickhouse-export",
		},
		{
			name:     "import",
			stdin:    true,
			expected: "container run --rm --env=BACKUP_CONFIG=/etc/clickhouse-server/config.d/dokku-backups.xml --env=CONFIG_FILE=/etc/clickhouse-server/config.xml --env=DOKKU_CLICKHOUSE_DATABASE=lollipop --env=DOKKU_CLICKHOUSE_PASSWORD=hunter2 --env=DOKKU_CLICKHOUSE_USER=lollipop --network=container:dokku.clickhouse.lollipop --volume=/var/lib/dokku/services/clickhouse/lollipop/config:/etc/clickhouse-server --volume=/var/lib/dokku/services/clickhouse/lollipop/data:/var/lib/clickhouse -i clickhouse/clickhouse-server:26.9.1.1629 dokku-clickhouse-import",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := clickhouseInput(t, test.name)

			command, ok := input.Definition.Dokku.Commands[test.name]
			if !ok {
				t.Fatalf("expected clickhouse to declare %s", test.name)
			}

			if command.Mode != definition.ModeSidecar {
				t.Errorf("expected %s to run in a sidecar, got mode %q", test.name, command.Mode)
			}

			if command.Stdin != test.stdin {
				t.Errorf("expected %s to have stdin %t, got %t", test.name, test.stdin, command.Stdin)
			}

			if command.ExtraArgs {
				t.Errorf("expected %s to refuse extra arguments, since the scripts take none", test.name)
			}

			if actual := clickhouseSidecarArgs(t, input); actual != test.expected {
				t.Errorf("expected:\n%s\ngot:\n%s", test.expected, actual)
			}
		})
	}
}

func TestClickhouseImplementsTheDumpFamily(t *testing.T) {
	clickhouse := definitionFor(t, "clickhouse")

	for _, subcommand := range []string{"export", "import", "clone", "backup", "backup-schedule"} {
		if !clickhouse.Implements(subcommand) {
			t.Errorf("expected clickhouse to implement %s", subcommand)
		}
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
		Name:  "connect",
		Names: backend.Names{Container: "dokku.postgres.lollipop"},
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
	// a mode that got past parsing is still refused rather than run in the
	// service container, which would run it somewhere other than where the
	// definition asked for
	input := commandInput(definition.Command{
		Exec: []string{"true"},
		Mode: "somewhere-else",
	}, definition.Scope{})

	err := Run(t.Context(), input)
	if err == nil {
		t.Fatal("expected an unknown mode to be refused")
	}

	if !strings.Contains(err.Error(), "somewhere-else") {
		t.Errorf("expected the error to name the mode, got %q", err)
	}
}

// A host command runs a script the definition ships, so without somewhere to
// find those scripts there is nothing it may run.
func TestRunHostNeedsTheScriptsTheDefinitionShips(t *testing.T) {
	input := commandInput(definition.Command{
		Exec: []string{"nginx-expose"},
		Mode: definition.ModeHost,
	}, definition.Scope{})

	err := Run(t.Context(), input)
	if err == nil {
		t.Fatal("expected a host command with no script root to be refused")
	}

	if !strings.Contains(err.Error(), "scripts the definition ships") {
		t.Errorf("expected the error to say what is missing, got %q", err)
	}
}

// Resolving by base name is what keeps a host command to the scripts its own
// definition ships. A path would be a way to reach anything on the host.
func TestRunHostRefusesAPath(t *testing.T) {
	for _, argv0 := range []string{"/bin/sh", "../../../bin/sh", "./nginx-expose"} {
		t.Run(argv0, func(t *testing.T) {
			input := commandInput(definition.Command{
				Exec: []string{argv0},
				Mode: definition.ModeHost,
			}, definition.Scope{})
			input.ScriptRoot = t.TempDir()

			err := Run(t.Context(), input)
			if err == nil {
				t.Fatalf("expected %s to be refused", argv0)
			}

			if !strings.Contains(err.Error(), "path") {
				t.Errorf("expected the error to say it is a path, got %q", err)
			}
		})
	}
}

// And a name the definition does not ship is refused rather than looked for on
// the host's own path.
func TestRunHostRefusesAScriptItDoesNotShip(t *testing.T) {
	input := commandInput(definition.Command{
		Exec: []string{"sh"},
		Mode: definition.ModeHost,
	}, definition.Scope{})
	input.ScriptRoot = t.TempDir()

	err := Run(t.Context(), input)
	if err == nil {
		t.Fatal("expected a script the definition does not ship to be refused")
	}

	if !strings.Contains(err.Error(), "does not ship") {
		t.Errorf("expected the error to say so, got %q", err)
	}
}

// What it does run is the script beside it, with the declared environment.
func TestRunHostRunsTheScriptWithItsEnvironment(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "greet")
	body := "#!/usr/bin/env bash\necho \"$GREETING $1\"\n"
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatalf("unable to write the script: %s", err)
	}

	input := commandInput(definition.Command{
		Exec: []string{"greet", "{{ .ServiceName }}"},
		Mode: definition.ModeHost,
		Env:  map[string]string{"GREETING": "hello"},
	}, definition.Scope{ServiceName: "lollipop"})
	input.ScriptRoot = root

	output := bytes.Buffer{}
	input.Stdout = &output

	if err := Run(t.Context(), input); err != nil {
		t.Fatalf("expected the script to run, got %s", err)
	}

	if got := strings.TrimSpace(output.String()); got != "hello lollipop" {
		t.Errorf("expected the script to see its environment and argument, got %q", got)
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
		Scope: scope,
		Name:  "connect",
		Names: backend.Names{Container: "dokku.example.lollipop"},
	}
}

// fakeDocker stands in for the docker binary, recording each invocation as a
// line of its arguments, and returns the file those lines are written to.
func fakeDocker(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	calls := filepath.Join(root, "calls")
	script := filepath.Join(root, "docker")
	body := "#!/usr/bin/env bash\necho \"$*\" >>\"" + calls + "\"\n"
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatalf("unable to write the fake docker: %s", err)
	}

	t.Setenv("DOCKER_BIN", script)
	return calls
}

// A command run in a container of its own may need the image's entrypoint
// cleared, as an image whose entrypoint starts the datastore would otherwise
// start it rather than run the command. Hooks always honored the setting;
// sidecar and offline commands dropped it without a word.
func TestRunPassesTheEntrypointToItsContainer(t *testing.T) {
	for _, mode := range []string{definition.ModeSidecar, definition.ModeOffline} {
		t.Run(mode, func(t *testing.T) {
			calls := fakeDocker(t)

			entrypoint := ""
			input := commandInput(definition.Command{
				Exec:       []string{"dump"},
				Mode:       mode,
				Entrypoint: &entrypoint,
			}, definition.Scope{})
			input.Image = "example:1.0"

			if err := Run(t.Context(), input); err != nil {
				t.Fatalf("expected the command to run, got %s", err)
			}

			recorded, err := os.ReadFile(calls)
			if err != nil {
				t.Fatalf("unable to read the recorded calls: %s", err)
			}

			var run string
			for _, line := range strings.Split(string(recorded), "\n") {
				if strings.HasPrefix(line, "container run ") {
					run = line
				}
			}

			if run == "" {
				t.Fatalf("expected a container run, got:\n%s", recorded)
			}

			if !strings.Contains(run, "--entrypoint= ") {
				t.Errorf("expected the entrypoint cleared, got %q", run)
			}
		})
	}
}

func TestRunOfflineNeedsTheServiceImage(t *testing.T) {
	// without it there is nothing to run the command in, and finding that out
	// after stopping the service would leave the datastore down for nothing
	input := commandInput(definition.Command{
		Exec: []string{"sh", "-c", "cat > /data/dump.rdb"},
		Mode: definition.ModeOffline,
	}, definition.Scope{})

	err := Run(t.Context(), input)
	if err == nil {
		t.Fatal("expected an offline command with no image to be refused")
	}

	if !strings.Contains(err.Error(), "image") {
		t.Errorf("expected the error to mention the image, got %q", err)
	}
}

// Extra arguments go after everything the definition renders, each as an
// argument of its own, so one with a space in it reaches the tool whole.
func TestResolveAppendsExtraArgs(t *testing.T) {
	tests := []struct {
		definition string
		verb       string
		extraArgs  []string
		last       string
	}{
		{definition: "mysql", verb: "export", extraArgs: []string{"--hex-blob", "--where=id > 1"}, last: "lollipop"},
		{definition: "mariadb", verb: "import", extraArgs: []string{"--max-allowed-packet=1G"}, last: "lollipop"},
		{definition: "postgres-18", verb: "import", extraArgs: []string{"--single-transaction"}, last: "-w"},
		// handed to the wrapper after the database, which passes them to pg_restore
		{definition: "postgres-timescaledb-pg18", verb: "import", extraArgs: []string{"--single-transaction"}, last: "lollipop"},
		{definition: "mongo", verb: "export", extraArgs: []string{"--numParallelCollections=1"}, last: "--archive"},
	}

	for _, test := range tests {
		t.Run(test.definition+" "+test.verb, func(t *testing.T) {
			scope := redisScope()
			scope.Plugin = test.definition
			scope.Secret = map[string]string{"password": "hunter2", "root_password": "hunter3"}

			resolved, err := Resolve(RunInput{
				Definition: definitionFor(t, test.definition),
				Scope:      scope,
				Name:       test.verb,
				Names:      backend.Names{Container: "dokku." + test.definition + ".lollipop"},
				ExtraArgs:  test.extraArgs,
			})
			if err != nil {
				t.Fatalf("unable to resolve %s: %s", test.verb, err)
			}

			rendered := len(resolved.Argv) - len(test.extraArgs)
			if rendered < 1 || resolved.Argv[rendered-1] != test.last {
				t.Fatalf("expected the extra arguments after %q, got %q", test.last, resolved.Argv)
			}

			if actual := resolved.Argv[rendered:]; !slices.Equal(actual, test.extraArgs) {
				t.Errorf("expected %q appended, got %q", test.extraArgs, actual)
			}
		})
	}
}

// A timescaledb import restores with the extension's background workers stopped,
// since one that writes its own catalog row mid-restore fails the copy of that
// catalog, and starts them again afterwards. The extra arguments still reach
// pg_restore, as the wrapper's own arguments after the database.
func TestResolveTimescaledbImportStopsTheWorkersAroundTheRestore(t *testing.T) {
	for _, name := range []string{"postgres-timescaledb-pg17", "postgres-timescaledb-pg18"} {
		t.Run(name, func(t *testing.T) {
			scope := redisScope()
			scope.Plugin = "postgres"
			scope.Secret = map[string]string{"password": "hunter2"}

			resolved, err := Resolve(RunInput{
				Definition: definitionFor(t, name),
				Scope:      scope,
				Name:       "import",
				Names:      backend.Names{Container: "dokku.postgres.lollipop"},
				ExtraArgs:  []string{"--single-transaction"},
			})
			if err != nil {
				t.Fatalf("unable to resolve import: %s", err)
			}

			if len(resolved.Argv) != 6 || resolved.Argv[0] != "sh" || resolved.Argv[1] != "-c" {
				t.Fatalf("expected sh -c, a script, its name, the database and the extra argument, got %q", resolved.Argv)
			}

			if actual := resolved.Argv[4:]; !slices.Equal(actual, []string{"lollipop", "--single-transaction"}) {
				t.Errorf("expected the database and then the extra argument, got %q", actual)
			}

			script := resolved.Argv[2]
			pre := strings.Index(script, "timescaledb_pre_restore()")
			restore := strings.Index(script, `pg_restore -h localhost -cO --if-exists -d "$database" -U postgres -w "$@"`)
			post := strings.Index(script, "timescaledb_post_restore()")
			reset := strings.Index(script, "RESET timescaledb.restoring")
			if pre < 0 || restore < pre || post < restore || reset < post {
				t.Errorf("expected the workers stopped, the restore, then the workers started and the setting reset, got %q", script)
			}
		})
	}
}

// Without any, the command is exactly what the definition renders.
func TestResolveWithoutExtraArgs(t *testing.T) {
	withNone, err := Resolve(redisInput(t, "export"))
	if err != nil {
		t.Fatalf("unable to resolve export: %s", err)
	}

	if !slices.Equal(withNone.Argv, []string{"dokku-redis-export"}) {
		t.Errorf("expected the definition's own argv, got %q", withNone.Argv)
	}
}

// redis dumps with a script that never reads its argv, so an argument handed to
// it would be dropped without a word and the dump made without it.
func TestResolveRefusesExtraArgsACommandIgnores(t *testing.T) {
	for _, name := range []string{"export", "import"} {
		t.Run(name, func(t *testing.T) {
			input := redisInput(t, name)
			input.ExtraArgs = []string{"--anything"}

			_, err := Resolve(input)
			var refused ErrExtraArgsRefused
			if !errors.As(err, &refused) {
				t.Fatalf("expected a refusal, got %v", err)
			}

			if expected := "redis " + name + " does not take extra arguments"; err.Error() != expected {
				t.Errorf("expected %q, got %q", expected, err)
			}
		})
	}
}

// A service that moved its data volume keeps its dump there, and the scripts
// are told so rather than looking where the image would have put it.
func TestResolveRedisDumpVerbsFollowAMovedDataVolume(t *testing.T) {
	for _, name := range []string{"export", "import"} {
		t.Run(name, func(t *testing.T) {
			input := redisInput(t, name)
			input.Scope.Target = map[string]string{"data": "/redis-data"}

			resolved, err := Resolve(input)
			if err != nil {
				t.Fatalf("unable to resolve %s: %s", name, err)
			}

			if actual := resolved.Env["DUMP_FILE"]; actual != "/redis-data/dump.rdb" {
				t.Errorf("expected the dump in the moved volume, got %q", actual)
			}
		})
	}
}

// Clickhouse's scripts read the server's config to find where backups go, from
// wherever the service mounts it.
func TestResolveClickhouseDumpVerbsFollowAMovedConfigVolume(t *testing.T) {
	input := clickhouseInput(t, "export")
	input.Scope.Target = map[string]string{"config": "/etc/clickhouse"}

	resolved, err := Resolve(input)
	if err != nil {
		t.Fatalf("unable to resolve export: %s", err)
	}

	if actual := resolved.Env["CONFIG_FILE"]; actual != "/etc/clickhouse/config.xml" {
		t.Errorf("expected the config read from the moved volume, got %q", actual)
	}

	if actual := resolved.Env["BACKUP_CONFIG"]; actual != "/etc/clickhouse/config.d/dokku-backups.xml" {
		t.Errorf("expected the backup config written to the moved volume, got %q", actual)
	}
}

// A command started in a container of its own mounts the volumes it declares
// after the ones it was handed, resolved against the service root dockerd
// sees, which is how a command reaches past the service's own mounts.
func TestVolumesAppendsWhatTheCommandDeclares(t *testing.T) {
	command := definition.Command{
		Volumes: []definition.Volume{
			{Type: "bind", Source: "{{ .HostRoot }}", Target: "/service"},
			{Type: "bind", Source: "{{ .HostRoot }}/certs", Target: "/certs"},
		},
	}
	scope := definition.Scope{HostRoot: "/host/services/postgres/lollipop"}
	handed := []string{"/host/services/postgres/lollipop/data:/var/lib/postgresql"}

	actual := Volumes(handed, command, scope)
	expected := []string{
		"/host/services/postgres/lollipop/data:/var/lib/postgresql",
		"/host/services/postgres/lollipop:/service",
		"/host/services/postgres/lollipop/certs:/certs",
	}
	if !slices.Equal(actual, expected) {
		t.Errorf("expected %v, got %v", expected, actual)
	}

	// the slice handed in is the service's own, which another command reads
	// after this one
	if len(handed) != 1 {
		t.Errorf("expected the handed volumes to be left alone, got %v", handed)
	}
}
