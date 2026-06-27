package debug

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

type ephemeralArgs struct {
	Namespace       string   `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Pod             string   `json:"pod" jsonschema:"the pod to attach the ephemeral container to"`
	Image           string   `json:"image" jsonschema:"the debug container image (e.g. busybox, nicolaka/netshoot)"`
	Name            string   `json:"name,omitempty" jsonschema:"a name for the ephemeral container (defaults to 'debugger')"`
	TargetContainer string   `json:"target_container,omitempty" jsonschema:"the container to share a namespace with (for distroless debugging)"`
	Command         []string `json:"command,omitempty" jsonschema:"optional command to run; defaults to the image entrypoint"`
}

func addEphemeralContainer(tk *tools.Toolkit) tools.ToolFunc[ephemeralArgs] {
	return func(ctx context.Context, a ephemeralArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckDebug(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Pod); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if a.Image == "" {
			return rpc.ErrorResult("image is required"), nil
		}
		if err := tk.Policy.CheckImage(a.Image); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.CheckScope(ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Pod", ns, a.Pod, false)
		audit.AttachArgs(ctx, map[string]any{"image": a.Image, "target_container": a.TargetContainer})

		pod, err := tk.Clients.Core.CoreV1().Pods(ns).Get(ctx, a.Pod, metav1.GetOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("get pod %s/%s", ns, a.Pod))
		}

		name := a.Name
		if name == "" {
			name = "debugger"
		}
		ec := corev1.EphemeralContainer{
			EphemeralContainerCommon: corev1.EphemeralContainerCommon{
				Name:                     name,
				Image:                    a.Image,
				Command:                  a.Command,
				Stdin:                    true,
				TTY:                      true,
				TerminationMessagePolicy: corev1.TerminationMessageReadFile,
				ImagePullPolicy:          corev1.PullIfNotPresent,
			},
			TargetContainerName: a.TargetContainer,
		}
		pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers, ec)

		updated, err := tk.Clients.Core.CoreV1().Pods(ns).UpdateEphemeralContainers(ctx, a.Pod, pod, metav1.UpdateOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("attach ephemeral container to %s/%s", ns, a.Pod))
		}
		_ = updated
		return rpc.TextResult(fmt.Sprintf(
			"Attached ephemeral container %q (image %s) to pod %s/%s.\n"+
				"Use exec_command with container=%q to run commands inside it.\n"+
				"Note: ephemeral containers require Kubernetes 1.25+ and are removed when the pod is restarted.\n",
			name, a.Image, ns, a.Pod, name)), nil
	}
}

// runDebugPod implements the throwaway debug-pod tool.
type runDebugPodArgs struct {
	Image          string   `json:"image" jsonschema:"the debug container image (e.g. nicolaka/netshoot, curlimages/curl)"`
	Namespace      string   `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name           string   `json:"name,omitempty" jsonschema:"a pod name (defaults to 'debug-<random>')"`
	Node           string   `json:"node,omitempty" jsonschema:"schedule the debug pod onto this specific node"`
	Command        []string `json:"command,omitempty" jsonschema:"optional command; defaults to sleep for the duration"`
	Duration       string   `json:"duration,omitempty" jsonschema:"how long the pod lives before auto-deletion (e.g. 600s; default 600s)"`
	ServiceAccount string   `json:"service_account,omitempty" jsonschema:"service account to run as (default 'default')"`
}

func runDebugPod(tk *tools.Toolkit) tools.ToolFunc[runDebugPodArgs] {
	return func(ctx context.Context, a runDebugPodArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckDebug(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if a.Image == "" {
			return rpc.ErrorResult("image is required"), nil
		}
		if err := tk.Policy.CheckImage(a.Image); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.CheckScope(ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}

		name := a.Name
		if name == "" {
			name = "debug-" + randSuffix()
		}
		sa := a.ServiceAccount
		if sa == "" {
			sa = "default"
		}
		if err := tk.Policy.CheckDebugServiceAccount(sa); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Pod", ns, name, false)
		audit.AttachArgs(ctx, map[string]any{
			"image":           a.Image,
			"service_account": sa,
			"node":            a.Node,
			"command":         strings.Join(a.Command, " "),
		})

		ttl := int64(600)
		if a.Duration != "" {
			if d, err := parseDur(a.Duration); err == nil && d > 0 {
				ttl = int64(d.Seconds())
			}
		}
		command := a.Command
		if len(command) == 0 {
			command = []string{"sleep", fmt.Sprintf("%d", ttl)}
		}

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Labels:    map[string]string{"app": "k8s-mcp-debug"},
			},
			Spec: corev1.PodSpec{
				RestartPolicy:      corev1.RestartPolicyNever,
				ServiceAccountName: sa,
				Containers: []corev1.Container{
					{
						Name:            "debug",
						Image:           a.Image,
						Command:         command,
						ImagePullPolicy: corev1.PullIfNotPresent,
					},
				},
			},
		}
		if a.Node != "" {
			pod.Spec.NodeName = a.Node
		}

		created, err := tk.Clients.Core.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("create debug pod %s/%s", ns, name))
		}

		// Background auto-cleanup after the requested duration.
		go cleanupPodAfter(tk, ns, name, ttl)

		return rpc.TextResult(fmt.Sprintf(
			"Created debug pod %s/%s (image %s) on node %q.\n"+
				"It will auto-delete in %ds. Use exec_command or get_logs to interact with it.\n",
			ns, created.Name, a.Image, nodeOrNone(a.Node), ttl)), nil
	}
}
