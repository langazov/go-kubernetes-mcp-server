// Package operations implements mutating and destructive tools: server-side
// apply, patch, scale, rollout restart/undo, label/annotate, resource creation,
// deletion, and node cordon/drain. All are gated behind --allow-writes (and
// destructive ops behind --allow-destructive) at registration time.
package operations

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

var write = security.VerbWrite

// Register registers all mutating tools on s and returns the count added.
// It must only be called when policy.CanMutate() is true. Destructive tools are
// registered conditionally on policy.CanDestroy().
func Register(tk *tools.Toolkit, s *mcp.Server) int {
	n := 0

	mcp.AddTool(s, tools.NewWriteTool("apply_manifest",
		"Apply one or more YAML/JSON manifests using server-side apply (field manager 'k8s-mcp', force=true). Defaults to DRY RUN — pass dry_run=false to actually apply. Refuses cluster-scoped and kube-system targets unless the server allows privileged targets."),
		tools.Wrap(tk, "apply_manifest", write, applyManifest(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("patch",
		"Patch a resource with a JSON patch. Supports strategic-merge, merge, and json patch types. Use for targeted updates."),
		tools.Wrap(tk, "patch", write, patch(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("scale",
		"Scale a Deployment, StatefulSet, or ReplicaSet to a given replica count."),
		tools.Wrap(tk, "scale", write, scale(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("rollout_restart",
		"Restart a Deployment, StatefulSet, or DaemonSet (triggers a rolling restart)."),
		tools.Wrap(tk, "rollout_restart", write, rolloutRestart(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("rollout_undo",
		"Roll back a Deployment to the previous revision (or a specific revision)."),
		tools.Wrap(tk, "rollout_undo", write, rolloutUndo(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("label",
		"Add or update labels on a resource. Set overwrite=true to replace existing values."),
		tools.Wrap(tk, "label", write, label(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("annotate",
		"Add or update annotations on a resource. Set overwrite=true to replace existing values."),
		tools.Wrap(tk, "annotate", write, annotate(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("create_namespace",
		"Create a new namespace with optional labels."),
		tools.Wrap(tk, "create_namespace", security.VerbWrite, createNamespace(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("create_configmap",
		"Create a configmap from key/value data."),
		tools.Wrap(tk, "create_configmap", write, createConfigMap(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("update_configmap",
		"Replace a configmap's data."),
		tools.Wrap(tk, "update_configmap", write, updateConfigMap(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("create_secret",
		"Create a secret with string data. The data is stored securely and only hashes are echoed back."),
		tools.Wrap(tk, "create_secret", write, createSecret(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("create_service",
		"Create a ClusterIP service exposing a set of pods via a label selector and port mapping."),
		tools.Wrap(tk, "create_service", write, createService(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("uncordon_node",
		"Mark a node schedulable again (undo cordon)."),
		tools.Wrap(tk, "uncordon_node", write, uncordonNode(tk)))
	n++

	// Destructive tools — only registered when --allow-destructive is set.
	if tk.Policy.CanDestroy() {
		n += registerDestructive(tk, s)
	}

	return n
}

// ----- apply_manifest -----

type applyArgs struct {
	Manifest     string `json:"manifest" jsonschema:"YAML or JSON manifest(s); multiple documents separated by ---"`
	Namespace    string `json:"namespace,omitempty" jsonschema:"default namespace for namespaced manifests without one (defaults to 'default')"`
	DryRun       *bool  `json:"dry_run,omitempty" jsonschema:"if true (default), validate without persisting changes"`
	FieldManager string `json:"field_manager,omitempty" jsonschema:"server-side-apply field manager name (defaults to 'k8s-mcp')"`
}

func applyManifest(tk *tools.Toolkit) tools.ToolFunc[applyArgs] {
	return func(ctx context.Context, a applyArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckMutating(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		dryRun := true
		if a.DryRun != nil {
			dryRun = *a.DryRun
		}
		fm := a.FieldManager
		if fm == "" {
			fm = "k8s-mcp"
		}

		objs, err := decodeManifests(a.Manifest)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if len(objs) == 0 {
			return rpc.ErrorResult("manifest contained no documents"), nil
		}

		defaultNS := tools.ResolveNS(a.Namespace)
		var b strings.Builder
		for i := range objs {
			obj := &objs[i]
			kind := obj.GetKind()
			apiVersion := obj.GetAPIVersion()
			name := obj.GetName()
			ns := obj.GetNamespace()
			if ns == "" {
				ns = defaultNS
				obj.SetNamespace(ns)
			}
			if err := validateIdent(kind, name); err != nil {
				return rpc.ErrorResult("document %d: %v", i+1, err), nil
			}
			gvr, namespaced, err := tools.ResolveGVR(ctx, tk, kind, apiVersion)
			if err != nil {
				return rpc.ErrorResult("document %d (%s): %v", i+1, kind, err), nil
			}
			if err := tk.Policy.CheckManifestKind(gvrKindKey(kind, apiVersion)); err != nil {
				return rpc.ErrorResult("%v", err), nil
			}
			if err := tk.Policy.CheckTarget(ns, !namespaced); err != nil {
				return rpc.ErrorResult("%v", err), nil
			}
			if ns != "" {
				if err := tk.Policy.CheckNamespace(ns); err != nil {
					return rpc.ErrorResult("%v", err), nil
				}
			}
			audit.Attach(ctx, kind, ns, name, dryRun)

			applyOpts := metav1.ApplyOptions{FieldManager: fm, Force: true}
			if dryRun {
				applyOpts.DryRun = []string{"All"}
			}
			nri := tk.Clients.Dynamic.Resource(gvr)
			if namespaced {
				_, err = nri.Namespace(ns).Apply(ctx, name, obj, applyOpts)
			} else {
				_, err = nri.Apply(ctx, name, obj, applyOpts)
			}
			if err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("apply %s %s/%s (dry_run=%t)", kind, ns, name, dryRun))
			}
			verb := "applied"
			if dryRun {
				verb = "validated (dry run)"
			}
			fmt.Fprintf(&b, "%s %s in namespace %q %s\n", kind, name, ns, verb)
		}
		if dryRun {
			b.WriteString("\nNOTE: this was a dry run. Pass dry_run=false to apply for real.\n")
		}
		return rpc.TextResult(b.String()), nil
	}
}

func gvrKindKey(kind, apiVersion string) string {
	return kind + "." + apiVersion
}

// ----- patch -----

type patchArgs struct {
	Kind       string `json:"kind" jsonschema:"the resource kind (e.g. Deployment)"`
	APIVersion string `json:"api_version,omitempty" jsonschema:"the group/version (e.g. apps/v1); omit to auto-detect"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name       string `json:"name" jsonschema:"the resource name"`
	Patch      string `json:"patch" jsonschema:"the patch document (JSON for merge/json, JSON or YAML for strategic)"`
	PatchType  string `json:"patch_type,omitempty" jsonschema:"patch strategy: strategic (default), merge, or json"`
	DryRun     bool   `json:"dry_run,omitempty" jsonschema:"if true, validate without applying"`
}

func patch(tk *tools.Toolkit) tools.ToolFunc[patchArgs] {
	return func(ctx context.Context, a patchArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckMutating(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		pt, err := parsePatchType(a.PatchType)
		if err != nil {
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
		audit.Attach(ctx, a.Kind, ns, a.Name, a.DryRun)

		opts := metav1.PatchOptions{}
		if a.DryRun {
			opts.DryRun = []string{"All"}
		}
		nri := tk.Clients.Dynamic.Resource(gvr)
		var res *unstructured.Unstructured
		if namespaced {
			res, err = nri.Namespace(ns).Patch(ctx, a.Name, pt, []byte(a.Patch), opts)
		} else {
			res, err = nri.Patch(ctx, a.Name, pt, []byte(a.Patch), opts)
		}
		if err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("patch %s %s/%s", a.Kind, ns, a.Name))
		}
		verb := "patched"
		if a.DryRun {
			verb = "validated (dry run)"
		}
		return rpc.TextResult(fmt.Sprintf("%s %s/%s %s. resourceVersion=%s\n", a.Kind, ns, a.Name, verb, res.GetResourceVersion())), nil
	}
}

func parsePatchType(s string) (types.PatchType, error) {
	switch strings.ToLower(s) {
	case "", "strategic":
		return types.StrategicMergePatchType, nil
	case "merge":
		return types.MergePatchType, nil
	case "json":
		return types.JSONPatchType, nil
	}
	return "", fmt.Errorf("invalid patch_type %q: use strategic, merge, or json", s)
}

// ----- helpers shared across mutating tools -----

func policyTarget(tk *tools.Toolkit, ns string, clusterScoped bool) error {
	if err := tk.Policy.CheckTarget(ns, clusterScoped); err != nil {
		return err
	}
	if !clusterScoped {
		return tk.Policy.CheckNamespace(ns)
	}
	return nil
}

func validateIdent(kind, name string) error {
	if kind == "" {
		return fmt.Errorf("manifest missing kind")
	}
	if name == "" {
		return fmt.Errorf("manifest missing metadata.name")
	}
	return nil
}

// decodeManifests parses a multi-document YAML/JSON blob into unstructured
// objects, dropping empty documents.
func decodeManifests(raw string) ([]unstructured.Unstructured, error) {
	// yaml.NewYAMLOrJSONDecoder handles both YAML and JSON and multi-doc.
	dec := yaml.NewYAMLOrJSONDecoder(strings.NewReader(raw), 4096)
	var out []unstructured.Unstructured
	for {
		obj := unstructured.Unstructured{}
		if err := dec.Decode(&obj); err != nil {
			if err.Error() == "EOF" || strings.Contains(err.Error(), "EOF") {
				break
			}
			break
		}
		if len(obj.Object) == 0 {
			continue
		}
		out = append(out, obj)
	}
	return out, nil
}

// gvrOf is a small wrapper for mutating tools that already validated args.
func gvrOf(ctx context.Context, tk *tools.Toolkit, kind, apiVersion, ns string) (schema.GroupVersionResource, bool, string, error) {
	gvr, namespaced, err := tools.ResolveGVR(ctx, tk, kind, apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, false, "", err
	}
	resolved := ns
	if namespaced {
		resolved = tools.ResolveNS(ns)
	}
	return gvr, namespaced, resolved, nil
}
