package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

// ----- scale -----

type scaleArgs struct {
	Kind      string `json:"kind" jsonschema:"the workload kind: Deployment, StatefulSet, or ReplicaSet"`
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name      string `json:"name" jsonschema:"the workload name"`
	Replicas  int32  `json:"replicas" jsonschema:"the desired replica count"`
	DryRun    bool   `json:"dry_run,omitempty" jsonschema:"if true, validate without applying"`
}

func scale(tk *tools.Toolkit) tools.ToolFunc[scaleArgs] {
	return func(ctx context.Context, a scaleArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckMutating(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := policyTarget(tk, ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, a.Kind, ns, a.Name, a.DryRun)

		opts := metav1.UpdateOptions{}
		if a.DryRun {
			opts.DryRun = []string{"All"}
		}
		scale := &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{Name: a.Name, Namespace: ns},
			Spec:       autoscalingv1.ScaleSpec{Replicas: a.Replicas},
		}

		var before int32
		kind := strings.ToLower(a.Kind)
		switch kind {
		case "deployment", "deploy":
			cur, err := tk.Clients.Core.AppsV1().Deployments(ns).GetScale(ctx, a.Name, metav1.GetOptions{})
			if err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("get scale for deployment %s/%s", ns, a.Name))
			}
			before = cur.Spec.Replicas
			if _, err := tk.Clients.Core.AppsV1().Deployments(ns).UpdateScale(ctx, a.Name, scale, opts); err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("scale deployment %s/%s", ns, a.Name))
			}
		case "statefulset", "sts":
			cur, err := tk.Clients.Core.AppsV1().StatefulSets(ns).GetScale(ctx, a.Name, metav1.GetOptions{})
			if err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("get scale for statefulset %s/%s", ns, a.Name))
			}
			before = cur.Spec.Replicas
			if _, err := tk.Clients.Core.AppsV1().StatefulSets(ns).UpdateScale(ctx, a.Name, scale, opts); err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("scale statefulset %s/%s", ns, a.Name))
			}
		case "replicaset", "rs":
			cur, err := tk.Clients.Core.AppsV1().ReplicaSets(ns).GetScale(ctx, a.Name, metav1.GetOptions{})
			if err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("get scale for replicaset %s/%s", ns, a.Name))
			}
			before = cur.Spec.Replicas
			if _, err := tk.Clients.Core.AppsV1().ReplicaSets(ns).UpdateScale(ctx, a.Name, scale, opts); err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("scale replicaset %s/%s", ns, a.Name))
			}
		default:
			return rpc.ErrorResult("unsupported kind %q for scale (use Deployment, StatefulSet, or ReplicaSet)", a.Kind), nil
		}
		verb := "scaled"
		if a.DryRun {
			verb = "would scale (dry run)"
		}
		return rpc.TextResult(fmt.Sprintf("%s %s/%s %s from %d to %d replicas\n", a.Kind, ns, a.Name, verb, before, a.Replicas)), nil
	}
}

// ----- rollout restart / undo -----

type rolloutMutArgs struct {
	Kind      string `json:"kind" jsonschema:"the workload kind: Deployment, StatefulSet, or DaemonSet"`
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name      string `json:"name" jsonschema:"the workload name"`
	Revision  string `json:"revision,omitempty" jsonschema:"(rollout_undo only) a specific revision to roll back to; omit for the previous one"`
}

