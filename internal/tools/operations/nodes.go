package operations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

// ----- cordon / uncordon -----

type nodeNameArgs struct {
	Name string `json:"name" jsonschema:"the node name"`
}

func uncordonNode(tk *tools.Toolkit) tools.ToolFunc[nodeNameArgs] {
	return setNodeUnschedulable(tk, "uncordon_node", false)
}

func cordonNode(tk *tools.Toolkit) tools.ToolFunc[nodeNameArgs] {
	return setNodeUnschedulable(tk, "cordon_node", true)
}

func setNodeUnschedulable(tk *tools.Toolkit, name string, unschedulable bool) tools.ToolFunc[nodeNameArgs] {
	verb := "cordon"
	if !unschedulable {
		verb = "uncordon"
	}
	return func(ctx context.Context, a nodeNameArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := tk.Policy.CheckTarget("", true); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Node", "", a.Name, false)

		patch := fmt.Sprintf(`{"spec":{"unschedulable":%t}}`, unschedulable)
		if _, err := tk.Clients.Core.CoreV1().Nodes().Patch(ctx, a.Name, types.MergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("%s node %s", verb, a.Name))
		}
		state := "unschedulable (cordoned)"
		if !unschedulable {
			state = "schedulable (uncordoned)"
		}
		return rpc.TextResult(fmt.Sprintf("node %s marked %s\n", a.Name, state)), nil
	}
}

// ----- drain -----

type drainArgs struct {
	Name               string `json:"name" jsonschema:"the node name"`
	DeleteEmptyDirData bool   `json:"delete_emptydir_data,omitempty" jsonschema:"evict pods using emptyDir volumes (data will be lost)"`
	IgnoreDaemonSets   bool   `json:"ignore_daemonsets,omitempty" jsonschema:"skip DaemonSet pods (default true)"`
	Force              bool   `json:"force,omitempty" jsonschema:"continue even if a pod is not managed by a controller"`
	Timeout            string `json:"timeout,omitempty" jsonschema:"max time to wait for evictions (e.g. 120s)"`
}

func drainNode(tk *tools.Toolkit) tools.ToolFunc[drainArgs] {
	return func(ctx context.Context, a drainArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckDestructive(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := tk.Policy.CheckTarget("", true); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Node", "", a.Name, false)

		timeout := 2 * time.Minute
		if a.Timeout != "" {
			if d, err := time.ParseDuration(a.Timeout); err == nil {
				timeout = d
			}
		}
		if timeout > tk.Cfg.DefaultTimeout {
			// Use a dedicated deadline for the (potentially long) drain.
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		if a.IgnoreDaemonSets {
			// already default true; flag accepted for kubectl parity
		}

		// 1. Cordon.
		if _, err := tk.Clients.Core.CoreV1().Nodes().Patch(ctx, a.Name, types.MergePatchType,
			[]byte(`{"spec":{"unschedulable":true}}`), metav1.PatchOptions{}); err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("cordon node %s during drain", a.Name))
		}

		// 2. List pods on the node and evict eligible ones.
		pods, err := tk.Clients.Core.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("spec.nodeName=%s", a.Name),
		})
		if err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("list pods on node %s", a.Name))
		}

		var b strings.Builder
		fmt.Fprintf(&b, "draining node %s (cordoned, %d resident pods)\n", a.Name, len(pods.Items))
		evicted, skipped, failed := 0, 0, 0
		for i := range pods.Items {
			p := &pods.Items[i]
			reason := skipReason(p, a)
			if reason != "" {
				fmt.Fprintf(&b, "  skip %s/%s (%s)\n", p.Namespace, p.Name, reason)
				skipped++
				continue
			}
			if err := evictPod(ctx, tk, p); err != nil {
				fmt.Fprintf(&b, "  FAIL  %s/%s: %v\n", p.Namespace, p.Name, err)
				failed++
				continue
			}
			fmt.Fprintf(&b, "  evict %s/%s\n", p.Namespace, p.Name)
			evicted++
		}
		fmt.Fprintf(&b, "done: %d evicted, %d skipped, %d failed\n", evicted, skipped, failed)
		if failed > 0 {
			b.WriteString("\nSome pods could not be evicted (likely blocked by a PodDisruptionBudget). Re-run after resolving, or increase the timeout.\n")
		}
		return rpc.TextResult(b.String()), nil
	}
}

// skipReason returns why a pod should not be evicted during drain, or "" to evict.
func skipReason(p *corev1.Pod, a drainArgs) string {
	if p.Namespace == "kube-system" {
		// Mirror pods and static pods.
		if _, ok := p.Annotations[corev1.MirrorPodAnnotationKey]; ok {
			return "mirror/static pod"
		}
	}
	// Standalone (not controller-managed) pods: skip unless --force.
	if !hasController(p) {
		if !a.Force {
			return "standalone pod (use force=true)"
		}
	}
	// DaemonSet pods: skip (default), unless caller disables (not implemented).
	if hasOwnerKind(p, "DaemonSet") {
		return "DaemonSet pod"
	}
	// emptyDir data: skip unless allowed.
	if !a.DeleteEmptyDirData && hasEmptyDir(p) {
		return "uses emptyDir (use delete_emptydir_data=true)"
	}
	return ""
}

func hasController(p *corev1.Pod) bool {
	for _, r := range p.OwnerReferences {
		if r.Controller != nil && *r.Controller {
			return true
		}
	}
	return false
}

func hasOwnerKind(p *corev1.Pod, kind string) bool {
	for _, r := range p.OwnerReferences {
		if r.Kind == kind {
			return true
		}
	}
	return false
}

func hasEmptyDir(p *corev1.Pod) bool {
	for i := range p.Spec.Volumes {
		if p.Spec.Volumes[i].EmptyDir != nil {
			return true
		}
	}
	return false
}

// evictPod creates an Eviction subresource, respecting PodDisruptionBudgets.
func evictPod(ctx context.Context, tk *tools.Toolkit, p *corev1.Pod) error {
	ev := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace},
	}
	return tk.Clients.Core.CoreV1().Pods(p.Namespace).EvictV1(ctx, ev)
}
