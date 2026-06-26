package debug

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

type execArgs struct {
	Namespace string   `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Pod       string   `json:"pod" jsonschema:"the pod name"`
	Container string   `json:"container,omitempty" jsonschema:"the container name (defaults to the first container)"`
	Command   []string `json:"command" jsonschema:"the command to execute, e.g. [\"sh\",\"-c\",\"env\"]"`
	Timeout   string   `json:"timeout,omitempty" jsonschema:"max execution time (e.g. 30s); defaults to the server --default-timeout"`
}

func execCommand(tk *tools.Toolkit) tools.ToolFunc[execArgs] {
	return func(ctx context.Context, a execArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckDebug(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Pod); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if len(a.Command) == 0 {
			return rpc.ErrorResult("command is required (a non-empty array)"), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.Policy.CheckNamespace(ns); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Pod", ns, a.Pod, false)

		// Apply an exec-specific timeout if provided (it overrides the call
		// deadline from Wrap, which is the server default-timeout).
		if a.Timeout != "" {
			var cancel context.CancelFunc
			ctx, cancel = withTimeout(ctx, a.Timeout)
			defer cancel()
		}

		req := tk.Clients.Core.CoreV1().RESTClient().Post().
			Resource("pods").
			Name(a.Pod).
			Namespace(ns).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Container: a.Container,
				Command:   a.Command,
				Stdin:     false,
				Stdout:    true,
				Stderr:    true,
				TTY:       false,
			}, scheme.ParameterCodec)

		execURL := req.URL()
		executor, err := remotecommand.NewWebSocketExecutor(tk.Clients.RESTConfig, "POST", execURL.String())
		if err != nil {
			return rpc.ErrorResult("create executor: %v", err), nil
		}

		var stdout, stderr bytes.Buffer
		if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdout: &stdout,
			Stderr: &stderr,
		}); err != nil {
			// Surface captured output alongside the error for debuggability.
			out := combinedOutput(&stdout, &stderr)
			return rpc.ErrorResult("exec failed in %s/%s: %v\n%s", ns, a.Pod, err, out), nil
		}
		return rpc.TextResult(fmt.Sprintf("# %s in %s/%s\n%s", strings.Join(a.Command, " "), ns, a.Pod, combinedOutput(&stdout, &stderr))), nil
	}
}

func combinedOutput(stdout, stderr *bytes.Buffer) string {
	var b strings.Builder
	if stdout.Len() > 0 {
		b.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[stderr]\n")
		b.WriteString(stderr.String())
	}
	if b.Len() == 0 {
		return "(no output)\n"
	}
	return b.String()
}

// withTimeout parses a duration string and derives a child context deadline.
func withTimeout(ctx context.Context, d string) (context.Context, context.CancelFunc) {
	dur, err := parseDur(d)
	if err != nil || dur <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, dur)
}

func parseDur(d string) (time.Duration, error) { return time.ParseDuration(d) }
