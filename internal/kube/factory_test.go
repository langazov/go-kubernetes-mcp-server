package kube

import (
	"path/filepath"
	"testing"

	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

func writeKubeconfig(t *testing.T, path string) {
	t.Helper()
	cfg := api.NewConfig()
	cfg.Clusters["test-cluster"] = &api.Cluster{
		Server:                   "https://127.0.0.1:6443",
		CertificateAuthorityData: []byte("ca-bytes"),
	}
	cfg.Contexts["ctx"] = &api.Context{Cluster: "test-cluster", AuthInfo: "user"}
	cfg.AuthInfos["user"] = &api.AuthInfo{Token: "token-value"}
	cfg.CurrentContext = "ctx"
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatalf("WriteToFile: %v", err)
	}
}

func TestBuildRESTConfigFromKubeconfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	writeKubeconfig(t, path)

	cfg := config.Defaults()
	cfg.Kubeconfig = path
	cfg.QPS = 42
	cfg.Burst = 99

	rc, name, err := BuildRESTConfig(cfg)
	if err != nil {
		t.Fatalf("BuildRESTConfig: %v", err)
	}
	if rc.Host != "https://127.0.0.1:6443" {
		t.Errorf("Host = %q, want https://127.0.0.1:6443", rc.Host)
	}
	if rc.QPS != 42 || rc.Burst != 99 {
		t.Errorf("limits not applied: QPS=%v Burst=%v", rc.QPS, rc.Burst)
	}
	if name != "test-cluster" {
		t.Errorf("cluster name = %q, want test-cluster", name)
	}
}

func TestBuildRESTConfigExplicitClusterNameWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	writeKubeconfig(t, path)

	cfg := config.Defaults()
	cfg.Kubeconfig = path
	cfg.ClusterName = "override"
	_, name, err := BuildRESTConfig(cfg)
	if err != nil {
		t.Fatalf("BuildRESTConfig: %v", err)
	}
	if name != "override" {
		t.Errorf("cluster name = %q, want override", name)
	}
}

func TestBuildRESTConfigContextOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	writeKubeconfig(t, path)

	cfg := config.Defaults()
	cfg.Kubeconfig = path
	cfg.Context = "ctx"
	_, _, err := BuildRESTConfig(cfg)
	if err != nil {
		t.Fatalf("explicit context should resolve: %v", err)
	}
}

func TestBuildRESTConfigMissingFileErrors(t *testing.T) {
	cfg := config.Defaults()
	cfg.Kubeconfig = filepath.Join(t.TempDir(), "does-not-exist")
	if _, _, err := BuildRESTConfig(cfg); err == nil {
		t.Error("expected an error for a missing kubeconfig path")
	}
}

func TestNewClientsRejectsNilConfig(t *testing.T) {
	if _, err := NewClients(nil); err == nil {
		t.Error("NewClients(nil) must return an error")
	}
}

func TestResolveClusterNameFallback(t *testing.T) {
	if got := resolveClusterName(config.Config{}, ""); got != "kubernetes" {
		t.Errorf("empty everything should fall back to 'kubernetes', got %q", got)
	}
	if got := resolveClusterName(config.Config{}, "fallback"); got != "fallback" {
		t.Errorf("fallback should be used when no explicit name, got %q", got)
	}
	if got := resolveClusterName(config.Config{ClusterName: "explicit"}, "fallback"); got != "explicit" {
		t.Errorf("explicit name should win, got %q", got)
	}
}

func TestActiveContextNameOverride(t *testing.T) {
	raw := api.Config{CurrentContext: "default-ctx"}
	if got := activeContextName(raw, ""); got != "default-ctx" {
		t.Errorf("no override: got %q", got)
	}
	if got := activeContextName(raw, "overridden"); got != "overridden" {
		t.Errorf("override should win: got %q", got)
	}
}