func rolloutRestart(tk *tools.Toolkit) tools.ToolFunc[rolloutMutArgs] {
	return func(ctx context.Context, a rolloutMutArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckMutating(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := policyTarget(tk, ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, a.Kind, ns, a.Name, false)

		// Inject the kubectl-style restart annotation with a fresh timestamp,
		// which triggers a rolling update.
		now := time.Now().Format(time.RFC3339Nano)
		patchData := map[string]any{
			"spec": map[string]any{
				"template": map[string]any{
					"metadata": map[string]any{
						"annotations": map[string]any{
							"kubectl.kubernetes.io/restartedAt": now,
						},
					},
				},
			},
		}
		return mutateController(ctx, tk, a.Kind, ns, a.Name, patchData, "restart")
	}
}

func rolloutUndo(tk *tools.Toolkit) tools.ToolFunc[rolloutMutArgs] {
	return func(ctx context.Context, a rolloutMutArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckMutating(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := policyTarget(tk, ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Deployment", ns, a.Name, false)

		if !strings.EqualFold(a.Kind, "deployment") {
			return rpc.ErrorResult("rollout_undo only supports Deployments (got %s)", a.Kind), nil
		}
		// Collect every ReplicaSet owned by this deployment, keyed by revision.
		sets, err := tk.Clients.Core.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, "list replicasets for undo")
		}
		byRev := map[string]*appsv1.ReplicaSet{}
		for i := range sets.Items {
			rs := &sets.Items[i]
			if !ownedBy(rs.OwnerReferences, a.Name, "Deployment") {
				continue
			}
			rev := rs.Annotations["deployment.kubernetes.io/revision"]
			if rev == "" {
				continue
			}
			byRev[rev] = rs
		}
		if len(byRev) == 0 {
			return rpc.ErrorResult("no rollout history found for deployment %s/%s", ns, a.Name), nil
		}

		var target *appsv1.ReplicaSet
		var targetRev string
		if a.Revision != "" {
			// Roll back to the specific requested revision.
			rs, ok := byRev[a.Revision]
			if !ok {
				return rpc.ErrorResult("revision %s not found for deployment %s/%s (have %s)", a.Revision, ns, a.Name, joinRevs(byRev)), nil
			}
			target, targetRev = rs, a.Revision
		} else {
			// Roll back to the previous revision: the second-highest revision
			// number (revisions are monotonic integers as strings).
			currentRev, _ := currentDeploymentRevision(ctx, tk, ns, a.Name)
			revisions := sortedRevisionsDesc(byRev)
			// Skip the current revision; pick the next one.
			for _, r := range revisions {
				if r == currentRev {
					continue
				}
				target, targetRev = byRev[r], r
				break
			}
			if target == nil {
				return rpc.ErrorResult("no previous revision found for deployment %s/%s (current revision %s)", ns, a.Name, currentRev), nil
			}
		}

		// Roll back by patching the deployment's template to the target RS template.
		tmplJSON, err := json.Marshal(target.Spec.Template)
		if err != nil {
			return rpc.ErrorResult("encode rollback template: %v", err), nil
		}
		patch := map[string]any{"spec": map[string]any{"template": json.RawMessage(tmplJSON)}}
		return mutateController(ctx, tk, "Deployment", ns, a.Name, patch, "rollback to revision "+targetRev)
	}
}

// currentDeploymentRevision returns the deployment's current revision annotation,
// or the highest ReplicaSet revision as a fallback.
func currentDeploymentRevision(ctx context.Context, tk *tools.Toolkit, ns, name string) (string, error) {
	d, err := tk.Clients.Core.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if r := d.Annotations["deployment.kubernetes.io/revision"]; r != "" {
		return r, nil
	}
	return "", nil
}

// sortedRevisionsDesc returns revisions sorted numerically descending.
func sortedRevisionsDesc(byRev map[string]*appsv1.ReplicaSet) []string {
	revs := make([]string, 0, len(byRev))
	for r := range byRev {
		revs = append(revs, r)
	}
	sort.Slice(revs, func(i, j int) bool {
		ai, _ := strconv.Atoi(revs[i])
		aj, _ := strconv.Atoi(revs[j])
		return ai > aj
	})
	return revs
}

func joinRevs(byRev map[string]*appsv1.ReplicaSet) string {
	revs := sortedRevisionsDesc(byRev)
	return strings.Join(revs, ", ")
}

// mutateController applies a strategic-merge patch to the supported controller.
func mutateController(ctx context.Context, tk *tools.Toolkit, kind, ns, name string, patch any, action string) (*mcp.CallToolResult, error) {
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return rpc.ErrorResult("encode patch: %v", err), nil
	}
	kind = strings.ToLower(kind)
	switch kind {
	case "deployment", "deploy":
		if _, err := tk.Clients.Core.AppsV1().Deployments(ns).Patch(ctx, name, typesMergePatch(), patchBytes, metav1.PatchOptions{}); err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("%s deployment %s/%s", action, ns, name))
		}
	case "statefulset", "sts":
		if _, err := tk.Clients.Core.AppsV1().StatefulSets(ns).Patch(ctx, name, typesMergePatch(), patchBytes, metav1.PatchOptions{}); err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("%s statefulset %s/%s", action, ns, name))
		}
	case "daemonset", "ds":
		if _, err := tk.Clients.Core.AppsV1().DaemonSets(ns).Patch(ctx, name, typesMergePatch(), patchBytes, metav1.PatchOptions{}); err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("%s daemonset %s/%s", action, ns, name))
		}
	default:
		return rpc.ErrorResult("unsupported kind %q (use Deployment, StatefulSet, or DaemonSet)", kind), nil
	}
	return rpc.TextResult(fmt.Sprintf("%s %s/%s: %s\n", kind, ns, name, action)), nil
}

