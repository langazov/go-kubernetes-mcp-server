package workloads

import (
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

// podStatus returns the human-readable status string for a pod.
func podStatus(p corev1.Pod) string {
	if p.Status.Phase == corev1.PodFailed {
		return "Failed"
	}
	if p.DeletionTimestamp != nil {
		return "Terminating"
	}
	// Pending with a specific waiting reason (e.g. ImagePullBackOff) is more useful.
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "ContainerCreating" {
			return cs.State.Waiting.Reason
		}
	}
	return string(p.Status.Phase)
}

func podReady(p corev1.Pod) string {
	ready, total := 0, 0
	for _, cs := range p.Status.ContainerStatuses {
		total++
		if cs.Ready {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, total)
}

func podRestarts(p corev1.Pod) string {
	var r int32
	for _, cs := range p.Status.ContainerStatuses {
		r += cs.RestartCount
	}
	return fmt.Sprintf("%d", r)
}

func nodeName(p corev1.Pod) string {
	if p.Spec.NodeName == "" {
		return "<none>"
	}
	return p.Spec.NodeName
}

// describePod renders a kubectl-describe-style report for a pod.
func describePod(p *corev1.Pod) string {
	var b strings.Builder
	ns := p.Namespace
	fmt.Fprintf(&b, "Name:              %s\n", p.Name)
	fmt.Fprintf(&b, "Namespace:         %s\n", ns)
	fmt.Fprintf(&b, "Status:            %s\n", podStatus(*p))
	fmt.Fprintf(&b, "Ready:             %s\n", podReady(*p))
	fmt.Fprintf(&b, "Restarts:          %s\n", podRestarts(*p))
	fmt.Fprintf(&b, "Node:              %s\n", nodeName(*p))
	if p.Status.PodIP != "" {
		fmt.Fprintf(&b, "Pod IP:            %s\n", p.Status.PodIP)
	}
	if p.Status.HostIP != "" {
		fmt.Fprintf(&b, "Host IP:           %s\n", p.Status.HostIP)
	}
	fmt.Fprintf(&b, "Age:               %s\n", tools.AgeStr(p.CreationTimestamp))
	if len(p.Labels) > 0 {
		fmt.Fprintf(&b, "Labels:            %s\n", formatMap(p.Labels))
	}
	if p.Spec.ServiceAccountName != "" {
		fmt.Fprintf(&b, "Service Account:   %s\n", p.Spec.ServiceAccountName)
	}

	if len(p.Spec.Containers) > 0 {
		b.WriteString("\nContainers:\n")
		for _, c := range p.Spec.Containers {
			describeContainer(&b, c, containerStatus(p, c.Name))
		}
	}
	if len(p.Status.Conditions) > 0 {
		b.WriteString("\nConditions:\n")
		ct := rpc.NewTable("TYPE", "STATUS", "REASON", "MESSAGE")
		for _, c := range p.Status.Conditions {
			ct.AddRow(string(c.Type), string(c.Status), c.Reason, tools.TruncLen(c.Message, 70))
		}
		b.WriteString(ct.Render())
	}
	return b.String()
}

func describeContainer(b *strings.Builder, c corev1.Container, cs *corev1.ContainerStatus) {
	fmt.Fprintf(b, "  %s:\n", c.Name)
	fmt.Fprintf(b, "    Image:      %s\n", c.Image)
	if cs != nil {
		switch {
		case cs.State.Running != nil:
			fmt.Fprintf(b, "    State:      Running (since %s)\n", cs.State.Running.StartedAt)
		case cs.State.Waiting != nil:
			fmt.Fprintf(b, "    State:      Waiting (%s) — %s\n", cs.State.Waiting.Reason, tools.TruncLen(cs.State.Waiting.Message, 80))
		case cs.State.Terminated != nil:
			fmt.Fprintf(b, "    State:      Terminated (%s) exit %d — %s\n", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode, tools.TruncLen(cs.State.Terminated.Message, 80))
		}
		fmt.Fprintf(b, "    Ready:      %t\n", cs.Ready)
		fmt.Fprintf(b, "    Restarts:   %d\n", cs.RestartCount)
	}
	if len(c.Ports) > 0 {
		var ports []string
		for _, p := range c.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", p.ContainerPort, p.Protocol))
		}
		fmt.Fprintf(b, "    Ports:      %s\n", strings.Join(ports, ", "))
	}
}

func containerStatus(p *corev1.Pod, name string) *corev1.ContainerStatus {
	for i := range p.Status.ContainerStatuses {
		if p.Status.ContainerStatuses[i].Name == name {
			return &p.Status.ContainerStatuses[i]
		}
	}
	return nil
}

func formatMap(m map[string]string) string {
	if len(m) == 0 {
		return "<none>"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}

// deploymentRow renders columns for a Deployment.
func deploymentRow(d appsv1.Deployment) []string {
	ready := fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, *d.Spec.Replicas)
	updated := fmt.Sprintf("%d", d.Status.UpdatedReplicas)
	avail := fmt.Sprintf("%d", d.Status.AvailableReplicas)
	return []string{d.Namespace, d.Name, ready, updated, avail, tools.AgeStr(d.CreationTimestamp)}
}

func controllerStatus(conds []appsv1.DeploymentCondition) string {
	for _, c := range conds {
		if c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionFalse {
			return "Progressing:false"
		}
	}
	for _, c := range conds {
		if c.Type == appsv1.DeploymentAvailable && c.Status == corev1.ConditionFalse {
			return "Unavailable"
		}
	}
	return "Available"
}

// jobStatus renders a short status for a Job.
func jobStatus(j batchv1.Job) string {
	if j.Status.Failed > 0 {
		return fmt.Sprintf("Failed(%d)", j.Status.Failed)
	}
	if j.Status.Succeeded > 0 {
		return fmt.Sprintf("Complete(%d)", j.Status.Succeeded)
	}
	if j.Status.Active > 0 {
		return fmt.Sprintf("Running(%d)", j.Status.Active)
	}
	if j.Spec.Suspend != nil && *j.Spec.Suspend {
		return "Suspended"
	}
	return "Pending"
}

// lastScheduleTime renders the last schedule time of a CronJob.
func lastScheduleTime(c batchv1.CronJob) string {
	if c.Status.LastScheduleTime == nil || c.Status.LastScheduleTime.IsZero() {
		return "<none>"
	}
	return tools.AgeStr(metav1.Time{Time: c.Status.LastScheduleTime.Time})
}
