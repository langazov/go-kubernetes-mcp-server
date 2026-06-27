package debug

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/testutil"
)

func withDebug(c *config.Config) { c.AllowDebug = true }

func TestExecBlockedWithoutFlag(t *testing.T) {
	tk := testutil.NewToolkit(t) // read-only
	res, _ := execCommand(tk)(context.Background(), execArgs{Pod: "p", Command: []string{"ls"}})
	if !testutil.IsError(res) {
		t.Fatal("exec must be blocked without --allow-debug")
	}
	if !strings.Contains(testutil.TextOf(res), "disabled") {
		t.Errorf("expected disabled message:\n%s", testutil.TextOf(res))
	}
}

func TestExecValidation(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withDebug))
	for _, tc := range []struct {
		name string
		args execArgs
	}{
		{"empty pod", execArgs{Command: []string{"ls"}}},
		{"empty command", execArgs{Pod: "p"}},
	} {
		res, _ := execCommand(tk)(context.Background(), tc.args)
		if !testutil.IsError(res) {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

func TestAddEphemeralBlockedWithoutFlag(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"}}),
	)
	res, _ := addEphemeralContainer(tk)(context.Background(), ephemeralArgs{Pod: "p", Image: "busybox"})
	if !testutil.IsError(res) {
		t.Fatal("ephemeral must be blocked without --allow-debug")
	}
}

func TestAddEphemeralImageRequired(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withDebug),
		testutil.WithObjs(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"}}),
	)
	res, _ := addEphemeralContainer(tk)(context.Background(), ephemeralArgs{Pod: "p"})
	if !testutil.IsError(res) {
		t.Error("expected error when image is missing")
	}
}

func TestAddEphemeralForbiddenImage(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithConfig(func(c *config.Config) {
			c.AllowDebug = true
			c.ForbiddenImages = []string{"evil:latest"}
		}),
		testutil.WithObjs(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"}}),
	)
	res, _ := addEphemeralContainer(tk)(context.Background(), ephemeralArgs{Pod: "p", Image: "evil:latest"})
	if !testutil.IsError(res) || !strings.Contains(testutil.TextOf(res), "forbidden") {
		t.Errorf("expected forbidden-image error:\n%s", testutil.TextOf(res))
	}
}

func TestRunDebugPodBlockedWithoutFlag(t *testing.T) {
	tk := testutil.NewToolkit(t)
	res, _ := runDebugPod(tk)(context.Background(), runDebugPodArgs{Image: "busybox"})
	if !testutil.IsError(res) {
		t.Fatal("run_debug_pod must be blocked without --allow-debug")
	}
}

func TestRunDebugPodImageRequired(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withDebug))
	res, _ := runDebugPod(tk)(context.Background(), runDebugPodArgs{})
	if !testutil.IsError(res) {
		t.Error("expected error when image missing")
	}
}

func TestRunDebugPodCreates(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withDebug))
	res, err := runDebugPod(tk)(context.Background(), runDebugPodArgs{Image: "busybox", Name: "dbg"})
	if err != nil {
		t.Fatalf("runDebugPod: %v", err)
	}
	if testutil.IsError(res) {
		t.Fatalf("create failed: %s", testutil.TextOf(res))
	}
	if _, err := tk.Clients.Core.CoreV1().Pods("default").Get(context.Background(), "dbg", metav1.GetOptions{}); err != nil {
		t.Errorf("debug pod not created: %v", err)
	}
}

func TestRunDebugPodServiceAccountBlocked(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withDebug))
	res, err := runDebugPod(tk)(context.Background(), runDebugPodArgs{Image: "busybox", Name: "dbg", ServiceAccount: "cluster-admin"})
	if err != nil {
		t.Fatalf("runDebugPod: %v", err)
	}
	if !testutil.IsError(res) || !strings.Contains(testutil.TextOf(res), "service account") {
		t.Fatalf("a non-default service account must be blocked without --allowed-debug-service-accounts:\n%s", testutil.TextOf(res))
	}
}

func TestRunDebugPodServiceAccountAllowlisted(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(func(c *config.Config) {
		c.AllowDebug = true
		c.AllowedDebugServiceAccounts = []string{"toolbox"}
	}))
	res, err := runDebugPod(tk)(context.Background(), runDebugPodArgs{Image: "busybox", Name: "dbg2", ServiceAccount: "toolbox"})
	if err != nil {
		t.Fatalf("runDebugPod: %v", err)
	}
	if testutil.IsError(res) {
		t.Fatalf("allowlisted service account should be permitted:\n%s", testutil.TextOf(res))
	}
}

func TestExecKubeSystemBlocked(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withDebug),
		testutil.WithObjs(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "kube-system"}}),
	)
	res, err := execCommand(tk)(context.Background(), execArgs{Pod: "p", Namespace: "kube-system", Command: []string{"id"}})
	if err != nil {
		t.Fatalf("execCommand: %v", err)
	}
	if !testutil.IsError(res) || !strings.Contains(testutil.TextOf(res), "privileged") {
		t.Fatalf("exec into kube-system must require --allow-privileged-targets:\n%s", testutil.TextOf(res))
	}
}
