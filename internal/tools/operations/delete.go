package operations

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

var destroy = security.VerbDestructive

// registerDestructive registers tools that require --allow-destructive.
func registerDestructive(tk *tools.Toolkit, s *mcp.Server) int {
	n := 0

	mcp.AddTool(s, tools.NewDestructiveTool("delete_pod",
		"Delete a single pod. The controller will recreate it if it is managed. Pass a grace period in seconds to force-kill."),
		tools.Wrap(tk, "delete_pod", destroy, deletePod(tk)))
	n++

	mcp.AddTool(s, tools.NewDestructiveTool("delete_manifest",
		"Delete a resource by kind, name, and namespace. Supports cascade deletion."),
		tools.Wrap(tk, "delete_manifest", destroy, deleteManifest(tk)))
	n++

	mcp.AddTool(s, tools.NewDestructiveTool("cordon_node",
		"Mark a node unschedulable (no new pods will be scheduled)."),
		tools.Wrap(tk, "cordon_node", destroy, cordonNode(tk)))
	n++

	mcp.AddTool(s, tools.NewDestructiveTool("drain_node",
		"Cordon a node and evict its pods, respecting PodDisruptionBudgets. Destructive (affects running workloads)."),
		tools.Wrap(tk, "drain_node", destroy, drainNode(tk)))
	n++

	return n
}

// ----- delete_pod -----

type deletePodArgs struct {
	Namespace   string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name        string `json:"name" jsonschema:"the pod name"`
	GracePeriod *int64 `json:"grace_period_seconds,omitempty" jsonschema:"grace period in seconds (0 = force immediate)"`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"if true, validate without deleting"`
}

func deletePod(tk *tools.Toolkit) tools.ToolFunc[deletePodArgs] {
	return func(ctx context.Context, a deletePodArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckDestructive(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.Policy.CheckNamespace(ns); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Pod", ns, a.Name, a.DryRun)

		opts := metav1.DeleteOptions{}
		if a.GracePeriod != nil {
			opts.GracePeriodSeconds = a.GracePeriod
		}
		if a.DryRun {
			opts.DryRun = []string{"All"}
		}
		if err := tk.Clients.Core.CoreV1().Pods(ns).Delete(ctx, a.Name, opts); err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("delete pod %s/%s", ns, a.Name))
		}
		verb := "deleted"
		if a.DryRun {
			verb = "would delete (dry run)"
		}
		return rpc.TextResult(fmt.Sprintf("pod %s/%s %s\n", ns, a.Name, verb)), nil
	}
}

// ----- delete_manifest -----

type deleteArgs struct {
	Kind       string `json:"kind" jsonschema:"the resource kind (e.g. Deployment)"`
	APIVersion string `json:"api_version,omitempty" jsonschema:"the group/version; omit to auto-detect"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name       string `json:"name" jsonschema:"the resource name"`
	Cascade    string `json:"cascade,omitempty" jsonschema:"deletion propagation: background (default), foreground, or orphan"`
	DryRun     bool   `json:"dry_run,omitempty" jsonschema:"if true, validate without deleting"`
}

func deleteManifest(tk *tools.Toolkit) tools.ToolFunc[deleteArgs] {
	return func(ctx context.Context, a deleteArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckDestructive(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		gvr, namespaced, err := tools.ResolveGVR(ctx, tk, a.Kind, a.APIVersion)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := policyTarget(tk, ns, !namespaced); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		displayNS := ns
		if !namespaced {
			displayNS = "" // cluster-scoped: no namespace
		}
		audit.Attach(ctx, a.Kind, displayNS, a.Name, a.DryRun)

		opts := metav1.DeleteOptions{}
		switch a.Cascade {
		case "foreground":
			propagation := metav1.DeletePropagationForeground
			opts.PropagationPolicy = &propagation
		case "orphan":
			propagation := metav1.DeletePropagationOrphan
			opts.PropagationPolicy = &propagation
		default: // background or unset
			propagation := metav1.DeletePropagationBackground
			opts.PropagationPolicy = &propagation
		}
		if a.DryRun {
			opts.DryRun = []string{"All"}
		}
		nri := tk.Clients.Dynamic.Resource(gvr)
		if namespaced {
			if err := nri.Namespace(ns).Delete(ctx, a.Name, opts); err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("delete %s %s/%s", a.Kind, ns, a.Name))
			}
		} else {
			if err := nri.Delete(ctx, a.Name, opts); err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("delete %s %s", a.Kind, a.Name))
			}
		}
		verb := "deleted"
		if a.DryRun {
			verb = "would delete (dry run)"
		}
		target := a.Name
		if namespaced {
			target = ns + "/" + a.Name
		}
		cascade := a.Cascade
		if cascade == "" {
			cascade = "background"
		}
		return rpc.TextResult(fmt.Sprintf("%s %s %s (cascade=%s)\n", a.Kind, target, verb, cascade)), nil
	}
}
