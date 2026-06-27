// Package workloads implements read tools for pods, deployments, statefulsets,
// daemonsets, replicasets, jobs, and cronjobs.
package workloads

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

// Register registers all workload tools on s and returns the number added.
func Register(tk *tools.Toolkit, s *mcp.Server) int {
	v := security.VerbRead

	mcp.AddTool(s, tk2NewRead("list_pods",
		"List pods with ready count, status, restarts, node, and age. Use --all-namespaces or a label selector to filter."), tools.Wrap(tk, "list_pods", v, listPods(tk)))

	mcp.AddTool(s, tk2NewRead("get_pod",
		"Get full details of a single pod (containers, state, conditions, IPs, labels)."), tools.Wrap(tk, "get_pod", v, getPod(tk)))

	mcp.AddTool(s, tk2NewRead("list_deployments",
		"List deployments with ready/updated/available replica counts."), tools.Wrap(tk, "list_deployments", v, listDeployments(tk)))

	mcp.AddTool(s, tk2NewRead("get_deployment",
		"Get full details of a single deployment, including conditions and strategy."), tools.Wrap(tk, "get_deployment", v, getDeployment(tk)))

	mcp.AddTool(s, tk2NewRead("list_statefulsets",
		"List statefulsets with ready replicas."), tools.Wrap(tk, "list_statefulsets", v, listStatefulSets(tk)))

	mcp.AddTool(s, tk2NewRead("list_daemonsets",
		"List daemonsets with desired/ready/available counts."), tools.Wrap(tk, "list_daemonsets", v, listDaemonSets(tk)))

	mcp.AddTool(s, tk2NewRead("list_replicasets",
		"List replicasets with ready and available replica counts."), tools.Wrap(tk, "list_replicasets", v, listReplicaSets(tk)))

	mcp.AddTool(s, tk2NewRead("list_jobs",
		"List jobs with succeed/active/failed counts and completion."), tools.Wrap(tk, "list_jobs", v, listJobs(tk)))

	mcp.AddTool(s, tk2NewRead("list_cronjobs",
		"List cronjobs with schedule, suspend, last-schedule, and active counts."), tools.Wrap(tk, "list_cronjobs", v, listCronJobs(tk)))

	return 9
}

// tk2NewRead is a local alias to the shared read-tool builder.
func tk2NewRead(name, desc string) *mcp.Tool { return tools.NewReadTool(name, desc) }

func listPods(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Pod", ns, "", false)
		list, err := tk.Clients.Core.CoreV1().Pods(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list pods")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "READY", "STATUS", "RESTARTS", "NODE", "AGE")
		for i := range list.Items {
			p := &list.Items[i]
			t.AddRow(p.Namespace, p.Name, podReady(*p), podStatus(*p), podRestarts(*p), nodeName(*p), tools.AgeStr(p.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func getPod(tk *tools.Toolkit) tools.ToolFunc[tools.NamespaceNameArgs] {
	return func(ctx context.Context, a tools.NamespaceNameArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.CheckScope(ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Pod", ns, a.Name, false)
		p, err := tk.Clients.Core.CoreV1().Pods(ns).Get(ctx, a.Name, metav1.GetOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, "get pod "+ns+"/"+a.Name)
		}
		return rpc.TextResult(describePod(p)), nil
	}
}

func listDeployments(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Deployment", ns, "", false)
		list, err := tk.Clients.Core.AppsV1().Deployments(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list deployments")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "READY", "UP-TO-DATE", "AVAILABLE", "AGE")
		for _, d := range list.Items {
			t.AddRow(deploymentRow(d)...)
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func getDeployment(tk *tools.Toolkit) tools.ToolFunc[tools.NamespaceNameArgs] {
	return func(ctx context.Context, a tools.NamespaceNameArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.CheckScope(ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Deployment", ns, a.Name, false)
		d, err := tk.Clients.Core.AppsV1().Deployments(ns).Get(ctx, a.Name, metav1.GetOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, "get deployment "+ns+"/"+a.Name)
		}
		return rpc.TextResult(describeDeployment(d)), nil
	}
}

func listStatefulSets(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "StatefulSet", ns, "", false)
		list, err := tk.Clients.Core.AppsV1().StatefulSets(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list statefulsets")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "READY", "AGE")
		for _, s := range list.Items {
			ready := "0/0"
			if s.Spec.Replicas != nil {
				ready = intstr(int(s.Status.ReadyReplicas), int(*s.Spec.Replicas))
			}
			t.AddRow(s.Namespace, s.Name, ready, tools.AgeStr(s.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func listDaemonSets(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "DaemonSet", ns, "", false)
		list, err := tk.Clients.Core.AppsV1().DaemonSets(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list daemonsets")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "DESIRED", "CURRENT", "READY", "AGE")
		for _, d := range list.Items {
			t.AddRow(d.Namespace, d.Name,
				strnum(d.Status.DesiredNumberScheduled),
				strnum(d.Status.CurrentNumberScheduled),
				strnum(d.Status.NumberReady),
				tools.AgeStr(d.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func listReplicaSets(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "ReplicaSet", ns, "", false)
		list, err := tk.Clients.Core.AppsV1().ReplicaSets(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list replicasets")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "DESIRED", "CURRENT", "READY", "AGE")
		for _, r := range list.Items {
			desired := int32(0)
			if r.Spec.Replicas != nil {
				desired = *r.Spec.Replicas
			}
			t.AddRow(r.Namespace, r.Name, intstr(int(desired), 0), strnum(r.Status.Replicas), strnum(r.Status.ReadyReplicas), tools.AgeStr(r.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func listJobs(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Job", ns, "", false)
		list, err := tk.Clients.Core.BatchV1().Jobs(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list jobs")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "STATUS", "COMPLETIONS", "DURATION", "AGE")
		for _, j := range list.Items {
			t.AddRow(j.Namespace, j.Name, jobStatus(j), completionsStr(j), jobDuration(j), tools.AgeStr(j.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func listCronJobs(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "CronJob", ns, "", false)
		list, err := tk.Clients.Core.BatchV1().CronJobs(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list cronjobs")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "SCHEDULE", "SUSPEND", "ACTIVE", "LAST SCHEDULE", "AGE")
		for _, c := range list.Items {
			t.AddRow(c.Namespace, c.Name, c.Spec.Schedule, cronSuspend(c), strnum(int32(len(c.Status.Active))), lastScheduleTime(c), tools.AgeStr(c.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}
