package troubleshoot

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

func registerDiagnose(tk *tools.Toolkit, s *mcp.Server) {
	mcp.AddTool(s, tools.NewReadTool("diagnose_pod",
		"Diagnose why a pod is not running/healthy. Inspects status, conditions, recent events, and tail logs to classify common failures (CrashLoopBackOff, ImagePullBackOff, OOMKilled, probe failures, scheduling/PVC issues) and suggests next steps."),
		tools.Wrap(tk, "diagnose_pod", read, diagnosePod(tk)))

	mcp.AddTool(s, tools.NewReadTool("diagnose_node",
		"Diagnose node health: readiness, pressure conditions (Memory/Disk/PID), taints, and recent events. Returns findings and suggested next steps."),
		tools.Wrap(tk, "diagnose_node", read, diagnoseNode(tk)))
}

type diagnosePodArgs struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name      string `json:"name" jsonschema:"the pod name"`
}

func diagnosePod(tk *tools.Toolkit) tools.ToolFunc[diagnosePodArgs] {
	return func(ctx context.Context, a diagnosePodArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.Policy.CheckNamespace(ns); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Pod", ns, a.Name, false)

		pod, err := tk.Clients.Core.CoreV1().Pods(ns).Get(ctx, a.Name, metav1.GetOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("get pod %s/%s", ns, a.Name))
		}
		events, _ := tk.Clients.Core.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("involvedObject.kind=Pod,involvedObject.name=%s", a.Name),
		})

		f := newFindings()
		classifyPod(f, pod, events.Items)

		// If a container is crash-looping, grab a few recent log lines.
		if c := failingContainer(pod); c != "" {
			if tail := tailLogs(ctx, tk, ns, a.Name, c, true); tail != "" {
				f.add(info, "Recent logs from container "+c+":\n"+tail)
			}
		}

		return rpc.TextResult(f.render(pod.Name, pod.Namespace, podStatusStr(pod))), nil
	}
}

func diagnoseNode(tk *tools.Toolkit) tools.ToolFunc[tools.NamespaceNameArgs] {
	return func(ctx context.Context, a tools.NamespaceNameArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Node", "", a.Name, false)
		node, err := tk.Clients.Core.CoreV1().Nodes().Get(ctx, a.Name, metav1.GetOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, "get node "+a.Name)
		}
		events, _ := tk.Clients.Core.CoreV1().Events("").List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("involvedObject.kind=Node,involvedObject.name=%s", a.Name),
		})

		f := newFindings()
		classifyNode(f, node, events.Items)
		return rpc.TextResult(f.render(node.Name, "", nodeReadyStr(node))), nil
	}
}

// ----- classification -----

