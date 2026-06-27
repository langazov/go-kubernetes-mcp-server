package workloads

import (
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

func intstr(a, b int) string { return fmt.Sprintf("%d/%d", a, b) }

func strnum(n int32) string { return fmt.Sprintf("%d", n) }

func cronSuspend(c batchv1.CronJob) string {
	if c.Spec.Suspend != nil && *c.Spec.Suspend {
		return "True"
	}
	return "False"
}

func completionsStr(j batchv1.Job) string {
	if j.Spec.Completions != nil {
		return intstr(int(j.Status.Succeeded), int(*j.Spec.Completions))
	}
	if j.Spec.Parallelism != nil && *j.Spec.Parallelism == 1 {
		return fmt.Sprintf("%d/1", j.Status.Succeeded)
	}
	return strnum(j.Status.Succeeded)
}

func jobDuration(j batchv1.Job) string {
	if j.Status.StartTime == nil {
		return ""
	}
	end := time.Now()
	if j.Status.CompletionTime != nil {
		end = j.Status.CompletionTime.Time
	}
	d := end.Sub(j.Status.StartTime.Time)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func describeDeployment(d *appsv1.Deployment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Name:               %s\n", d.Name)
	fmt.Fprintf(&b, "Namespace:          %s\n", d.Namespace)
	replicas := int32(0)
	if d.Spec.Replicas != nil {
		replicas = *d.Spec.Replicas
	}
	fmt.Fprintf(&b, "Replicas:           %d desired | %d updated | %d total | %d available | %d unavailable\n",
		replicas, d.Status.UpdatedReplicas, d.Status.Replicas, d.Status.AvailableReplicas, d.Status.UnavailableReplicas)
	fmt.Fprintf(&b, "Status:             %s\n", controllerStatus(d.Status.Conditions))
	fmt.Fprintf(&b, "Strategy:           %s\n", d.Spec.Strategy.Type)
	fmt.Fprintf(&b, "Age:                %s\n", tools.AgeStr(d.CreationTimestamp))
	if len(d.Spec.Template.Spec.Containers) > 0 {
		b.WriteString("\nContainers:\n")
		for _, c := range d.Spec.Template.Spec.Containers {
			fmt.Fprintf(&b, "  %s: %s\n", c.Name, c.Image)
		}
	}
	if len(d.Status.Conditions) > 0 {
		b.WriteString("\nConditions:\n")
		ct := rpc.NewTable("TYPE", "STATUS", "REASON", "MESSAGE")
		for _, c := range d.Status.Conditions {
			ct.AddRow(string(c.Type), string(c.Status), c.Reason, truncateMsg(c.Message))
		}
		b.WriteString(ct.Render())
	}
	return b.String()
}

var _ metav1.Time // keep import if temporarily unused

func truncateMsg(s string) string {
	const maxLen = 80
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "…"
}
