package troubleshoot

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

func registerRollout(tk *tools.Toolkit, s *mcp.Server) {
	mcp.AddTool(s, tools.NewReadTool("rollout_status",
		"Report the rollout status of a Deployment, StatefulSet, or DaemonSet: whether the latest revision is fully rolled out, and if not, why."),
		tools.Wrap(tk, "rollout_status", read, rolloutStatus(tk)))

	mcp.AddTool(s, tools.NewReadTool("rollout_history",
		"Show revision history for a Deployment (its ReplicaSets with revision annotations and change-cause)."),
		tools.Wrap(tk, "rollout_history", read, rolloutHistory(tk)))
}

type rolloutArgs struct {
	Kind      string `json:"kind" jsonschema:"the workload kind: Deployment, StatefulSet, or DaemonSet"`
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name      string `json:"name" jsonschema:"the workload name"`
}

func rolloutStatus(tk *tools.Toolkit) tools.ToolFunc[rolloutStatusArgs] {
	return func(ctx context.Context, a rolloutStatusArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.Policy.CheckNamespace(ns); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		kind := strings.ToLower(a.Kind)
		audit.Attach(ctx, a.Kind, ns, a.Name, false)

		switch kind {
		case "deployment", "deploy":
			d, err := tk.Clients.Core.AppsV1().Deployments(ns).Get(ctx, a.Name, metav1.GetOptions{})
			if err != nil {
				return nil, tools.RPCStatusError(err, "get deployment "+ns+"/"+a.Name)
			}
			return rpc.TextResult(deploymentRolloutStatus(d)), nil
		case "statefulset", "sts":
			s, err := tk.Clients.Core.AppsV1().StatefulSets(ns).Get(ctx, a.Name, metav1.GetOptions{})
			if err != nil {
				return nil, tools.RPCStatusError(err, "get statefulset "+ns+"/"+a.Name)
			}
			return rpc.TextResult(statefulSetRolloutStatus(s)), nil
		case "daemonset", "ds":
			d, err := tk.Clients.Core.AppsV1().DaemonSets(ns).Get(ctx, a.Name, metav1.GetOptions{})
			if err != nil {
				return nil, tools.RPCStatusError(err, "get daemonset "+ns+"/"+a.Name)
			}
			return rpc.TextResult(daemonSetRolloutStatus(d)), nil
		default:
			return rpc.ErrorResult("unsupported kind %q (use Deployment, StatefulSet, or DaemonSet)", a.Kind), nil
		}
	}
}

type rolloutStatusArgs struct {
	Kind      string `json:"kind" jsonschema:"the workload kind: Deployment, StatefulSet, or DaemonSet"`
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name      string `json:"name" jsonschema:"the workload name"`
}

func deploymentRolloutStatus(d *appsv1.Deployment) string {
	if d.Generation <= d.Status.ObservedGeneration && d.Status.UpdatedReplicas == *d.Spec.Replicas &&
		d.Status.Replicas == d.Status.UpdatedReplicas && d.Status.AvailableReplicas >= d.Status.UpdatedReplicas {
		return fmt.Sprintf("deployment %s/%s successfully rolled out (%d/%d updated and available).\n", d.Namespace, d.Name, d.Status.UpdatedReplicas, *d.Spec.Replicas)
	}
	for _, c := range d.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionFalse {
			return fmt.Sprintf("deployment %s/%s rollout STALLED: %s\nReason: %s\n\nUse rollout_history to inspect revisions and consider rollout_restart or applying a fix.\n", d.Namespace, d.Name, c.Message, c.Reason)
		}
		if c.Type == appsv1.DeploymentReplicaFailure && c.Status == corev1.ConditionTrue {
			return fmt.Sprintf("deployment %s/%s has replica failures: %s\n", d.Namespace, d.Name, c.Message)
		}
	}
	return fmt.Sprintf("deployment %s/%s rollout IN PROGRESS: %d/%d updated, %d/%d available.\nWaiting for rollout to finish...\n",
		d.Namespace, d.Name, d.Status.UpdatedReplicas, *d.Spec.Replicas, d.Status.AvailableReplicas, *d.Spec.Replicas)
}