func classifyPod(f *findings, pod *corev1.Pod, events []corev1.Event) {
	// Scheduling / pending.
	if pod.Status.Phase == corev1.PodPending {
		if cond := getCondition(pod, corev1.PodScheduled); cond != nil && cond.Status == corev1.ConditionFalse {
			if strings.Contains(cond.Message, "Insufficient") || strings.Contains(cond.Reason, "Unschedulable") {
				f.add(warn, "Pod is Pending and Unschedulable: "+cond.Message+
					"\n  → check node capacity/taints with `describe_node` or `top_nodes`; consider scaling nodes or relaxing requests/tolerations.")
			} else {
				f.add(info, "Pod is Pending (not yet scheduled): "+cond.Message)
			}
		} else {
			f.add(info, "Pod is Pending. Common causes: scheduling, image pull, PVC pending, or waiting on config/secrets.")
		}
	}

	// Container states.
	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			msg := cs.State.Waiting.Message
			switch reason {
			case "ImagePullBackOff", "ErrImagePull", "ImagePullError":
				f.add(critical, fmt.Sprintf("Container %s is in %s: %s\n  → the image cannot be pulled. Check the image name/tag and registry credentials (imagePullSecrets).", cs.Name, reason, msg))
			case "CrashLoopBackOff":
				f.add(critical, fmt.Sprintf("Container %s is in CrashLoopBackOff: it starts then exits repeatedly.\n  → call `get_logs` with previous=true to see the crash cause; check args/env/config, liveness probes, and OOM limits.", cs.Name))
			case "CreateContainerConfigError", "CreateContainerError":
				f.add(critical, fmt.Sprintf("Container %s has %s: %s\n  → usually a missing ConfigMap/Secret or invalid env var reference.", cs.Name, reason, msg))
			case "InvalidImageName":
				f.add(critical, fmt.Sprintf("Container %s has InvalidImageName: %s", cs.Name, msg))
			case "ContainerCreating":
				f.add(info, fmt.Sprintf("Container %s is still being created (waiting on runtime/storage/network).", cs.Name))
			default:
				f.add(warn, fmt.Sprintf("Container %s is waiting (%s): %s", cs.Name, reason, msg))
			}
		}
		if cs.State.Terminated != nil {
			t := cs.State.Terminated
			switch {
			case t.Reason == "OOMKilled" || t.ExitCode == 137:
				f.add(critical, fmt.Sprintf("Container %s was OOMKilled (exit %d). It exceeded its memory limit.\n  → raise resources.limits.memory, or investigate a memory leak.", cs.Name, t.ExitCode))
			case t.ExitCode != 0:
				f.add(critical, fmt.Sprintf("Container %s terminated (reason=%s, exit=%d): %s\n  → call `get_logs` with previous=true for the error output.", cs.Name, t.Reason, t.ExitCode, t.Message))
			}
		}
	}

	// Probe failures surface via events and conditions.
	for _, e := range events {
		switch e.Reason {
		case "Unhealthy":
			f.add(warn, fmt.Sprintf("Probe failure: %s (Liveness/Readiness probe is failing). %s", e.Reason, e.Message))
		case "BackOff":
			// already covered by CrashLoopBackOff; skip to avoid noise.
		case "FailedScheduling":
			f.add(warn, "FailedScheduling: "+e.Message)
		case "FailedMount", "FailedAttachVolume":
			f.add(critical, "Volume problem: "+e.Message+"\n  → check PVC status with `describe` kind=PersistentVolumeClaim.")
		}
	}

	// PVCs referenced by the pod that are not Bound.
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim != nil {
			f.addPVCNote(v.PersistentVolumeClaim.ClaimName)
		}
	}

	// Readiness gate.
	if !isPodReady(*pod) && pod.Status.Phase == corev1.PodRunning {
		f.add(warn, "Pod is Running but not Ready. A readiness probe may be failing or a container is unhealthy. Check `list_events` and container statuses.")
	}

	if len(f.items) == 0 {
		f.add(ok, "No issues detected. All containers are running and ready.")
	}
}

func classifyNode(f *findings, node *corev1.Node, events []corev1.Event) {
	if !isNodeReady(*node) {
		if c := getNodeCondition(node, corev1.NodeReady); c != nil {
			f.add(critical, fmt.Sprintf("Node is NotReady: %s — %s\n  → investigate kubelet on the node; check node events and conditions.", c.Reason, c.Message))
		}
	}
	for _, ct := range []corev1.NodeConditionType{
		corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure, corev1.NodeNetworkUnavailable,
	} {
		if c := getNodeCondition(node, ct); c != nil && c.Status == corev1.ConditionTrue {
			f.add(critical, fmt.Sprintf("Node under %s: %s — %s\n  → %s", ct, c.Reason, c.Message, pressureHint(ct)))
		}
	}
	for _, t := range node.Spec.Taints {
		if t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute {
			f.add(warn, fmt.Sprintf("Node has taint %s=%s:%s — pods without a matching toleration will not schedule here.", t.Key, t.Value, t.Effect))
		}
	}
	for _, e := range events {
		if e.Reason == "NodeNotReady" || e.Reason == "NodeUnschedulable" {
			f.add(warn, "Event: "+e.Reason+" — "+e.Message)
		}
	}
	if len(f.items) == 0 {
		f.add(ok, "Node is Ready with no pressure conditions or blocking taints.")
	}
}

