package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/langazov/go-kubernetes-mcp-server/internal/kube"
	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
)

// toolNames builds a server with the given config and lists its advertised tools
// over an in-memory transport. This drives the security regression suite:
// mutating/destructive/debug tools must be ABSENT unless the relevant flag is set.
func toolNames(t *testing.T, cfg config.Config) []string {
	t.Helper()
	cs := kubefake.NewSimpleClientset()
	cs.Resources = []*metav1.APIResourceList{}
	clients := &kube.Clients{Core: cs, Discovery: cs.Discovery()}

	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := Build(&cfg, clients, security.FromConfig(cfg), discard, audit.NewLogger(discard), "test")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientT, serverT := mcp.NewInMemoryTransports()
	go func() { _ = app.Server.Run(ctx, serverT) }()

	cli := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v"}, nil)
	session, err := cli.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	return names
}

func TestReadOnlyRegistersNoMutatingTools(t *testing.T) {
	names := toolNames(t, config.Defaults())
	mutating := []string{
		"apply_manifest", "scale", "delete_pod", "delete_manifest",
		"cordon_node", "drain_node", "exec_command", "port_forward",
		"run_debug_pod", "create_namespace", "create_service", "check_connectivity",
	}
	_ = mutating
	mutating = []string{
		"apply_manifest", "scale", "delete_pod", "delete_manifest",
		"cordon_node", "drain_node", "exec_command", "port_forward",
		"run_debug_pod", "create_namespace", "create_service",
	}
	for _, m := range mutating {
		if contains(names, m) {
			t.Errorf("read-only server must NOT register %q (got %v)", m, names)
		}
	}
	if !contains(names, "get_logs") || !contains(names, "diagnose_pod") || !contains(names, "check_connectivity") {
		t.Errorf("expected read tools present (incl. check_connectivity), got %v", names)
	}
}

func TestAllowWritesRegistersMutating(t *testing.T) {
	cfg := config.Defaults()
	cfg.AllowWrites = true
	names := toolNames(t, cfg)
	for _, want := range []string{"apply_manifest", "scale", "rollout_restart", "create_namespace", "create_service"} {
		if !contains(names, want) {
			t.Errorf("write mode should register %q (got %v)", want, names)
		}
	}
	for _, banned := range []string{"delete_pod", "drain_node", "exec_command"} {
		if contains(names, banned) {
			t.Errorf("write-only mode must not register %q (got %v)", banned, names)
		}
	}
}

func TestAllowDestructiveRegistersDestructive(t *testing.T) {
	cfg := config.Defaults()
	cfg.AllowDestructive = true
	names := toolNames(t, cfg)
	for _, want := range []string{"delete_pod", "delete_manifest", "cordon_node", "drain_node"} {
		if !contains(names, want) {
			t.Errorf("destructive mode should register %q (got %v)", want, names)
		}
	}
}

func TestAllowDebugRegistersDebug(t *testing.T) {
	cfg := config.Defaults()
	cfg.AllowDebug = true
	names := toolNames(t, cfg)
	for _, want := range []string{"exec_command", "add_ephemeral_container", "port_forward", "run_debug_pod"} {
		if !contains(names, want) {
			t.Errorf("debug mode should register %q (got %v)", want, names)
		}
	}
}

func TestCategoryFilter(t *testing.T) {
	cfg := config.Defaults()
	cfg.EnableCategories = []string{"core"}
	names := toolNames(t, cfg)
	for _, want := range []string{"list_namespaces", "cluster_health"} {
		if !contains(names, want) {
			t.Errorf("core category should include %q (got %v)", want, names)
		}
	}
	if contains(names, "get_logs") || contains(names, "list_pods") {
		t.Errorf("category filter should exclude non-core tools (got %v)", names)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func TestBearerAuthRejectsAndAccepts(t *testing.T) {
	const token = "topsecret"
	srv := httptest.NewServer(bearerAuth(token)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()

	// No credentials -> 401.
	if code := httpStatus(srv.Client(), "GET", srv.URL, ""); code != http.StatusUnauthorized {
		t.Fatalf("missing token: got %d, want 401", code)
	}
	// Wrong token -> 401.
	if code := httpStatus(srv.Client(), "GET", srv.URL, "Bearer nope"); code != http.StatusUnauthorized {
		t.Fatalf("bad token: got %d, want 401", code)
	}
	// Correct token -> 200.
	if code := httpStatus(srv.Client(), "GET", srv.URL, "Bearer "+token); code != http.StatusOK {
		t.Fatalf("good token: got %d, want 200", code)
	}
}

func TestBearerAuthDisabledWhenNoToken(t *testing.T) {
	h := bearerAuth("")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()
	if code := httpStatus(srv.Client(), "GET", srv.URL, ""); code != http.StatusOK {
		t.Fatalf("no token configured: request should pass, got %d", code)
	}
}

func httpStatus(client *http.Client, method, url, authHeader string) int {
	req, _ := http.NewRequest(method, url, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}
