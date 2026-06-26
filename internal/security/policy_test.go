package security

import (
	"testing"

	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
)

func fromDefaults(t *testing.T, mutate func(*config.Config)) *Policy {
	t.Helper()
	c := config.Defaults()
	if mutate != nil {
		mutate(&c)
	}
	return FromConfig(c)
}

func TestReadOnlyPolicyBlocksMutations(t *testing.T) {
	p := fromDefaults(t, nil)
	if p.CanMutate() || p.CanDestroy() || p.CanDebug() {
		t.Fatal("read-only policy must block all mutations")
	}
	if err := p.CheckMutating(); err == nil {
		t.Error("CheckMutating should error")
	}
	if err := p.CheckDestructive(); err == nil {
		t.Error("CheckDestructive should error")
	}
}

func TestNamespaceAllowlist(t *testing.T) {
	p := fromDefaults(t, func(c *config.Config) { c.Namespaces = []string{"team-a", "team-b"} })
	if err := p.CheckNamespace("team-a"); err != nil {
		t.Errorf("team-a should be allowed: %v", err)
	}
	if err := p.CheckNamespace("default"); err == nil {
		t.Error("default should be blocked")
	}
}

func TestNoAllowlistPermitsAll(t *testing.T) {
	p := fromDefaults(t, nil)
	if err := p.CheckNamespace("anything"); err != nil {
		t.Errorf("empty allowlist should allow all: %v", err)
	}
}

func TestPrivilegedTargetGuard(t *testing.T) {
	p := fromDefaults(t, nil)
	if err := p.CheckTarget("kube-system", false); err == nil {
		t.Error("kube-system should be blocked without privileged flag")
	}
	if err := p.CheckTarget("", true); err == nil {
		t.Error("cluster-scoped should be blocked without privileged flag")
	}

	priv := fromDefaults(t, func(c *config.Config) { c.AllowPrivilegedTargets = true })
	if err := priv.CheckTarget("kube-system", false); err != nil {
		t.Errorf("privileged flag should allow kube-system: %v", err)
	}
	if err := priv.CheckTarget("", true); err != nil {
		t.Errorf("privileged flag should allow cluster-scoped: %v", err)
	}
}

func TestSecretRevealRequiresFlag(t *testing.T) {
	p := fromDefaults(t, nil)
	if err := p.CheckSecretReveal(true); err == nil {
		t.Error("reveal should be blocked without flag")
	}
	if err := p.CheckSecretReveal(false); err != nil {
		t.Errorf("non-reveal should always pass: %v", err)
	}

	reveal := fromDefaults(t, func(c *config.Config) { c.RevealSecrets = true })
	if err := reveal.CheckSecretReveal(true); err != nil {
		t.Errorf("reveal with flag should pass: %v", err)
	}
}

func TestManifestKindAllowlist(t *testing.T) {
	p := fromDefaults(t, func(c *config.Config) {
		c.AllowedManifestKinds = []string{"Deployment.v1.apps"}
	})
	if err := p.CheckManifestKind("Deployment.v1.apps"); err != nil {
		t.Errorf("allowed kind rejected: %v", err)
	}
	if err := p.CheckManifestKind("Pod.v1."); err == nil {
		t.Error("non-allowed kind should be rejected")
	}
}
