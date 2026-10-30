package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/dokku/dokku-datastore/internal/definition"
	"github.com/dokku/dokku-datastore/internal/service"
)

// DokkuVersion is the dokku version the generated readme tells people to install
// on. It is the lower leg of each plugin's test matrix, and the two are meant to
// be changed together: the matrix is the only thing that can notice this drifting
// out of date, which is how it came to say 0.19 while the code needed 0.38.
const DokkuVersion = "0.35.x+"

// readmeSections are the readme usage sections, in the order they are written
// out, along with the prose that introduces each one
// readmeSections are the readme's usage sections, in the order they are written,
// each with the order of the commands inside it.
//
// Commands is the order, not the membership: which section a command belongs to
// is the command's own answer, from Group(). A command in a section but not named
// here is documented after the ones that are, alphabetically, so adding a command
// is never a crash and never a silent reordering of everything around it. That is
// also what puts a custom command last in whichever section it declares, without
// anything here having to know about custom commands at all.
var readmeSections = []struct {
	Group    string
	Title    string
	Intro    []string
	Commands []string
}{
	{
		Group:    definition.GroupBasicUsage,
		Title:    "Basic Usage",
		Commands: []string{"create", "destroy", "info", "list", "logs", "link", "unlink", "set", "mount", "unmount", "label", "unlabel"},
	},
	{
		Group:    definition.GroupServiceLifecycle,
		Title:    "Service Lifecycle",
		Intro:    []string{"The lifecycle of each service can be managed through the following commands:"},
		Commands: []string{"connect", "enter", "expose", "unexpose", "reexpose", "promote", "start", "stop", "pause", "restart", "upgrade"},
	},
	{
		Group:    definition.GroupServiceAutomation,
		Title:    "Service Automation",
		Intro:    []string{"Service scripting can be executed using the following commands:"},
		Commands: []string{"app-links", "clone", "exists", "linked", "links"},
	},
	{
		Group:    definition.GroupDataManagement,
		Title:    "Data Management",
		Intro:    []string{"The underlying service data can be imported and exported with the following commands:"},
		Commands: []string{"import", "export", "reset"},
	},
	{
		Group: definition.GroupDatabasesAndUsers,
		Title: "Databases and Users",
		Intro: []string{
			"A service runs in a container of its own. Several apps can share one service, and the memory its container uses, by each being linked to a database or user of its own within it, made with the following commands. Every user made in a service can use every database in it.",
		},
		Commands: []string{"create-database", "destroy-database", "create-user", "destroy-user", "set-user-password"},
	},
	{
		Group: definition.GroupBackups,
		Title: "Backups",
		Intro: []string{
			"Datastore backups are supported via AWS S3 and S3 compatible services like [minio](https://github.com/minio/minio) and [DigitalOcean Spaces](https://docs.digitalocean.com/products/spaces/).",
			"The endpoint of an S3 compatible service is passed as the `endpoint-url` argument of `backup-auth`, such as `https://nyc3.digitaloceanspaces.com`, and must not include the bucket. The bucket is passed to `backup` and `backup-schedule` by its name alone, such as `my-s3-bucket` rather than `s3://my-s3-bucket`, and must follow the [S3 bucket naming rules](https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html).",
			"You may skip the `backup-auth` step if your dokku install is running within EC2 and has access to the bucket via an IAM profile. In that case, use the `--use-iam` option with the `backup` command.",
			"If both passphrase and public key forms of encryption are set, the public key encryption will take precedence.",
			"Each setting `backup-auth`, `backup-set-encryption` and `backup-set-public-key-encryption` store can also be changed on its own with the `set` command, as can the bucket and IAM setting of the schedule made without `--id`, when that is the only schedule. The keys are listed under Service properties.",
			"Backups are uploaded with the bucket's default storage class unless the service sets the `backup-storage-class` property with the `set` command. A schedule may set its own value with `set <key> <value> --id <id>`.",
			"Backups are uploaded to `<prefix>-<service>-<timestamp>.tgz`. The service may name the key with the `backup-object-name` property and drop the timestamp by setting the `backup-timestamp` property to `false`, so that every backup is uploaded to the same key and bucket versioning and lifecycle rules can keep and rotate them. The bucket name may end in a path to upload under, such as `my-s3-bucket/backups`. A schedule may set either for itself.",
			"The underlying core backup script is present [here](https://github.com/dokku/docker-s3backup/blob/main/backup.sh).",
			"A service has one scheduled backup, made with `backup-schedule` and replaced by calling it again. Further schedules are added with `--id <name>`, each with its own cron expression and bucket, so a service can ship a frequent backup and a monthly backup to different buckets. `backup-schedule-cat` lists every schedule. `backup-unschedule` removes the only schedule; with several it removes the one named by `--id`, or every schedule with `--all`.",
			"`backup-auth` and `backup-deauth` take the same `--id`. A schedule is created first, then given its own access key and secret. A schedule with both uses that pair. A schedule with neither uses the service's credentials, unless the backup is run with `--use-iam`. A region, signature version or endpoint set on the schedule is used for that schedule, and one left unset is the service's.",
			"Scheduled backups are added to the dokku crontab, and are listed by `dokku cron:list --global`. Each service's scheduled backups append their output to a log of its own, `/var/log/dokku/<prefix>.<service>.backup.log`, which the `backup-logs` command shows. The output of a service's scheduled backups can be mailed to specific recipients by setting the `backup-mailto` property with the `set` command, on dokku versions that support a per-entry `MAILTO`. The service value applies to every schedule that has not set its own.",
			"Backups can be performed using the backup commands:",
		},
		Commands: []string{
			"backup-auth", "backup-deauth", "backup",
			"backup-set-encryption", "backup-set-public-key-encryption",
			"backup-unset-encryption", "backup-unset-public-key-encryption",
			"backup-schedule", "backup-schedule-cat", "backup-unschedule",
			"backup-logs",
		},
	},
	{
		// last, and with no order of its own: a datastore's own commands are
		// listed alphabetically among themselves
		Group: definition.GroupCustomCommands,
		Title: "Custom Commands",
		Intro: []string{"This datastore adds the following commands of its own:"},
	},
}