func pressureHint(ct corev1.NodeConditionType) string {
	switch ct {
	case corev1.NodeMemoryPressure:
		return "free up memory, evict large pods, or add memory/replicas."
	case corev1.NodeDiskPressure:
		return "clean up images/logs, check kubelet garbage collection, or expand storage."
	case corev1.NodePIDPressure:
		return "reduce pod/process density on this node."
	case corev1.NodeNetworkUnavailable:
		return "check the node's CNI/network plugin."
	}
	return ""
}

// ----- helpers -----

type severity string

const (
	ok       severity = "OK"
	info     severity = "INFO"
	warn     severity = "WARN"
	critical severity = "CRITICAL"
)

type finding struct {
	sev  severity
	text string
}

type findings struct {
	items []finding
	pvcs  []string
}

func newFindings() *findings { return &findings{} }

func (f *findings) add(s severity, text string) { f.items = append(f.items, finding{s, text}) }
func (f *findings) addPVCNote(name string)      { f.pvcs = append(f.pvcs, name) }

func (f *findings) render(name, ns, status string) string {
	var b strings.Builder
	if ns != "" {
		fmt.Fprintf(&b, "Diagnosis for Pod %s/%s\n", ns, name)
	} else {
		fmt.Fprintf(&b, "Diagnosis for Node %s\n", name)
	}
	fmt.Fprintf(&b, "Current status: %s\n\n", status)
	for _, it := range f.items {
		fmt.Fprintf(&b, "[%s] %s\n\n", strings.ToUpper(string(it.sev)), it.text)
	}
	if len(f.pvcs) > 0 {
		fmt.Fprintf(&b, "Mounted PVCs: %s (use `describe` kind=PersistentVolumeClaim to verify each is Bound)\n", strings.Join(f.pvcs, ", "))
	}
	return b.String()
}

func podStatusStr(p *corev1.Pod) string {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "ContainerCreating" {
			return cs.State.Waiting.Reason
		}
	}
	return string(p.Status.Phase)
}

func nodeReadyStr(n *corev1.Node) string {
	if isNodeReady(*n) {
		return "Ready"
	}
	return "NotReady"
}

func failingContainer(p *corev1.Pod) string {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && (cs.State.Waiting.Reason == "CrashLoopBackOff" || cs.State.Terminated != nil) {
			return cs.Name
		}
	}
	return ""
}

func tailLogs(ctx context.Context, tk *tools.Toolkit, ns, pod, container string, previous bool) string {
	tail := int64(15)
	opts := &corev1.PodLogOptions{Container: container, TailLines: &tail, Previous: previous}
	stream, err := tk.Clients.Core.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return ""
	}
	defer stream.Close()
	b, _ := io.ReadAll(stream)
	out := strings.TrimRight(string(b), "\n")
	if len(out) > 2048 {
		out = out[:2048] + "…"
	}
	return out
}

func getCondition(p *corev1.Pod, t corev1.PodConditionType) *corev1.PodCondition {
	for i := range p.Status.Conditions {
		if p.Status.Conditions[i].Type == t {
			return &p.Status.Conditions[i]
		}
	}
	return nil
}

func getNodeCondition(n *corev1.Node, t corev1.NodeConditionType) *corev1.NodeCondition {
	for i := range n.Status.Conditions {
		if n.Status.Conditions[i].Type == t {
			return &n.Status.Conditions[i]
		}
	}
	return nil
}

func isNodeReady(n corev1.Node) bool {
	if c := getNodeCondition(&n, corev1.NodeReady); c != nil {
		return c.Status == corev1.ConditionTrue
	}
	return false
}

func isPodReady(p corev1.Pod) bool {
	if c := getCondition(&p, corev1.PodReady); c != nil {
		return c.Status == corev1.ConditionTrue
	}
	return false
}
