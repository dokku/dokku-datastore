package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/dokku/dokku/plugins/common"
)

// LabelsProperty holds the docker labels a service's container is made with
// beyond the plugin's own, as a json object.
//
// Json rather than the comma joined list the other list properties are kept
// in, because a label's value is often a comma separated list of its own, such
// as a list of traefik middlewares. Read at the moment a container is made, like
// the mounts, so a change lands on the next container rather than on the
// running one.
const LabelsProperty = "labels"

// reservedLabelPrefixes are the label namespaces a service may not set. The
// plugin finds its containers by the labels it puts on them, and dokku core
// keeps com.dokku for its own, so a label in either would be one that a service
// could use to hide or impersonate a container.
var reservedLabelPrefixes = []string{"dokku.", "com.dokku."}

// reservedLabels are the label keys a service may not set, beyond the prefixes
// above
var reservedLabels = map[string]bool{"dokku": true}

// ParseLabel reads a label given as <key>=<value>, split on the first equals
// sign so a value may contain one. An empty value is a label docker takes.
func ParseLabel(spec string) (string, string, error) {
	key, value, found := strings.Cut(spec, "=")
	if !found {
		return "", "", fmt.Errorf("invalid label %q, must be written as <key>=<value>", spec)
	}

	if err := ValidateLabel(key, value); err != nil {
		return "", "", err
	}

	return key, value, nil
}

// ParseLabels reads the labels a --label flag or the label command was given.
// An empty spec is skipped rather than refused, so --label "" asks for none, and
// a key given twice is refused rather than having one of the two quietly win.
func ParseLabels(specs []string) (map[string]string, error) {
	labels := map[string]string{}
	for _, spec := range specs {
		if spec == "" {
			continue
		}

		key, value, err := ParseLabel(spec)
		if err != nil {
			return nil, err
		}

		if _, ok := labels[key]; ok {
			return nil, fmt.Errorf("invalid label %q, the key %s is given more than once", spec, key)
		}

		labels[key] = value
	}

	return labels, nil
}

// ValidateLabel reports whether a key and value make a label the service may
// be given.
//
// The key is checked for what would make it unreadable or someone else's: it
// may not be empty, contain whitespace or an equals sign, or sit in a namespace
// the plugin or dokku keeps for itself. The value is the label's business and
// may hold anything that fits on one line.
func ValidateLabel(key string, value string) error {
	if key == "" {
		return fmt.Errorf("invalid label, the key must not be empty")
	}

	if strings.ContainsRune(key, '=') || strings.IndexFunc(key, unicode.IsSpace) != -1 {
		return fmt.Errorf("invalid label key %q, must not contain whitespace or an equals sign", key)
	}

	if reservedLabels[key] {
		return fmt.Errorf("invalid label key %q, it is reserved for dokku", key)
	}

	for _, prefix := range reservedLabelPrefixes {
		if strings.HasPrefix(key, prefix) {
			return fmt.Errorf("invalid label key %q, keys starting with %s are reserved for dokku", key, prefix)
		}
	}

	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("invalid value for the label %s, must not contain a newline", key)
	}

	return nil
}

// ValidateLabels reports whether every label in a set is one the service may be
// given, in key order so the same set always fails on the same label
func ValidateLabels(labels map[string]string) error {
	for _, key := range sortedLabelKeys(labels) {
		if err := ValidateLabel(key, labels[key]); err != nil {
			return err
		}
	}

	return nil
}

// ServiceLabels are the labels a service was given, beyond the plugin's own
func ServiceLabels(s *Datastore, serviceName string) (map[string]string, error) {
	value := strings.TrimSpace(common.PropertyGet(s.Properties().CommandPrefix, serviceName, LabelsProperty))
	if value == "" {
		return nil, nil
	}

	labels := map[string]string{}
	if err := json.Unmarshal([]byte(value), &labels); err != nil {
		return nil, fmt.Errorf("unable to read the %s property of %s: %w", LabelsProperty, serviceName, err)
	}

	return labels, nil
}

// WriteLabels records the labels a service is given, and clears the property
// when there are none rather than leaving an empty object behind
func WriteLabels(s *Datastore, serviceName string, labels map[string]string) error {
	commandPrefix := s.Properties().CommandPrefix
	if len(labels) == 0 {
		if err := common.PropertyDelete(commandPrefix, serviceName, LabelsProperty); err != nil {
			return fmt.Errorf("failed to clear the %s property: %w", LabelsProperty, err)
		}

		return nil
	}

	value, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("unable to encode the %s property: %w", LabelsProperty, err)
	}

	if err := common.PropertyWrite(commandPrefix, serviceName, LabelsProperty, string(value)); err != nil {
		return fmt.Errorf("failed to write the %s property: %w", LabelsProperty, err)
	}

	return nil
}

// FormatLabels is how info reports a set of labels: a json object, empty for a
// service with none. A label's value may hold the spaces and commas the other
// list settings are reported with, so a joined list could not be read back.
// encoding/json sorts the keys, so the same labels always read the same, and
// html escaping is left off so a traefik rule's && reads as it was given.
func FormatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	value := bytes.Buffer{}
	encoder := json.NewEncoder(&value)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(labels); err != nil {
		return ""
	}

	return strings.TrimSuffix(value.String(), "\n")
}

// sortedLabelKeys are the keys of a set of labels, sorted
func sortedLabelKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}
