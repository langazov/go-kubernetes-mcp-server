package core

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

func clusterHealth(tk *tools.Toolkit) tools.ToolFunc[noNamespaceArgs] {
	return func(ctx context.Context, _ noNamespaceArgs) (*mcp.CallToolResult, error) {
		var b strings.Builder
		ok := true

		// API server reachability via server version.
		version, err := tk.Clients.Discovery.ServerVersion()
		if err != nil {
			ok = false
			fmt.Fprintf(&b, "API server:  UNREACHABLE (%v)\n", err)
		} else {
			fmt.Fprintf(&b, "API server:  OK (%s %s)\n", version.GitVersion, version.Platform)
		}

		// Node readiness.
		nodes, err := tk.Clients.Core.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			ok = false
			fmt.Fprintf(&b, "Nodes:       unable to list (%v)\n", err)
		} else {
			ready := 0
			var notReady []string
			for _, n := range nodes.Items {
				if isNodeReady(n) {
					ready++
				} else {
					notReady = append(notReady, n.Name)
				}
			}
			fmt.Fprintf(&b, "Nodes:       %d/%d ready", ready, len(nodes.Items))
			if len(notReady) > 0 {
				ok = false
				fmt.Fprintf(&b, " (NotReady: %s)", strings.Join(notReady, ", "))
			}
			b.WriteString("\n")
		}

		// Control-plane health: kube-system pods.
		cp, err := tk.Clients.Core.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
			LabelSelector: "component",
		})
		if err == nil && len(cp.Items) > 0 {
			running := 0
			var bad []string
			for _, p := range cp.Items {
				if p.Status.Phase == corev1.PodRunning && isPodReady(p) {
					running++
				} else {
					bad = append(bad, p.Name+"="+string(p.Status.Phase))
				}
			}
			fmt.Fprintf(&b, "Control plane: %d/%d running", running, len(cp.Items))
			if len(bad) > 0 {
				fmt.Fprintf(&b, " (issues: %s)", strings.Join(bad, ", "))
			}
			b.WriteString("\n")
		}

		// Metrics server availability.
		hasMetrics := false
		if _, err := tk.Clients.Metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{Limit: 1}); err == nil {
			hasMetrics = true
		}
		if hasMetrics {
			fmt.Fprintf(&b, "Metrics server: INSTALLED\n")
		} else {
			fmt.Fprintf(&b, "Metrics server: NOT DETECTED (top_* tools will be unavailable)\n")
		}

		summary := "HEALTHY"
		if !ok {
			summary = "DEGRADED"
		}
		return rpc.TextResult(fmt.Sprintf("Cluster status: %s\n\n%s", summary, b.String())), nil
	}
}

func isNodeReady(n corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func isPodReady(p corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
