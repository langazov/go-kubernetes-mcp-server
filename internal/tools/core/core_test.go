package core

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/langazov/go-kubernetes-mcp-server/internal/kube"
	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

func testToolkit(t *testing.T, objs ...runtime.Object) *tools.Toolkit {
	t.Helper()
	cs := fake.NewSimpleClientset(objs...)
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Defaults()
	return &tools.Toolkit{
		Clients: &kube.Clients{Core: cs, Discovery: cs.Discovery()},
		Policy:  security.FromConfig(cfg),
		Cfg:     &cfg,
		Audit:   audit.NewLogger(discard),
		Log:     discard,
	}
}

func TestListNamespaces(t *testing.T) {
	tk := testToolkit(t,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app-prod"}},
	)
	res, err := listNamespaces(tk)(context.Background(), noNamespaceArgs{})
	if err != nil {
		t.Fatalf("listNamespaces: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", textOf(res))
	}
	txt := textOf(res)
	for _, want := range []string{"default", "kube-system", "app-prod"} {
		if !strings.Contains(txt, want) {
			t.Errorf("expected namespace %q in output:\n%s", want, txt)
		}
	}
}

func TestGetNamespace(t *testing.T) {
	tk := testToolkit(t, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}})
	res, err := getNamespace(tk)(context.Background(), nn("default"))
	if err != nil {
		t.Fatalf("getNamespace: %v", err)
	}
	if !strings.Contains(textOf(res), "Name:") {
		t.Errorf("expected describe output:\n%s", textOf(res))
	}
}

func TestGetNamespaceInvalidName(t *testing.T) {
	tk := testToolkit(t)
	res, _ := getNamespace(tk)(context.Background(), nn(""))
	if !res.IsError {
		t.Error("expected error result for empty name")
	}
}

func TestListNodes(t *testing.T) {
	tk := testToolkit(t,
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}, Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.30.0"},
		}},
	)
	res, err := listNodes(tk)(context.Background(), tools.ListArgs{})
	if err != nil {
		t.Fatalf("listNodes: %v", err)
	}
	if !strings.Contains(textOf(res), "node-1") {
		t.Errorf("expected node-1 in output:\n%s", textOf(res))
	}
}

// --- test helpers ---

func nn(name string) tools.NamespaceNameArgs { return tools.NamespaceNameArgs{Name: name} }

func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