// ----- label / annotate -----

type metaPatchArgs struct {
	Kind       string            `json:"kind" jsonschema:"the resource kind (e.g. Pod, Service)"`
	APIVersion string            `json:"api_version,omitempty" jsonschema:"the group/version; omit to auto-detect"`
	Namespace  string            `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name       string            `json:"name" jsonschema:"the resource name"`
	Items      map[string]string `json:"items" jsonschema:"key/value pairs to set"`
	Overwrite  bool              `json:"overwrite,omitempty" jsonschema:"if false, refuse to change existing keys"`
}

func label(tk *tools.Toolkit) tools.ToolFunc[metaPatchArgs] {
	return metaPatcher(tk, "label", "labels")
}

func annotate(tk *tools.Toolkit) tools.ToolFunc[metaPatchArgs] {
	return metaPatcher(tk, "annotate", "annotations")
}

func metaPatcher(tk *tools.Toolkit, action, field string) tools.ToolFunc[metaPatchArgs] {
	return func(ctx context.Context, a metaPatchArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckMutating(); err != nil {
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
		audit.Attach(ctx, a.Kind, ns, a.Name, false)

		nri := tk.Clients.Dynamic.Resource(gvr)
		var existing *unstructured.Unstructured
		var gerr error
		if namespaced {
			existing, gerr = nri.Namespace(ns).Get(ctx, a.Name, metav1.GetOptions{})
		} else {
			existing, gerr = nri.Get(ctx, a.Name, metav1.GetOptions{})
		}
		if gerr != nil {
			return nil, tools.RPCStatusError(gerr, fmt.Sprintf("get %s %s/%s", a.Kind, ns, a.Name))
		}
		cur := map[string]string{}
		if v, ok := existing.GetAnnotations()[field]; ok {
			_ = v
		}
		cur = existing.GetLabels()
		if field == "annotations" {
			cur = existing.GetAnnotations()
		}
		if !a.Overwrite {
			for k := range a.Items {
				if _, exists := cur[k]; exists {
					return rpc.ErrorResult("%s %q already exists on %s %s/%s; set overwrite=true to replace", action, k, a.Kind, ns, a.Name), nil
				}
			}
		}
		patch := map[string]any{
			"metadata": map[string]any{field: a.Items},
		}
		patchBytes, err := json.Marshal(patch)
		if err != nil {
			return rpc.ErrorResult("encode patch: %v", err), nil
		}
		if namespaced {
			if _, err := nri.Namespace(ns).Patch(ctx, a.Name, typesMergePatch(), patchBytes, metav1.PatchOptions{}); err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("%s %s %s/%s", action, a.Kind, ns, a.Name))
			}
		} else {
			if _, err := nri.Patch(ctx, a.Name, typesMergePatch(), patchBytes, metav1.PatchOptions{}); err != nil {
				return nil, tools.RPCStatusError(err, fmt.Sprintf("%s %s %s", action, a.Kind, a.Name))
			}
		}
		keys := make([]string, 0, len(a.Items))
		for k := range a.Items {
			keys = append(keys, k)
		}
		return rpc.TextResult(fmt.Sprintf("%sd %s %s/%s: %s\n", action, a.Kind, ns, a.Name, strings.Join(keys, ", "))), nil
	}
}

// ownedBy reports whether refs indicate ownership by name/kind.
func ownedBy(refs []metav1.OwnerReference, name, kind string) bool {
	for _, r := range refs {
		if r.Kind == kind && r.Name == name && r.Controller != nil && *r.Controller {
			return true
		}
	}
	return false
}

func typesMergePatch() types.PatchType { return types.MergePatchType }