// SectionCommands is the order a readme usage section documents its commands in,
// and whether the readme has such a section at all.
//
// Exported for the test that holds every command to being named here: a command
// missing from its section's list still renders, at the end, so nothing else
// would say it had been forgotten.
func SectionCommands(group string) ([]string, bool) {
	for _, section := range readmeSections {
		if section.Group == group {
			return section.Commands, true
		}
	}

	return nil, false
}

// maxCommandColumn is how wide the command column in the readme command list
// grows before the descriptions are left to run on
const maxCommandColumn = 50

// ReadmeInput is the input for the Readme function
type ReadmeInput struct {
	// Commands are the commands the plugin exposes as subcommands
	Commands []PluginCommand

	// Data is the datastore the readme is rendered for
	Data DocumentationData

	// PluginDir is the plugin checkout the readme is generated for
	PluginDir string

	// Sponsors are the github accounts that sponsored the plugin
	Sponsors []string
}

// Readme renders the plugin's readme
func Readme(input ReadmeInput) (string, error) {
	sections := []string{
		readmeHeader(input.Data),
		readmeDescription(input.Data),
	}

	if len(input.Sponsors) > 0 {
		sections = append(sections, readmeSponsors(input.Data, input.Sponsors))
	}

	sections = append(sections,
		readmeRequirements(),
		readmeInstallation(input.Data),
	)

	commandList, err := readmeCommandList(input)
	if err != nil {
		return "", err
	}
	sections = append(sections, commandList)

	usage, err := readmeUsage(input)
	if err != nil {
		return "", err
	}
	sections = append(sections, usage...)

	return strings.Join(sections, "\n\n") + "\n", nil
}

// readmeHeader is the title line, with the badges that follow it
func readmeHeader(data DocumentationData) string {
	prefix := data.CommandPrefix
	return strings.Join([]string{
		fmt.Sprintf("# dokku %s", prefix),
		fmt.Sprintf(`[![Build Status](https://img.shields.io/github/actions/workflow/status/dokku/dokku-%s/ci.yml?branch=master&style=flat-square "Build Status")](https://github.com/dokku/dokku-%s/actions/workflows/ci.yml?query=branch%%3Amaster)`, prefix, prefix),
		`[![IRC Network](https://img.shields.io/badge/irc-libera-blue.svg?style=flat-square "IRC Libera")](https://webchat.libera.chat/?channels=dokku)`,
	}, " ")
}

