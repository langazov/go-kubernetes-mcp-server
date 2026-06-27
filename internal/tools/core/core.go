// Package core implements cluster-level tools: namespaces, nodes, events, API
// resource discovery, and cluster health.
package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

// Register registers all core tools on s and returns the number added.
func Register(tk *tools.Toolkit, s *mcp.Server) int {
	read := security.VerbRead

	mcp.AddTool(s, tool("list_namespaces",
		"List all namespaces in the cluster.", read, true,
	), tools.Wrap(tk, "list_namespaces", read, listNamespaces(tk)))

	mcp.AddTool(s, tool("get_namespace",
		"Get details of a single namespace by name.", read, true,
	), tools.Wrap(tk, "get_namespace", read, getNamespace(tk)))

	mcp.AddTool(s, tool("get_api_resources",
		"Discover all API resources the cluster serves (built-ins and CRDs), with their kind, group, version, namespaced flag, and verbs.", read, true,
	), tools.Wrap(tk, "get_api_resources", read, getAPIResources(tk)))

	mcp.AddTool(s, tool("cluster_health",
		"Report overall cluster health: API server reachability, node readiness, control-plane pod health, and metrics-server availability.", read, true,
	), tools.Wrap(tk, "cluster_health", read, clusterHealth(tk)))

	mcp.AddTool(s, tool("list_nodes",
		"List cluster nodes with status, role, version, age, and key conditions.", read, true,
	), tools.Wrap(tk, "list_nodes", read, listNodes(tk)))

	mcp.AddTool(s, tool("get_node",
		"Get details of a single node.", read, true,
	), tools.Wrap(tk, "get_node", read, getNode(tk)))

	mcp.AddTool(s, tool("describe_node",
		"Describe a node: conditions, allocatable/capacity resources, taints, images, and pods scheduled to it.", read, true,
	), tools.Wrap(tk, "describe_node", read, describeNode(tk)))

	mcp.AddTool(s, tool("list_events",
		"List recent Kubernetes events, optionally filtered by namespace, kind/name, or field selector. Sorted by most recent first.", read, true,
	), tools.Wrap(tk, "list_events", read, listEvents(tk)))

	return 8
}

// tool builds an *mcp.Tool with read-only annotations (closed world).
func tool(name, desc string, _ security.Verb, readOnly bool) *mcp.Tool {
	t := &mcp.Tool{
		Name:        name,
		Description: desc,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, OpenWorldHint: ptr(false)},
	}
	return t
}

func ptr[T any](v T) *T { return &v }

