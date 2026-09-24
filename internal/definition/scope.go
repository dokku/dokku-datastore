package definition

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
	"text/template"
)

// Scope is what a definition's templates are rendered against. Nothing in it is
// computed by a template: it is assembled in Go from the service's files on disk
// and the datastore's own metadata, so that a value four things read off disk
// cannot differ between them.
type Scope struct {
	// identity
	ServiceName   string
	ContainerName string
	Host          string
	Database      string
	Plugin        string
	Title         string
	Variable      string

	// image
	Image        string
	ImageVersion string
	TaggedImage  string

	// paths. ServiceRoot is what the tool reads; HostRoot is what dockerd sees.
	// They differ on a docker-in-docker install, which is why bind sources must
	// use HostRoot and hook scripts must use ServiceRoot.
	ServiceRoot string
	HostRoot    string

	// values
	Secret         map[string]string
	Port           map[string]int
	Scheme         string
	Memory         string
	ShmSize        string
	InitialNetwork string

	// LogDriver and LogOptions are the docker logging a container is made
	// with, already resolved. Empty means the daemon's own default, which is
	// what every container had before there was anything to say here.
	LogDriver  string
	LogOptions map[string]string

	// RestartPolicy is the docker restart policy a container is made with, as
	// the service set it. Empty means the default, which the renderer applies.
	RestartPolicy string

	// Mounts are the docker -v arguments for the mounts the service was given
	// beyond the definition's own, already rendered. They follow the
	// definition's volumes, so one may sit inside a directory those mount.
	Mounts []string

	// Args are the positional arguments of an extra subcommand.
	Args map[string]string
}

// templateFuncs are the functions a definition may call. Deliberately tiny:
// sprig's full map exposes env and expandenv, and a definition is a file a plugin
// checkout may override, so a third-party definition must not be able to
// interpolate a host credential into a connection string.
var templateFuncs = template.FuncMap{
	// urlescape percent-encodes a value for a url userinfo field, so a password
	// containing a colon or an at sign cannot reshape the dsn
	"urlescape": url.QueryEscape,
}

// Render fills one template in against a scope.
//
// text/template, never html/template. Contextual escaping would rewrite a
// password containing an ampersand or a quote on its way into a connection
// string, and emits the sentinel ZgotmplZ for a substitution it decides is an
// unsafe url, so a perfectly good dsn can render as that literal string.
func Render(body string, scope Scope) (string, error) {
	parsed, err := template.New("definition").Funcs(templateFuncs).Option("missingkey=error").Parse(body)
	if err != nil {
		return "", fmt.Errorf("unable to parse template %q: %w", body, err)
	}

	rendered := bytes.Buffer{}
	if err := parsed.Execute(&rendered, scope); err != nil {
		return "", fmt.Errorf("unable to render template %q: %w", body, err)
	}

	return rendered.String(), nil
}

// RenderAll fills in a list of templates, dropping any that render empty. An
// element that renders to nothing is an element the bash plugins guarded with
// [[ -n "$X" ]], so dropping it is what reproduces their behaviour.
func RenderAll(bodies []string, scope Scope) ([]string, error) {
	rendered := make([]string, 0, len(bodies))
	for _, body := range bodies {
		value, err := Render(body, scope)
		if err != nil {
			return nil, err
		}

		if value == "" {
			continue
		}

		rendered = append(rendered, value)
	}

	return rendered, nil
}

// TemplateNames returns the scope fields a template refers to, so that a
// definition naming a field that does not exist fails at load rather than at
// create time.
func TemplateNames(body string) []string {
	names := []string{}
	for _, fragment := range strings.Split(body, "{{") {
		field, _, found := strings.Cut(fragment, "}}")
		if !found {
			continue
		}

		field = strings.TrimSpace(field)
		if strings.HasPrefix(field, ".") {
			names = append(names, field)
		}
	}

	return names
}