// readmeDescription says which image the plugin installs by default
func readmeDescription(data DocumentationData) string {
	base := "_"
	image := data.Image
	if namespace, name, found := strings.Cut(data.Image, "/"); found {
		base = "r/" + namespace
		image = name
	}

	return fmt.Sprintf("Official %s plugin for dokku. Currently defaults to installing [%s %s](https://hub.docker.com/%s/%s/).",
		data.CommandPrefix, data.Image, data.ImageVersion, base, image)
}

// readmeSponsors credits the people who paid for the plugin to be written
func readmeSponsors(data DocumentationData, sponsors []string) string {
	lines := []string{
		"## Sponsors",
		"",
		fmt.Sprintf("The %s plugin was generously sponsored by the following:", data.CommandPrefix),
		"",
	}

	for _, sponsor := range sponsors {
		lines = append(lines, fmt.Sprintf("- [%s](https://github.com/%s)", sponsor, sponsor))
	}

	return strings.Join(lines, "\n")
}

// readmeRequirements lists what the plugin needs to run
func readmeRequirements() string {
	return strings.Join([]string{
		"## Requirements",
		"",
		"- dokku " + DokkuVersion,
		"- docker 1.8.x",
	}, "\n")
}

// readmeInstallation is the command that installs the plugin
func readmeInstallation(data DocumentationData) string {
	return strings.Join([]string{
		"## Installation",
		"",
		"```shell",
		"# on " + DokkuVersion,
		fmt.Sprintf("sudo dokku plugin:install https://github.com/dokku/dokku-%s.git --name %s", data.CommandPrefix, data.CommandPrefix),
		"```",
	}, "\n")
}

// readmeCommandList is the block listing every command the plugin implements
func readmeCommandList(input ReadmeInput) (string, error) {
	commands := sortedCommands(input.Commands)

	invocations := make([]string, 0, len(commands))
	descriptions := make([]string, 0, len(commands))
	width := 0
	for _, c := range commands {
		usage, err := RenderDocumentation(c.Usage(), input.Data)
		if err != nil {
			return "", err
		}

		description, err := RenderDocumentation(c.Description(), input.Data)
		if err != nil {
			return "", err
		}

		invocation := input.Data.Command(c.Name()) + " " + usage
		if len(invocation) > width {
			width = len(invocation)
		}

		invocations = append(invocations, invocation)
		descriptions = append(descriptions, description)
	}

	if width > maxCommandColumn {
		width = maxCommandColumn
	}

	lines := []string{"## Commands", "", "```"}
	for i, invocation := range invocations {
		padding := ""
		if width > len(invocation) {
			padding = strings.Repeat(" ", width-len(invocation))
		}

		lines = append(lines, invocation+padding+" # "+descriptions[i])
	}
	lines = append(lines, "```")

	return strings.Join(lines, "\n"), nil
}

// readmeUsage is the per command documentation, grouped into sections
func readmeUsage(input ReadmeInput) ([]string, error) {
	sections := []string{
		"## Usage",
		fmt.Sprintf("Help for any commands can be displayed by specifying the command as an argument to %s. "+
			"Plugin help output in conjunction with any files in the `docs/` folder is used to generate the plugin documentation. "+
			"Please consult the `%s` command for any undocumented commands.", input.Data.Command("help"), input.Data.Command("help")),
	}

	for _, section := range readmeSections {
		commands := commandsInGroup(input.Commands, section.Group, section.Commands)
		if len(commands) == 0 {
			continue
		}

		sections = append(sections, "### "+section.Title)
		sections = append(sections, section.Intro...)

		for _, c := range commands {
			documentation, err := readmeCommand(c, input)
			if err != nil {
				return nil, err
			}

			sections = append(sections, documentation...)
		}
	}

	sections = append(sections, readmeSettableProperties(input.Data)...)
	sections = append(sections, readmeExposeLimits(input.Data)...)
	sections = append(sections, readmeExposeMode(input.Data)...)
	sections = append(sections, readmeExposedDsn(input.Data)...)
	sections = append(sections, readmeExtraArgs(input.Data)...)
	sections = append(sections, readmeWaitTimeout(input.Data)...)
	sections = append(sections, readmeServiceMemory(input.Data)...)
	sections = append(sections, readmeVolumeTargets(input.Data)...)
	sections = append(sections, readmeReservedNames(input.Data)...)

	definitionSections, err := readmeDefinitionSections(input.Data)
	if err != nil {
		return nil, err
	}

	sections = append(sections, definitionSections...)
	sections = append(sections, readmeDockerPull(input.Data)...)
	return sections, nil
}

