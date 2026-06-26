// Package security centralizes blast-radius policy: whether mutating, destructive,
// and debug tools are permitted, namespace allowlists, and privileged-target
// guards. It is consulted at tool registration time AND inside each mutating
// handler (defense in depth).
package security

import (
	"fmt"

	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
)

// Policy captures the effective security posture of a running server.
type Policy struct {
	AllowWrites            bool
	AllowDestructive       bool
	AllowDebug             bool
	AllowPrivilegedTargets bool
	Namespaces             map[string]bool
	RevealSecrets          bool
	AllowedManifestKinds   map[string]bool
	ForbiddenImages        map[string]bool
}

// FromConfig builds a Policy from configuration. It applies the same
// implications as config.Validate (destructive implies writes) so the policy is
// correct even if Validate was not called.
func FromConfig(cfg config.Config) *Policy {
	p := &Policy{
		AllowWrites:            cfg.AllowWrites || cfg.AllowDestructive,
		AllowDestructive:       cfg.AllowDestructive,
		AllowDebug:             cfg.AllowDebug,
		AllowPrivilegedTargets: cfg.AllowPrivilegedTargets,
		RevealSecrets:          cfg.RevealSecrets,
		Namespaces:             map[string]bool{},
		AllowedManifestKinds:   map[string]bool{},
		ForbiddenImages:        map[string]bool{},
	}
	for _, n := range cfg.Namespaces {
		p.Namespaces[n] = true
	}
	for _, k := range cfg.AllowedManifestKinds {
		p.AllowedManifestKinds[k] = true
	}
	for _, img := range cfg.ForbiddenImages {
		p.ForbiddenImages[img] = true
	}
	return p
}

// Verb classifies a tool call for auditing.
type Verb string

const (
	VerbRead        Verb = "read"
	VerbWrite       Verb = "write"
	VerbDestructive Verb = "destructive"
	VerbDebug       Verb = "debug"
)

// CanMutate reports whether mutating tools are registered/usable.
func (p *Policy) CanMutate() bool { return p.AllowWrites }

// CanDestroy reports whether destructive tools are registered/usable.
func (p *Policy) CanDestroy() bool { return p.AllowDestructive }

// CanDebug reports whether debug tools are registered/usable.
func (p *Policy) CanDebug() bool { return p.AllowDebug }

// CheckMutating returns an error if writes are not enabled.
func (p *Policy) CheckMutating() error {
	if !p.AllowWrites {
		return fmt.Errorf("mutating operations are disabled (start the server with --allow-writes)")
	}
	return nil
}

// CheckDestructive returns an error if destructive ops are not enabled.
func (p *Policy) CheckDestructive() error {
	if !p.AllowDestructive {
		return fmt.Errorf("destructive operations are disabled (start the server with --allow-destructive)")
	}
	return nil
}

// CheckDebug returns an error if debug ops are not enabled.
func (p *Policy) CheckDebug() error {
	if !p.AllowDebug {
		return fmt.Errorf("debug operations are disabled (start the server with --allow-debug)")
	}
	return nil
}

// CheckNamespace returns an error if the namespace is outside the configured
// allowlist. An empty allowlist permits all namespaces.
func (p *Policy) CheckNamespace(ns string) error {
	if len(p.Namespaces) == 0 {
		return nil
	}
	if !p.Namespaces[ns] {
		return fmt.Errorf("namespace %q is outside the configured allowlist", ns)
	}
	return nil
}

// CheckTarget returns an error if a target namespace is privileged and the
// server is not permitted to touch privileged targets. Pass clusterScoped=true
// for cluster-scoped resources (nodes, PVs, clusterroles, ...).
func (p *Policy) CheckTarget(ns string, clusterScoped bool) error {
	if clusterScoped && !p.AllowPrivilegedTargets {
		return fmt.Errorf("cluster-scoped resources require --allow-privileged-targets")
	}
	if ns != "" && rpc.IsPrivilegedNamespace(ns) && !p.AllowPrivilegedTargets {
		return fmt.Errorf("namespace %q is a control-plane namespace and requires --allow-privileged-targets", ns)
	}
	return nil
}

// CheckManifestKind returns an error if a GVK is not in the allowed list. An
// empty allowed list permits all kinds.
func (p *Policy) CheckManifestKind(gvk string) error {
	if len(p.AllowedManifestKinds) == 0 {
		return nil
	}
	if !p.AllowedManifestKinds[gvk] {
		return fmt.Errorf("kind %q is not in the allowed-manifest-kinds list", gvk)
	}
	return nil
}

// CheckImage returns an error if an image is explicitly forbidden.
func (p *Policy) CheckImage(image string) error {
	if p.ForbiddenImages[image] {
		return fmt.Errorf("image %q is forbidden by server policy", image)
	}
	return nil
}

// CheckSecretReveal returns an error if secret reveal is requested but disabled.
func (p *Policy) CheckSecretReveal(requested bool) error {
	if requested && !p.RevealSecrets {
		return fmt.Errorf("secret reveal is disabled (start the server with --reveal-secrets)")
	}
	return nil
}