func listNamespaces(tk *tools.Toolkit) tools.ToolFunc[noNamespaceArgs] {
	return func(ctx context.Context, _ noNamespaceArgs) (*mcp.CallToolResult, error) {
		list, err := tk.Clients.Core.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, "list namespaces")
		}
		t := rpc.NewTable("NAME", "STATUS", "AGE")
		items := list.Items
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		for _, n := range items {
			t.AddRow(n.Name, string(n.Status.Phase), tools.AgeStr(n.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

type noNamespaceArgs struct{}

func getNamespace(tk *tools.Toolkit) tools.ToolFunc[tools.NamespaceNameArgs] {
	return func(ctx context.Context, a tools.NamespaceNameArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Namespace", "", a.Name, false)
		ns, err := tk.Clients.Core.CoreV1().Namespaces().Get(ctx, a.Name, metav1.GetOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, "get namespace "+a.Name)
		}
		return rpc.TextResult(describeNamespace(ns)), nil
	}
}

func listNodes(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		_, opts, err := tools.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		list, err := tk.Clients.Core.CoreV1().Nodes().List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list nodes")
		}
		t := rpc.NewTable("NAME", "STATUS", "ROLES", "VERSION", "AGE")
		for _, n := range list.Items {
			t.AddRow(n.Name, nodeStatus(n), nodeRoles(n), nodeVersion(n), tools.AgeStr(n.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func getNode(tk *tools.Toolkit) tools.ToolFunc[tools.NamespaceNameArgs] {
	return func(ctx context.Context, a tools.NamespaceNameArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Node", "", a.Name, false)
		n, err := tk.Clients.Core.CoreV1().Nodes().Get(ctx, a.Name, metav1.GetOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, "get node "+a.Name)
		}
		return rpc.TextResult(describeNodeObject(n)), nil
	}
}

func describeNode(tk *tools.Toolkit) tools.ToolFunc[tools.NamespaceNameArgs] {
	return getNode(tk)
}

func getAPIResources(tk *tools.Toolkit) tools.ToolFunc[apiResourceArgs] {
	return func(_ context.Context, a apiResourceArgs) (*mcp.CallToolResult, error) {
		groups, lists, err := tk.Clients.Discovery.ServerGroupsAndResources()
		// Discovery can return partial results with a non-nil error; surface a
		// note but keep going if we got anything.
		hadErr := err
		_ = groups
		if len(lists) == 0 && hadErr != nil {
			return nil, tools.RPCStatusError(hadErr, "discover API resources")
		}
		t := rpc.NewTable("NAME", "KIND", "GROUP", "VERSION", "NAMESPACED", "VERBS")
		var rows [][]string
		for _, rl := range lists {
			group, version := splitGV(rl.GroupVersion)
			for _, r := range rl.APIResources {
				if a.NamespacedOnly && !r.Namespaced {
					continue
				}
				rows = append(rows, []string{
					r.Name,
					r.Kind,
					group,
					version,
					fmt.Sprintf("%t", r.Namespaced),
					"[" + strings.Join(r.Verbs, ",") + "]",
				})
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			return rows[i][1]+rows[i][2]+rows[i][3] < rows[j][1]+rows[j][2]+rows[j][3]
		})
		for _, r := range rows {
			t.AddRow(r...)
		}
		out := t.Render()
		if hadErr != nil {
			out += fmt.Sprintf("\nNote: discovery returned partial results: %v\n", hadErr)
		}
		return rpc.TextResult(out), nil
	}
}

func splitGV(gv string) (string, string) {
	i := strings.Index(gv, "/")
	if i < 0 {
		return "", gv
	}
	return gv[:i], gv[i+1:]
}

type apiResourceArgs struct {
	NamespacedOnly bool `json:"namespaced_only,omitempty" jsonschema:"if true, only list namespaced resources"`
}

func listEvents(tk *tools.Toolkit) tools.ToolFunc[eventsArgs] {
	return func(ctx context.Context, a eventsArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateSelectorToken(a.Kind); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateSelectorToken(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if a.AllNamespaces {
			ns = ""
		}
		if ns == "" {
			if len(tk.Policy.Namespaces) > 0 {
				return rpc.ErrorResult("listing all namespaces is not permitted while a namespace allowlist is configured"), nil
			}
		} else if err := tk.CheckScope(ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Event", ns, "", false)
		opts := metav1.ListOptions{Limit: a.Limit}
		if a.Kind != "" && a.Name != "" {
			opts.FieldSelector = fmt.Sprintf("involvedObject.kind=%s,involvedObject.name=%s", a.Kind, a.Name)
		} else if a.FieldSelector != "" {
			opts.FieldSelector = a.FieldSelector
		}
		list, err := tk.Clients.Core.CoreV1().Events(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list events")
		}
		items := list.Items
		sort.Slice(items, func(i, j int) bool {
			return eventTime(items[i]).After(eventTime(items[j]).Time)
		})
		if a.Limit > 0 && int64(len(items)) > a.Limit {
			items = items[:a.Limit]
		}
		t := rpc.NewTable("LAST SEEN", "TYPE", "REASON", "OBJECT", "MESSAGE")
		for _, e := range items {
			msg := tools.TruncLen(e.Message, 80)
			t.AddRow(tools.AgeStr(metav1.Time{Time: eventTime(e).Time}), e.Type, e.Reason,
				fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name), msg)
		}
		return rpc.TextResult(t.Render()), nil
	}
}

type eventsArgs struct {
	Namespace     string `json:"namespace,omitempty" jsonschema:"the namespace (omit for all namespaces)"`
	Kind          string `json:"kind,omitempty" jsonschema:"filter events to a resource kind (e.g. Pod)"`
	Name          string `json:"name,omitempty" jsonschema:"filter events to a resource name (requires kind)"`
	FieldSelector string `json:"field_selector,omitempty" jsonschema:"raw Kubernetes field selector"`
	AllNamespaces bool   `json:"all_namespaces,omitempty" jsonschema:"if true, list events across all namespaces"`
	Limit         int64  `json:"limit,omitempty" jsonschema:"maximum number of events to return (default 100)"`
}