// readmeCommand is the documentation for a single command
func readmeCommand(c PluginCommand, input ReadmeInput) ([]string, error) {
	usage, err := RenderDocumentation(c.Usage(), input.Data)
	if err != nil {
		return nil, err
	}

	description, err := RenderDocumentation(c.Description(), input.Data)
	if err != nil {
		return nil, err
	}

	invocation := strings.TrimSpace(fmt.Sprintf("dokku %s %s", input.Data.Command(c.Name()), usage))
	blocks := []string{
		"### " + description,
		strings.Join([]string{"```shell", "# usage", invocation, "```"}, "\n"),
	}

	flags, err := DocumentedFlags(c, input.Data)
	if err != nil {
		return nil, err
	}

	if len(flags) > 0 {
		lines := []string{"flags:", ""}
		for _, f := range flags {
			lines = append(lines, fmt.Sprintf("- `%s`: %s", f.Label, f.Description))
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}

	documentation, err := RenderDocumentation(c.Documentation(), input.Data)
	if err != nil {
		return nil, err
	}

	blocks = append(blocks, readmeDocumentationBlocks(documentation)...)

	extra, err := readmeExtraDocumentation(input.PluginDir, c.Name())
	if err != nil {
		return nil, err
	}

	if extra != "" {
		blocks = append(blocks, extra)
	}

	return blocks, nil
}

// readmeDocumentationBlocks renders the long form documentation as markdown
func readmeDocumentationBlocks(documentation string) []string {
	blocks := []string{}
	for _, block := range DocumentationBlocks(documentation) {
		switch block.Kind {
		case DocCommand:
			blocks = append(blocks, "```shell\n"+strings.Join(block.Lines, "\n")+"\n```")
		case DocCode:
			blocks = append(blocks, "```\n"+strings.Join(block.Lines, "\n")+"\n```")
		case DocNote:
			blocks = append(blocks, strings.Join(block.Lines, "\n"))
		default:
			blocks = append(blocks, processSentence(block.Lines))
		}
	}

	return blocks
}

// readmeExtraDocumentation is the plugin's escape hatch for prose that only
// makes sense for one datastore, kept in a file named after the command
func readmeExtraDocumentation(pluginDir string, name string) (string, error) {
	if pluginDir == "" {
		return "", nil
	}

	contents, err := os.ReadFile(filepath.Join(pluginDir, "docs", name+".md"))
	if os.IsNotExist(err) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	return strings.TrimRight(string(contents), "\n"), nil
}

// readmeDockerPull explains how to stop the plugin pulling images
func readmeDockerPull(data DocumentationData) []string {
	return []string{
		"### Disabling `docker image pull` calls",
		fmt.Sprintf("If you wish to disable the `docker image pull` calls that the plugin triggers, you may set the `disable-pull` "+
			"global property to `true` with `dokku %s --global disable-pull true`. Once disabled, you will need to pull the service image you wish to deploy as shown in the "+
			"`stderr` output.", data.Command("set")),
		"Please ensure the proper images are in place when `docker image pull` is disabled.",
	}
}

// readmeSettableProperties lists every property the set command takes for the
// datastore, and when a change to each reaches the service
func readmeSettableProperties(data DocumentationData) []string {
	effects := map[PropertyEffect]string{
		EffectImmediately: "immediately",
		EffectRebuild:     fmt.Sprintf("after `%s` and `%s`", data.Command("stop"), data.Command("start")),
		EffectReexpose:    fmt.Sprintf("after `%s`", data.Command("reexpose")),
	}

	// one block, since the sections are joined by blank lines and a table
	// broken by one is not a table
	table := []string{
		"| Property | Description | Takes effect |",
		"| --- | --- | --- |",
	}
	for _, property := range SettableProperties {
		if !documentsProperty(property, data) {
			continue
		}

		effect := effects[property.TakesEffect]
		if property.IsSecret() && data.PasswordRestarts {
			effect = "once `set` restarts the service, with `--allow-restart`"
		}

		// told to a running service where the datastore can take it that way
		if property.Name == service.ServiceMemoryProperty && data.ServiceMemoryLive {
			effect = effects[EffectImmediately]
		}

		table = append(table, fmt.Sprintf("| `%s` | %s | %s |", property.Name, property.Description, effect))
	}

	sections := []string{
		"### Service properties",
		fmt.Sprintf("Every setting a service has after it is created, other than its image, its mounts, its labels and its backup schedule, each of which has a command of its own, is changed with `dokku %s lollipop <key> <value>`, and cleared by giving no value. "+
			"`dokku %s lollipop` reports each of them, with a credential reported as a fingerprint rather than its value. "+
			"A setting the container is made with only reaches it once the container is built again, and `set` warns when that is the case: "+
			"`dokku %s` keeps the container the service has, so use `dokku %s` and then `dokku %s`. "+
			"Passing `--allow-restart` to `set` does that, or the `reexpose` an expose setting takes, whenever the value changed, and leaves the service alone when it did not.", data.Command("set"), data.Command("info"), data.Command("restart"), data.Command("stop"), data.Command("start")),
		strings.Join(table, "\n"),
	}

	if data.Password {
		sections = append(sections, readmePasswords(data))
	}

	return sections
}

// readmePasswords says how a password is changed, which unlike every other
// setting reaches the datastore and the linked apps rather than only the file it
// is kept in
func readmePasswords(data DocumentationData) string {
	keys := "The `password` key is"
	if data.RootPassword {
		keys = "The `password` and `root-password` keys are"
	}

	restart := "The datastore is told the new password while it runs, so the service has to be running."
	if data.PasswordRestarts {
		restart = "The datastore only reads the password as it starts, so changing it stops and starts the service, and `set` refuses to unless it is given `--allow-restart`."
	}

	return fmt.Sprintf("%s changed in the datastore as well as on disk, and every linked app is given the new url and restarted, unless `--no-restart` is passed. "+
		"%s A password cannot be cleared, may only hold letters, digits, `.`, `_`, `~` and `-`, and is never printed back: `info` reports a fingerprint of it.", keys, restart)
}

// documentsProperty is whether the readme lists a property for the datastore,
// which it does not for one the datastore has no use for
func documentsProperty(property SettableProperty, data DocumentationData) bool {
	switch {
	case property.Backup:
		return data.Backups
	case property.Name == service.ExportArgsProperty:
		return data.ExportArgs
	case property.Name == service.ImportArgsProperty:
		return data.ImportArgs
	case property.Name == service.VolumeTargetsProperty:
		return data.VolumeKey != ""
	case property.Name == "password":
		return data.Password
	case property.Name == "root-password":
		return data.RootPassword
	case property.Name == service.ServiceMemoryProperty:
		return data.ServiceMemory
	}

	return true
}

// readmeExposeLimits explains how to limit where an exposed service is
// published and which clients reach it, and where the client limit falls short
func readmeExposeLimits(data DocumentationData) []string {
	return []string{
		"### Limiting where and to whom a service is exposed",
		fmt.Sprintf("An exposed service's ports are published on every interface unless they are given an address of their own. "+
			"To publish them on one address instead, set the service's `port-bind-address` property with `dokku %s`, and to accept connections only from clients in one IP address or CIDR, set its `port-source-range` property. "+
			"Either reaches a running service with `dokku %s`, which leaves the service running when its ports are published through an ambassador.", data.Command("set"), data.Command("reexpose")),
		"Only one source range can be given. The range is checked against the address a connection reaches the service from, which for a connection to the exposed port on the loopback interface, or an IPv6 connection to a service network without IPv6, is the docker network's gateway rather than the client, " +
			"so with a range that leaves the gateway out, connecting to `127.0.0.1` from the dokku host itself is refused.",
	}
}

// readmeExposeMode explains publishing an exposed service's ports on its own
// container rather than through an ambassador, and what that costs
func readmeExposeMode(data DocumentationData) []string {
	return []string{
		"### Exposing a service without an ambassador",
		"An exposed service's ports are published by an ambassador, a container that relays every connection on to the service. " +
			"The ambassador can be replaced without touching the service and can hold clients to a `port-source-range`, but relaying adds latency to every request.",
		fmt.Sprintf("To publish the ports on the service container itself instead, set the service's `expose-mode` property to `direct` with `dokku %s`. "+
			"Docker has no way of changing the ports a container publishes, so the container is made again whenever what it publishes changes: when the service is exposed or unexposed, when its `port-bind-address` changes, and when it moves between expose modes. "+
			"For a running service, `dokku %s`, `dokku %s` and `dokku %s` ask before stopping and starting it, and change nothing if the answer is no. Pass `--force` to stop and start it without being asked. "+
			"A change also reaches the service the next time it is restarted, or stopped and started.", data.Command("set"), data.Command("expose"), data.Command("unexpose"), data.Command("reexpose")),
		"A `port-source-range` cannot be enforced on a port the service container publishes itself, so it cannot be set on a service exposed directly, and a service with one cannot be exposed directly.",
	}
}

// readmeExposedDsn explains the dsn a client off the host connects to an
// exposed service with, and where its host comes from
func readmeExposedDsn(data DocumentationData) []string {
	return []string{
		"### Connecting to an exposed service from outside the host",
		fmt.Sprintf("`dokku %s lollipop --exposed-dsn` prints the dsn a client off the dokku host connects with. It is the dsn a linked app is handed, with the exposed ports in place of the container's and a public host in place of the service container's name, so it carries the same credentials. "+
			"The host is the service's `expose-host` property, set with `dokku %s`, or the first global domain when it has none. "+
			"The `port-bind-address`, or an address given with a port, is never used as the host, since it is where the port is bound rather than where a client elsewhere reaches it. "+
			"The dsn is empty until the service is exposed and there is a host to name.", data.Command("info"), data.Command("set")),
	}
}

// readmeExtraArgs explains how to pass the dump and load tools arguments of
// their own, for a datastore whose tools take them
func readmeExtraArgs(data DocumentationData) []string {
	verbs := []string{}
	properties := []string{}
	if data.ExportArgs {
		verbs = append(verbs, "`export`")
		properties = append(properties, "`export-args`")
	}
	if data.ImportArgs {
		verbs = append(verbs, "`import`")
		properties = append(properties, "`import-args`")
	}

	if len(verbs) == 0 {
		return nil
	}

	return []string{
		"### Passing extra arguments to export and import",
		fmt.Sprintf("Arguments given to %s after `--` are appended to the ones the datastore's own tool is run with, for that run alone. "+
			"To use them every time, set the service's %s property with `dokku %s`, giving the value after `--` so that its leading dash is not read as a flag. "+
			"The property is split the way a shell would split it, so an argument with a space in it is quoted, and a variable in it is refused rather than expanded.", strings.Join(verbs, " or "), strings.Join(properties, " or "), data.Command("set")),
		"Arguments given after `--` replace the property rather than adding to it. " +
			"Backups and clones are made with the property, and a clone is given the source's.",
	}
}

// readmeVolumeTargets explains how to mount a service's volumes somewhere other
// than where the definition does, for a datastore that mounts any, and what
// that does not do
func readmeVolumeTargets(data DocumentationData) []string {
	if len(data.Volumes) == 0 {
		return nil
	}

	// one block, since the sections are joined by blank lines and a table
	// broken by one is not a table
	table := []string{
		"| Definition | Volume | Mounted at |",
		"| --- | --- | --- |",
	}
	for _, volume := range data.Volumes {
		table = append(table, fmt.Sprintf("| %s | %s | `%s` |", volume.Definition, volume.Key, volume.Target))
	}

	return []string{
		"### Moving where a service's volumes are mounted",
		fmt.Sprintf("Each volume a service mounts is named by the directory it lives in under the service's own directory, and is mounted where the datastore's definition says. "+
			"To mount one somewhere else in the container, for an image that keeps its data at another path, set the service's `volume-targets` property with `dokku %s`, or pass `--volume-target` to `create`, `clone` or `upgrade`. "+
			"Each is written as `<volume>=<container-path>`, several separated by spaces, and `dokku %s lollipop --volume-targets` shows the ones a service moved.", data.Command("set"), data.Command("info")),
		strings.Join(table, "\n"),
		"Moving a volume changes where it is mounted, not where the image reads and writes. " +
			"The datastore's own commands and the paths it is started with follow the volume, but an image that keeps writing to its own path writes into the container rather than into the volume, and what it writes is lost when the container is rebuilt, so only move a volume to where the image expects its data. " +
			fmt.Sprintf("The data stays in the same directory on the host, and a move reaches the container the next time one is built, so use `dokku %s` and then `dokku %s` on a running service. ", data.Command("stop"), data.Command("start")) +
			"An upgrade onto a definition that does not mount a volume the service moved is refused until the move is cleared or replaced.",
	}
}

// readmeWaitTimeout explains how long a service is waited on to become ready,
// and how to wait longer for one on a slow host
func readmeWaitTimeout(data DocumentationData) []string {
	return []string{
		"### Waiting for a service to become ready",
		"A service is waited on until it answers on its port after it is created, cloned, started, restarted, upgraded or exposed. " +
			"If it takes longer than that to start - on a slow host, or with an image that does more on its first boot - the command fails with `ERROR: unable to connect`.",
		fmt.Sprintf("To wait longer for every %s service on the host, set the `wait-timeout` global property to a number of seconds with `dokku %s --global wait-timeout 120`. "+
			"To wait longer for a single service, set its `wait-timeout` property with `dokku %s` or pass `--wait-timeout` to `create`, `clone` or `upgrade`. "+
			"The service's own setting is used first, then the global property, then the datastore's default.", data.CommandPrefix, data.Command("set"), data.Command("set")),
	}
}

// readmeServiceMemory explains the memory a datastore is told to use for
// itself, for a datastore that can be told it
func readmeServiceMemory(data DocumentationData) []string {
	if !data.ServiceMemory {
		return nil
	}

	given := fmt.Sprintf("A service is created without one unless `--service-memory` is passed, which leaves the size to %s.", data.Title)
	if data.ServiceMemoryDefault > 0 {
		given = fmt.Sprintf("A service is created with %d megabytes unless `--service-memory` is passed, and that value is kept for every container the service is given afterwards.", data.ServiceMemoryDefault)
	}

	takes := fmt.Sprintf("A change reaches the container the next time one is built, so use `dokku %s` and then `dokku %s`, or pass `--allow-restart` to `set`.", data.Command("stop"), data.Command("start"))
	if data.ServiceMemoryLive {
		takes = "A change is told to a running service at once, without its container being built again, and a stopped service takes it when it is next started."
	}

	return []string{
		"### Datastore memory",
		fmt.Sprintf("The memory %s itself is told to use, in megabytes, is the service's `service-memory`. "+
			"It is set with `--service-memory` when a service is created or upgraded, changed with `dokku %s lollipop service-memory <megabytes>`, cleared by giving no value, and reported by `dokku %s lollipop --service-memory`. ", data.Title, data.Command("set"), data.Command("info")) +
			given + " " + takes,
		"It is separate from the container's `memory` limit, which is unlimited unless it is set. " +
			"When both are set, keep the service memory below the limit, since a datastore that grows past the limit has its container killed.",
	}
}

// readmeReservedNames lists the service names create and clone refuse, for a
// datastore that keeps databases of its own
func readmeReservedNames(data DocumentationData) []string {
	if len(data.ReservedNames) == 0 {
		return nil
	}

	names := make([]string, 0, len(data.ReservedNames))
	for _, name := range data.ReservedNames {
		names = append(names, "`"+name+"`")
	}
	sort.Strings(names)

	return []string{
		"### Reserved service names",
		fmt.Sprintf("A service's database is named after the service, with hyphens replaced by underscores. "+
			"So that an app is never handed a database %s keeps for itself, `dokku %s` and `dokku %s` refuse a name that is, or whose database would be, one of %s, in any case. "+
			"A service that already has such a name is not affected.", data.Title, data.Command("create"), data.Command("clone"), strings.Join(names, ", ")),
	}
}

// readmeDefinitionSections are the sections a datastore's definitions add, for
// what it does that none of its commands explain. Each is written the way a
// command's documentation is, so it renders the same way.
func readmeDefinitionSections(data DocumentationData) ([]string, error) {
	sections := []string{}
	for _, section := range data.Sections {
		title, err := RenderDocumentation(section.Title, data)
		if err != nil {
			return nil, err
		}

		body, err := RenderDocumentation(section.Body, data)
		if err != nil {
			return nil, err
		}

		sections = append(sections, "### "+strings.TrimSpace(title))
		sections = append(sections, readmeDocumentationBlocks(body)...)
	}

	return sections, nil
}

// sortedCommands returns the commands in the order the readme lists them
func sortedCommands(commands []PluginCommand) []PluginCommand {
	sorted := make([]PluginCommand, len(commands))
	copy(sorted, commands)
	sort.Slice(sorted, func(i int, j int) bool {
		return sorted[i].Name() < sorted[j].Name()
	})

	return sorted
}

// commandsInGroup returns the commands a readme usage section documents, in the
// order the section declares.
//
// A command the section does not name sorts after every one it does, and
// alphabetically among its peers. That is what keeps a newly added command from
// silently reordering the section around it, and what places a custom command
// after the built-ins of whichever section it declares.
func commandsInGroup(commands []PluginCommand, group string, order []string) []PluginCommand {
	position := map[string]int{}
	for index, name := range order {
		position[name] = index
	}

	matching := []PluginCommand{}
	for _, c := range commands {
		if c.Group() == group {
			matching = append(matching, c)
		}
	}

	placed := func(c PluginCommand) int {
		if index, ok := position[c.Name()]; ok {
			return index
		}

		return len(order)
	}

	sort.SliceStable(matching, func(i int, j int) bool {
		left, right := placed(matching[i]), placed(matching[j])
		if left != right {
			return left < right
		}

		return matching[i].Name() < matching[j].Name()
	})

	return matching
}

// possessive is an apostrophe that turning quotes into backticks caught, which
// follows a letter where the quote opening inline code follows a space. Matching
// on the s alone turned the opening quote of 'sslmode=require' back into one.
var possessive = regexp.MustCompile("([\\p{L}\\p{N}])`s\\b")

// processSentence turns a paragraph of the terminal oriented documentation into
// markdown prose: sentences are capitalized, acronyms and variable names are
// quoted, and the single quotes the annotations used become backticks.
func processSentence(lines []string) string {
	text := strings.Join(lines, " ")

	pieces := strings.Split(text, ". ")
	for i, piece := range pieces {
		pieces[i] = upperFirst(strings.TrimSpace(piece))
	}

	text = strings.TrimSpace(strings.Join(pieces, ". "))
	if !strings.HasSuffix(text, ".") && !strings.HasSuffix(text, ":") {
		text += ":"
	}

	pieces = strings.Split(text, ". ")
	for i, piece := range pieces {
		words := strings.Split(strings.TrimSpace(piece), " ")
		for j, word := range words {
			words[j] = quoteAcronym(word)
		}
		pieces[i] = strings.Join(words, " ")
	}

	text = strings.Join(pieces, ". ")
	text = strings.ReplaceAll(text, "(0.0.0.0)", "(`0.0.0.0`)")
	text = strings.ReplaceAll(text, "'", "`")
	text = possessive.ReplaceAllString(text, "$1's")
	text = strings.ReplaceAll(text, "``", "`")

	return strings.TrimSpace(text)
}

// upperFirst capitalizes the first letter of a sentence
func upperFirst(text string) string {
	for i, char := range text {
		return string(unicode.ToUpper(char)) + text[i+len(string(char)):]
	}

	return text
}

// quoteAcronym wraps an all caps word in backticks, so that the acronyms and
// environment variable names the documentation mentions read as code
func quoteAcronym(word string) string {
	if len(word) <= 1 || !isUpper(word) {
		return word
	}

	for _, ending := range []string{":", "."} {
		if strings.HasSuffix(word, ending) {
			return "`" + strings.TrimSuffix(word, ending) + "`" + ending
		}
	}

	return "`" + word + "`"
}

// isUpper reports whether a word has at least one letter and no lowercase ones
func isUpper(word string) bool {
	cased := false
	for _, char := range word {
		if unicode.IsLower(char) {
			return false
		}

		if unicode.IsUpper(char) {
			cased = true
		}
	}

	return cased
}
