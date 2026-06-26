package troubleshoot

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

func registerDescribe(tk *tools.Toolkit, s *mcp.Server) {
	mcp.AddTool(s, tools.NewReadTool("describe",
		"Describe any resource (built-in or CRD) by kind, apiVersion, name, and namespace. Returns the full live object as YAML. Use get_api_resources to find kinds."),
		tools.Wrap(tk, "describe", read, describe(tk)))
}

type describeArgs struct {
	Kind       string `json:"kind" jsonschema:"the resource kind (e.g. Pod, Deployment, Service, Job)"`
	APIVersion string `json:"api_version,omitempty" jsonschema:"the group/version (e.g. apps/v1); omit to auto-detect"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default'; omit for cluster-scoped resources)"`
	Name       string `json:"name" jsonschema:"the resource name"`
}

func describe(tk *tools.Toolkit) tools.ToolFunc[describeArgs] {
	return func(ctx context.Context, a describeArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		gvr, namespaced, err := resolveGVR(ctx, tk, a.Kind, a.APIVersion)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := ""
		if namespaced {
			ns = tools.ResolveNS(a.Namespace)
			if err := tk.Policy.CheckNamespace(ns); err != nil {
				return rpc.ErrorResult("%v", err), nil
			}
		} else if a.Namespace != "" {
			return rpc.ErrorResult("%s is cluster-scoped; do not pass a namespace", a.Kind), nil
		}
		audit.Attach(ctx, a.Kind, ns, a.Name, false)

		nri := tk.Clients.Dynamic.Resource(gvr)
		var got interface{}
		if namespaced {
			u, err := nri.Namespace(ns).Get(ctx, a.Name, metav1.GetOptions{})
			if err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("get %s %s/%s", a.Kind, ns, a.Name))
			}
			got = cleanUnstructured(u.Object)
		} else {
			u, err := nri.Get(ctx, a.Name, metav1.GetOptions{})
			if err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("get %s %s", a.Kind, a.Name))
			}
			got = cleanUnstructured(u.Object)
		}
		b, err := yaml.Marshal(got)
		if err != nil {
			return rpc.ErrorResult("encode %s as YAML: %v", a.Kind, err), nil
		}
		return rpc.TextResult(string(b)), nil
	}
}

// resolveGVR finds the GroupVersionResource for a kind using discovery. It scans
// all API resources; if apiVersion is given it restricts to that group/version.
func resolveGVR(ctx context.Context, tk *tools.Toolkit, kind, apiVersion string) (schema.GroupVersionResource, bool, error) {
	_, lists, err := tk.Clients.Discovery.ServerGroupsAndResources()
	if err != nil && len(lists) == 0 {
		return schema.GroupVersionResource{}, false, fmt.Errorf("discovery failed: %w", err)
	}
	wantGroup, wantVersion := "", ""
	if apiVersion != "" {
		gv := apiVersion
		if i := strings.Index(apiVersion, "/"); i >= 0 {
			wantGroup = apiVersion[:i]
			wantVersion = apiVersion[i+1:]
		} else {
			wantVersion = apiVersion // core group, e.g. "v1"
		}
		_ = gv
	}
	for _, rl := range lists {
		group, version := splitGV(rl.GroupVersion)
		if wantVersion != "" && version != wantVersion {
			continue
		}
		if wantGroup != "" && group != wantGroup {
			continue
		}
		for _, r := range rl.APIResources {
			if !strings.EqualFold(r.Kind, kind) {
				continue
			}
			// Skip subresources (pods/exec etc.) which share a kind sometimes.
			if strings.Contains(r.Name, "/") {
				continue
			}
			return schema.GroupVersionResource{Group: group, Version: version, Resource: r.Name}, r.Namespaced, nil
		}
	}
	return schema.GroupVersionResource{}, false, fmt.Errorf("kind %q (apiVersion %q) not found; call get_api_resources to list available kinds", kind, apiVersion)
}

func splitGV(gv string) (string, string) {
	if i := strings.Index(gv, "/"); i >= 0 {
		return gv[:i], gv[i+1:]
	}
	return "", gv
}

// cleanUnstructured strips noisy managed-by fields that bloat output but keeps
// everything useful for debugging.
func cleanUnstructured(obj map[string]any) map[string]any {
	if obj == nil {
		return obj
	}
	if md, ok := obj["metadata"].(map[string]any); ok {
		delete(md, "managedFields")
	}
	// Drop the status field's raw bytes only if huge? Keep status — it's useful.
	return obj
}
