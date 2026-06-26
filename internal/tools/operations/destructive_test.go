package operations

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/testutil"
)

func withDestructive(c *config.Config) {
	c.AllowDestructive = true
	c.AllowPrivilegedTargets = true // node ops are cluster-scoped
}

// ----- delete_pod -----

func TestDeletePod(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withDestructive),
		testutil.WithObjs(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"}}),
	)
	res, err := deletePod(tk)(context.Background(), deletePodArgs{Name: "p"})
	if err != nil {
		t.Fatalf("deletePod: %v", err)
	}
	if testutil.IsError(res) {
		t.Fatalf("delete failed: %s", testutil.TextOf(res))
	}
	if _, err := tk.Clients.Core.CoreV1().Pods("default").Get(context.Background(), "p", metav1.GetOptions{}); err == nil {
		t.Error("pod should be deleted")
	}
}

func TestDeletePodBlockedWithoutDestructiveFlag(t *testing.T) {
	// Register-level gating means deletePod isn't even available in read-only
	// mode, but the handler also self-checks. We test the self-check here by
	// constructing a write-only policy (no destructive).
	tk := testutil.NewToolkit(t,
		testutil.WithConfig(func(c *config.Config) { c.AllowWrites = true }),
		testutil.WithObjs(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"}}),
	)
	res, _ := deletePod(tk)(context.Background(), deletePodArgs{Name: "p"})
	if !testutil.IsError(res) {
		t.Fatal("delete must be blocked without --allow-destructive")
	}
	if _, err := tk.Clients.Core.CoreV1().Pods("default").Get(context.Background(), "p", metav1.GetOptions{}); err != nil {
		t.Error("pod should still exist when blocked")
	}
}

// ----- delete_manifest (dynamic) -----

func TestDeleteManifest(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withDestructive),
		testutil.WithObjs(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default"}, Data: map[string]string{"k": "v"}}),
		testutil.WithDynamicObjs(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default"}, Data: map[string]string{"k": "v"}}),
	)
	res, err := deleteManifest(tk)(context.Background(), deleteArgs{Kind: "ConfigMap", APIVersion: "v1", Name: "cfg"})
	if err != nil {
		t.Fatalf("deleteManifest: %v", err)
	}
	if testutil.IsError(res) {
		t.Fatalf("delete failed: %s", testutil.TextOf(res))
	}
}

// ----- cordon / drain -----

func TestCordonNode(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withDestructive),
		testutil.WithObjs(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}),
	)
	res, err := cordonNode(tk)(context.Background(), nodeNameArgs{Name: "n1"})
	if err != nil {
		t.Fatalf("cordonNode: %v", err)
	}
	if testutil.IsError(res) {
		t.Fatalf("cordon failed: %s", testutil.TextOf(res))
	}
	n, err := tk.Clients.Core.CoreV1().Nodes().Get(context.Background(), "n1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if !n.Spec.Unschedulable {
		t.Error("node should be unschedulable after cordon")
	}
}

func TestDrainNodeSkipsDaemonSets(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withDestructive),
		testutil.WithObjs(
			&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
			// A standalone pod (no controller) — should be skipped without force.
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "standalone", Namespace: "default"},
				Spec:       corev1.PodSpec{NodeName: "n1"},
			},
			// A DaemonSet-owned pod — always skipped.
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "ds-pod", Namespace: "kube-system", OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet", Name: "fluentd", Controller: ptrBool(true)}}},
				Spec:       corev1.PodSpec{NodeName: "n1"},
			},
		),
	)
	res, err := drainNode(tk)(context.Background(), drainArgs{Name: "n1", Force: false})
	if err != nil {
		t.Fatalf("drainNode: %v", err)
	}
	out := testutil.TextOf(res)
	// Node cordoned.
	n, _ := tk.Clients.Core.CoreV1().Nodes().Get(context.Background(), "n1", metav1.GetOptions{})
	if !n.Spec.Unschedulable {
		t.Error("drain should cordon the node")
	}
	// DaemonSet pod must be skipped (mentioned in output).
	if !strings.Contains(out, "DaemonSet") && !strings.Contains(out, "skip") {
		t.Errorf("expected skip messages for DS/standalone pods:\n%s", out)
	}
}

func TestSkipReason(t *testing.T) {
	ds := corev1.Pod{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet", Controller: ptrBool(true)}}}}
	if skipReason(&ds, drainArgs{}) != "DaemonSet pod" {
		t.Error("DaemonSet pod should be skipped")
	}
	standalone := corev1.Pod{}
	if skipReason(&standalone, drainArgs{Force: false}) != "standalone pod (use force=true)" {
		t.Error("standalone pod should be skipped without force")
	}
	if skipReason(&standalone, drainArgs{Force: true}) != "" {
		t.Error("standalone pod should be evicted with force")
	}
	emptyDir := corev1.Pod{Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "v", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}}}
	if skipReason(&emptyDir, drainArgs{Force: true, DeleteEmptyDirData: false}) == "" {
		t.Error("emptyDir pod should be skipped without delete_emptydir_data")
	}
	if skipReason(&emptyDir, drainArgs{Force: true, DeleteEmptyDirData: true}) != "" {
		t.Error("emptyDir pod should be evicted with delete_emptydir_data")
	}
}

func ptrBool(b bool) *bool { return &b }
