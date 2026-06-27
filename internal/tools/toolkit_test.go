package tools

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func tkWithPolicy(mutate func(*config.Config)) *Toolkit {
	cfg := config.Defaults()
	if mutate != nil {
		mutate(&cfg)
	}
	return &Toolkit{
		Policy: security.FromConfig(cfg),
		Cfg:    &cfg,
		Audit:  audit.NewLogger(discardLogger()),
		Log:    discardLogger(),
	}
}

func resultText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// ----- ResolveNS / AgeStr / TruncLen helpers -----

func TestResolveNSDefaultsEmptyToDefault(t *testing.T) {
	if got := ResolveNS(""); got != "default" {
		t.Errorf("ResolveNS('') = %q, want default", got)
	}
	if got := ResolveNS("team-a"); got != "team-a" {
		t.Errorf("ResolveNS('team-a') = %q, want team-a", got)
	}
}

func TestTruncLen(t *testing.T) {
	if got := TruncLen("abc", 10); got != "abc" {
		t.Errorf("short string should be unchanged, got %q", got)
	}
	if got := TruncLen("abcdef", 3); got != "abc…" {
		t.Errorf("truncated string = %q, want abc…", got)
	}
}

func TestAgeStr(t *testing.T) {
	if got := AgeStr(metav1.Time{}); got != "" {
		t.Errorf("zero time should render empty, got %q", got)
	}
	got := AgeStr(metav1.Time{Time: time.Now().Add(-5 * time.Minute)})
	if !strings.HasSuffix(got, "m") {
		t.Errorf("5m-old age should end in 'm', got %q", got)
	}
}

// ----- Toolkit.CheckScope (privileged-target + allowlist) -----

func TestCheckScopeClusterScopedRequiresPrivileged(t *testing.T) {
	tk := tkWithPolicy(nil)
	if err := tk.CheckScope("", true); err == nil {
		t.Error("cluster-scoped target must require --allow-privileged-targets")
	}
}

func TestCheckScopeKubeSystemRequiresPrivileged(t *testing.T) {
	tk := tkWithPolicy(nil)
	if err := tk.CheckScope("kube-system", false); err == nil {
		t.Error("kube-system must require --allow-privileged-targets")
	}
}

func TestCheckScopePrivilegedFlagAllowsClusterScoped(t *testing.T) {
	tk := tkWithPolicy(func(c *config.Config) { c.AllowPrivilegedTargets = true })
	if err := tk.CheckScope("kube-public", false); err != nil {
		t.Errorf("privileged flag should allow kube-public: %v", err)
	}
	if err := tk.CheckScope("", true); err != nil {
		t.Errorf("privileged flag should allow cluster-scoped: %v", err)
	}
}

func TestCheckScopeNamespaceAllowlistEnforced(t *testing.T) {
	tk := tkWithPolicy(func(c *config.Config) { c.Namespaces = []string{"team-a"} })
	if err := tk.CheckScope("team-a", false); err != nil {
		t.Errorf("team-a should be allowed: %v", err)
	}
	if err := tk.CheckScope("team-b", false); err == nil {
		t.Error("team-b is outside the allowlist and must be rejected")
	}
}

// ----- Toolkit.ResolveList (namespace-allowlist bypass fix) -----

func TestResolveListRejectsAllNamespacesWithAllowlist(t *testing.T) {
	tk := tkWithPolicy(func(c *config.Config) { c.Namespaces = []string{"team-a"} })
	if _, _, err := tk.ResolveList(ListArgs{AllNamespaces: true}); err == nil {
		t.Error("all_namespaces must be rejected when an allowlist is configured")
	}
}

func TestResolveListRejectsNamespaceOutsideAllowlist(t *testing.T) {
	tk := tkWithPolicy(func(c *config.Config) { c.Namespaces = []string{"team-a"} })
	if _, _, err := tk.ResolveList(ListArgs{Namespace: "team-b"}); err == nil {
		t.Error("a namespace outside the allowlist must be rejected")
	}
}

func TestResolveListAllowsAllNamespacesWhenNoAllowlist(t *testing.T) {
	tk := tkWithPolicy(nil)
	ns, _, err := tk.ResolveList(ListArgs{AllNamespaces: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ns != "" {
		t.Errorf("all-namespaces list should yield ns='', got %q", ns)
	}
}

func TestResolveListRejectsBadSelector(t *testing.T) {
	tk := tkWithPolicy(nil)
	if _, _, err := tk.ResolveList(ListArgs{Selector: "a=b\n"}); err == nil {
		t.Error("a selector with a newline must be rejected")
	}
}

// ----- InitSemaphore + Wrap concurrency cap -----

func TestInitSemaphoreCreatesBoundedChannel(t *testing.T) {
	tk := tkWithPolicy(nil)
	tk.InitSemaphore(2)
	if tk.sem == nil || cap(tk.sem) != 2 {
		t.Errorf("InitSemaphore(2) should create a channel of capacity 2")
	}
}

func TestInitSemaphoreZeroLeavesNil(t *testing.T) {
	tk := tkWithPolicy(nil)
	tk.InitSemaphore(0)
	if tk.sem != nil {
		t.Error("InitSemaphore(0) should leave the semaphore nil (unlimited)")
	}
}

func TestWrapRejectsCallWhenSemaphoreFull(t *testing.T) {
	cfg := config.Defaults()
	cfg.DefaultTimeout = 5 * time.Second
	cfg.MaxOutputBytes = 1024
	tk := &Toolkit{
		Policy: security.FromConfig(cfg),
		Cfg:    &cfg,
		Audit:  audit.NewLogger(discardLogger()),
		Log:    discardLogger(),
	}
	tk.InitSemaphore(1)

	hold := make(chan struct{})
	released := make(chan struct{})
	slow := func(context.Context, noArgs) (*mcp.CallToolResult, error) {
		hold <- struct{}{}
		<-released
		return rpc.TextResult("ok"), nil
	}
	wrapped := Wrap(tk, "slow", security.VerbRead, slow)

	go func() { _, _, _ = wrapped(context.Background(), nil, noArgs{}) }()
	<-hold // the slow call now occupies the only slot

	// A second call must be rejected quickly while the slot is held.
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	res, _, err := wrapped(ctx, nil, noArgs{})
	close(released)

	if err != nil {
		t.Fatalf("unexpected go-level error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected a 'server busy' tool error, got %+v", res)
	}
	if !strings.Contains(resultText(res), "busy") {
		t.Fatalf("expected busy message, got: %s", resultText(res))
	}
}

func TestWrapRunsAndTruncates(t *testing.T) {
	tk := tkWithPolicy(func(c *config.Config) { c.MaxOutputBytes = 4 })
	huge := func(context.Context, noArgs) (*mcp.CallToolResult, error) {
		return rpc.TextResult("0123456789"), nil
	}
	wrapped := Wrap(tk, "echo", security.VerbRead, huge)
	res, _, err := wrapped(context.Background(), nil, noArgs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resultText(res) == "0123456789" {
		t.Errorf("output should have been truncated to fit max-output-bytes")
	}
}