func statefulSetRolloutStatus(s *appsv1.StatefulSet) string {
	desired := int32(0)
	if s.Spec.Replicas != nil {
		desired = *s.Spec.Replicas
	}
	if s.Spec.UpdateStrategy.Type == appsv1.RollingUpdateStatefulSetStrategyType &&
		s.Status.UpdateRevision != s.Status.CurrentRevision &&
		s.Status.UpdatedReplicas < desired {
		return fmt.Sprintf("statefulset %s/%s rollout IN PROGRESS: %d/%d pods updated to revision %s.\n",
			s.Namespace, s.Name, s.Status.UpdatedReplicas, desired, s.Status.UpdateRevision)
	}
	return fmt.Sprintf("statefulset %s/%s rolled out (revision %s, %d/%d ready).\n",
		s.Namespace, s.Name, s.Status.CurrentRevision, s.Status.ReadyReplicas, desired)
}

func daemonSetRolloutStatus(d *appsv1.DaemonSet) string {
	if d.Status.UpdatedNumberScheduled < d.Status.DesiredNumberScheduled {
		return fmt.Sprintf("daemonset %s/%s rollout IN PROGRESS: %d of %d updated pods scheduled.\n",
			d.Namespace, d.Name, d.Status.UpdatedNumberScheduled, d.Status.DesiredNumberScheduled)
	}
	return fmt.Sprintf("daemonset %s/%s rolled out (%d/%d updated and scheduled).\n",
		d.Namespace, d.Name, d.Status.UpdatedNumberScheduled, d.Status.DesiredNumberScheduled)
}

func rolloutHistory(tk *tools.Toolkit) tools.ToolFunc[rolloutArgs] {
	return func(ctx context.Context, a rolloutArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		audit.Attach(ctx, "Deployment", ns, a.Name, false)
		// History = ReplicaSets owned by the deployment, ordered by revision.
		sets, err := tk.Clients.Core.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{LabelSelector: ""})
		if err != nil {
			return nil, tools.RPCStatusError(err, "list replicasets")
		}
		type rev struct {
			revision, changeCause, image string
			rs                           *appsv1.ReplicaSet
		}
		var revs []rev
		for i := range sets.Items {
			rs := &sets.Items[i]
			if !isOwnedBy(rs.OwnerReferences, a.Name, "Deployment") {
				continue
			}
			r := rev{rs: rs, revision: rs.Annotations["deployment.kubernetes.io/revision"], changeCause: rs.Annotations["kubernetes.io/change-cause"]}
			if len(rs.Spec.Template.Spec.Containers) > 0 {
				r.image = rs.Spec.Template.Spec.Containers[0].Image
			}
			if r.revision == "" {
				r.revision = "?"
			}
			revs = append(revs, r)
		}
		sort.Slice(revs, func(i, j int) bool { return revs[i].revision > revs[j].revision })

		t := rpc.NewTable("REVISION", "CHANGE-CAUSE", "IMAGE", "AGE")
		for _, r := range revs {
			cc := r.changeCause
			if cc == "" {
				cc = "<none>"
			}
			t.AddRow(r.revision, cc, r.image, tools.AgeStr(r.rs.CreationTimestamp))
		}
		hdr := fmt.Sprintf("Rollout history for Deployment %s/%s\n\n", ns, a.Name)
		if len(revs) == 0 {
			return rpc.TextResult(hdr + "No revisions found.\n"), nil
		}
		// Mark current revision.
		var cur string
		if len(revs) > 0 {
			cur = revs[len(revs)-1].revision
		}
		return rpc.TextResult(hdr + t.Render() + fmt.Sprintf("\nCurrent revision: %s\n", cur)), nil
	}
}

func isOwnedBy(refs []metav1.OwnerReference, name, kind string) bool {
	for _, r := range refs {
		if r.Kind == kind && r.Name == name && r.Controller != nil && *r.Controller {
			return true
		}
	}
	return false
}
