package rpc

import (
	"fmt"
	"regexp"
	"strings"
)

const defaultNamespace = "default"

// ResolveNamespace returns the namespace to use for a namespaced resource,
// defaulting to "default" when empty.
func ResolveNamespace(ns string) string {
	if ns == "" {
		return defaultNamespace
	}
	return ns
}

var (
	// dnsSubdomain matches a DNS-1123 subdomain: lowercase alphanumeric and '-',
	// 1-253 chars, segments separated by '.'.
	dnsSubdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	// dnsLabel: 1-63 chars, no dots.
	dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// ValidateName validates a Kubernetes resource name (DNS subdomain).
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > 253 || !dnsSubdomain.MatchString(name) {
		return fmt.Errorf("invalid name %q: must be a DNS subdomain (lowercase, max 253 chars)", name)
	}
	return nil
}

// ValidateNamespace validates a namespace name (DNS label, max 63 chars).
// An empty namespace is allowed (it will be resolved later).
func ValidateNamespace(ns string) error {
	if ns == "" {
		return nil
	}
	if len(ns) > 63 || !dnsLabel.MatchString(ns) {
		return fmt.Errorf("invalid namespace %q: must be a DNS label (lowercase, max 63 chars)", ns)
	}
	return nil
}

// ValidateLabelSelector performs a lightweight sanity check on a label selector.
// It rejects characters that are obviously malformed; Kubernetes performs the
// authoritative parse.
func ValidateLabelSelector(sel string) error {
	if sel == "" {
		return nil
	}
	if strings.ContainsAny(sel, "\n\r") {
		return fmt.Errorf("invalid label selector: must not contain newlines")
	}
	return nil
}

// ValidateSelectorToken rejects selector metacharacters in a value that will be
// interpolated into a Kubernetes field selector (e.g. an involved-object kind or
// name), preventing a caller from injecting extra selector clauses.
func ValidateSelectorToken(v string) error {
	if v == "" {
		return nil
	}
	if strings.ContainsAny(v, ",=!<>()\"'\\ \t\n\r") {
		return fmt.Errorf("value %q contains characters that are not allowed in a selector token", v)
	}
	return nil
}

// PrivilegedNamespaces are namespaces hosting control-plane components that
// require explicit opt-in to touch.
var PrivilegedNamespaces = map[string]bool{
	"kube-system":     true,
	"kube-public":     true,
	"kube-node-lease": true,
}

// IsPrivilegedNamespace reports whether ns is a control-plane namespace.
func IsPrivilegedNamespace(ns string) bool {
	return PrivilegedNamespaces[ns]
}
