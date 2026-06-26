package kube

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/langazov/go-kubernetes-mcp-server/internal/config"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/homedir"
)

// BuildRESTConfig resolves a *rest.Config from (in order):
//  1. explicit --kubeconfig path,
//  2. in-cluster service account (when running inside a pod),
//  3. default kubeconfig location (~/.kube/config).
//
// It applies QPS/burst limits and an optional named context override.
func BuildRESTConfig(cfg config.Config) (*rest.Config, string, error) {
	kubeconfigPath := cfg.Kubeconfig
	if kubeconfigPath == "" {
		// Try in-cluster first only when no kubeconfig is explicitly given and we
		// appear to be running in a pod.
		if c, err := rest.InClusterConfig(); err == nil {
			applyLimits(c, cfg)
			return c, resolveClusterName(cfg, "in-cluster"), nil
		}
		kubeconfigPath = defaultKubeconfigPath()
	}

	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{CurrentContext: cfg.Context},
	)

	rc, err := loader.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("load kubeconfig %q: %w", kubeconfigPath, err)
	}

	raw, err := loader.RawConfig()
	if err == nil && cfg.ClusterName == "" {
		active := activeContextName(raw, cfg.Context)
		if ctx, ok := raw.Contexts[active]; ok && ctx.Cluster != "" {
			cfg.ClusterName = ctx.Cluster
		}
	}

	applyLimits(rc, cfg)
	return rc, resolveClusterName(cfg, activeContextName(raw, cfg.Context)), nil
}

func applyLimits(c *rest.Config, cfg config.Config) {
	if cfg.QPS > 0 {
		c.QPS = float32(cfg.QPS)
	}
	if cfg.Burst > 0 {
		c.Burst = cfg.Burst
	}
}

func defaultKubeconfigPath() string {
	if home := homedir.HomeDir(); home != "" {
		p := filepath.Join(home, ".kube", "config")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func activeContextName(raw api.Config, override string) string {
	if override != "" {
		return override
	}
	return raw.CurrentContext
}

func resolveClusterName(cfg config.Config, fallback string) string {
	if cfg.ClusterName != "" {
		return cfg.ClusterName
	}
	if fallback != "" {
		return fallback
	}
	return "kubernetes"
}
