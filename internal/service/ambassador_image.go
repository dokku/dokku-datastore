package service

import (
	"fmt"
	"os"
	"strings"

	"github.com/distribution/reference"
	"github.com/dokku/dokku-datastore/internal/hostenv"
)

// AmbassadorImageProperty is the global property naming the image the
// ambassador an exposed service runs is made from. Empty leaves it to the
// default the plugin pins.
//
// Read when the ambassador is made, like the port bind address, so a change
// reaches a running service on the next reexpose, or whenever the ambassador
// is next replaced.
const AmbassadorImageProperty = "ambassador-image"

// ValidateAmbassadorImage reports whether a value is a usable ambassador
// image. An empty value is valid and means unset.
//
// The image has to name a tag or a digest. Every image the plugin runs is
// pinned, and one that floats would change what publishes a service's ports
// on whatever pull next fetches it.
func ValidateAmbassadorImage(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	named, err := reference.ParseNormalizedNamed(value)
	if err != nil {
		return fmt.Errorf("invalid %s value %q: %w", AmbassadorImageProperty, value, err)
	}

	if _, tagged := named.(reference.Tagged); tagged {
		return nil
	}

	if _, digested := named.(reference.Digested); digested {
		return nil
	}

	return fmt.Errorf("invalid %s value %q, must name a tag or a digest, such as %s", AmbassadorImageProperty, value, hostenv.DefaultAmbassadorImage)
}

// AmbassadorImage is the image a service's ambassador is made from.
//
// The variable is checked first. The --ambassador-image flag sets it for the
// rest of the process, so a flag given to a command wins, and otherwise it is
// whatever the host exported, which only reaches the binary where sudo keeps
// the caller's environment. The global property comes next, and then the
// image the plugin pins. A malformed value is an error rather than skipped,
// since skipping it would publish the service through an image the operator
// never asked for.
func AmbassadorImage(s *Datastore) (string, error) {
	if value := strings.TrimSpace(os.Getenv(hostenv.AmbassadorImageVariable)); value != "" {
		if err := ValidateAmbassadorImage(value); err != nil {
			return "", fmt.Errorf("unable to read the %s environment variable: %w", hostenv.AmbassadorImageVariable, err)
		}

		return value, nil
	}

	if value := strings.TrimSpace(GlobalProperty(s, AmbassadorImageProperty)); value != "" {
		if err := ValidateAmbassadorImage(value); err != nil {
			return "", fmt.Errorf("unable to read the global %s property: %w", AmbassadorImageProperty, err)
		}

		return value, nil
	}

	return hostenv.DefaultAmbassadorImage, nil
}
