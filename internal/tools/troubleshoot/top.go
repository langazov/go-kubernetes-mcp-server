package troubleshoot

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

func registerTop(tk *tools.Toolkit, s *mcp.Server) {
	mcp.AddTool(s, tools.NewReadTool("top_nodes",
		"Show CPU and memory usage per node, with usage percentages of capacity (requires metrics-server)."),
		tools.Wrap(tk, "top_nodes", read, topNodes(tk)))

	mcp.AddTool(s, tools.NewReadTool("top_pods",
		"Show CPU and memory usage per pod, with usage percentages of requests where available (requires metrics-server)."),
		tools.Wrap(tk, "top_pods", read, topPods(tk)))
}

type topArgs struct {
	Namespace     string `json:"namespace,omitempty" jsonschema:"the namespace (omit for all namespaces)"`
	AllNamespaces bool   `json:"all_namespaces,omitempty" jsonschema:"if true, aggregate across all namespaces"`
	Selector      string `json:"selector,omitempty" jsonschema:"a Kubernetes label selector to filter pods"`
}

func topNodes(tk *tools.Toolkit) tools.ToolFunc[topArgs] {
	return func(ctx context.Context, _ topArgs) (*mcp.CallToolResult, error) {
		list, err := tk.Clients.Metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
		if err != nil {
			return rpc.ErrorResult("metrics unavailable (is metrics-server installed?): %v", err), nil
		}
		nodes, _ := tk.Clients.Core.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		nodeAlloc := map[string]corev1.ResourceList{}
		for _, n := range nodes.Items {
			nodeAlloc[n.Name] = n.Status.Allocatable
		}
		t := rpc.NewTable("NAME", "CPU(cores)", "CPU%", "MEMORY(bytes)", "MEM%")
		for _, nm := range list.Items {
			cpu, mem := nm.Usage[corev1.ResourceCPU], nm.Usage[corev1.ResourceMemory]
			alloc := nodeAlloc[nm.Name]
			t.AddRow(nm.Name, cpu.String(), pct(cpu, alloc[corev1.ResourceCPU]), memScaled(mem), pct(mem, alloc[corev1.ResourceMemory]))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func topPods(tk *tools.Toolkit) tools.ToolFunc[topArgs] {
	return func(ctx context.Context, a topArgs) (*mcp.CallToolResult, error) {
		ns := ""
		if !a.AllNamespaces {
			ns = tools.ResolveNS(a.Namespace)
		}
		list, err := tk.Clients.Metrics.MetricsV1beta1().PodMetricses(ns).List(ctx, metav1.ListOptions{LabelSelector: a.Selector})
		if err != nil {
			return rpc.ErrorResult("metrics unavailable (is metrics-server installed?): %v", err), nil
		}
		// Fetch pods to get requests for percentage calculation.
		pods, _ := tk.Clients.Core.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: a.Selector})
		req := map[string]corev1.ResourceList{} // key: namespace/name
		for i := range pods.Items {
			p := &pods.Items[i]
			req[p.Namespace+"/"+p.Name] = podRequests(p)
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "CPU(cores)", "CPU%(req)", "MEMORY(bytes)", "MEM%(req)")
		for _, pm := range list.Items {
			cpu, mem := resource.Quantity{}, resource.Quantity{}
			for _, c := range pm.Containers {
				cpu.Add(*c.Usage.Cpu())
				mem.Add(*c.Usage.Memory())
			}
			r := req[pm.Namespace+"/"+pm.Name]
			t.AddRow(pm.Namespace, pm.Name, cpu.String(), pct(cpu, r[corev1.ResourceCPU]), memScaled(mem), pct(mem, r[corev1.ResourceMemory]))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func podRequests(p *corev1.Pod) corev1.ResourceList {
	out := corev1.ResourceList{}
	for _, c := range p.Spec.Containers {
		for name, q := range c.Resources.Requests {
			cur := out[name]
			cur.Add(q)
			out[name] = cur
		}
	}
	return out
}

func pct(used, base resource.Quantity) string {
	if base.IsZero() {
		return "-"
	}
	u, _ := used.AsInt64()
	b, _ := base.AsInt64()
	if b == 0 {
		return "-"
	}
	return fmt.Sprintf("%d%%", u*100/b)
}

func memScaled(q resource.Quantity) string {
	// Show memory in Mi for readability.
	v, ok := q.AsInt64()
	if !ok {
		return q.String()
	}
	return fmt.Sprintf("%dMi", v/(1024*1024))
}
