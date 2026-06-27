package troubleshoot

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

const followBudget = 20 * time.Second // max wall time for follow=true polls

func registerLogs(tk *tools.Toolkit, s *mcp.Server) {
	mcp.AddTool(s, tools.NewReadTool("get_logs",
		"Fetch container logs from a pod. Supports tail lines, time/duration windows, previous (crashed) container, timestamps, multi-container selection, and a bounded follow mode."),
		tools.Wrap(tk, "get_logs", read, getLogs(tk)))
}

type logArgs struct {
	Namespace  string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Pod        string `json:"pod" jsonschema:"the pod name"`
	Container  string `json:"container,omitempty" jsonschema:"the container name (defaults to the first container in the pod)"`
	Tail       int64  `json:"tail,omitempty" jsonschema:"number of recent lines to return (defaults to --max-log-lines)"`
	Since      int64  `json:"since,omitempty" jsonschema:"show logs newer than this many seconds"`
	SinceTime  string `json:"since_time,omitempty" jsonschema:"RFC3339 timestamp; show logs at or after this time"`
	Previous   bool   `json:"previous,omitempty" jsonschema:"return logs of the previous (crashed) container instance"`
	Timestamps bool   `json:"timestamps,omitempty" jsonschema:"prefix each log line with an RFC3339 timestamp"`
	Follow     bool   `json:"follow,omitempty" jsonschema:"stream new logs for a bounded period (~20s)"`
}

func getLogs(tk *tools.Toolkit) tools.ToolFunc[logArgs] {
	return func(ctx context.Context, a logArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Pod); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.CheckScope(ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Pod", ns, a.Pod, false)

		tail := a.Tail
		if tail <= 0 {
			tail = int64(tk.Cfg.MaxLogLines)
		}

		opts := &corev1.PodLogOptions{
			Container:  a.Container,
			TailLines:  &tail,
			Previous:   a.Previous,
			Timestamps: a.Timestamps,
		}
		if a.Since > 0 {
			sec := a.Since
			opts.SinceSeconds = &sec
		}
		if a.SinceTime != "" {
			t, err := time.Parse(time.RFC3339, a.SinceTime)
			if err != nil {
				return rpc.ErrorResult("invalid since_time (use RFC3339): %v", err), nil
			}
			opts.SinceTime = &metav1.Time{Time: t}
		}

		streamCtx := ctx
		var cancel context.CancelFunc
		if a.Follow {
			streamCtx, cancel = context.WithTimeout(ctx, followBudget)
			opts.Follow = true
		}
		if cancel != nil {
			defer cancel()
		}

		req := tk.Clients.Core.CoreV1().Pods(ns).GetLogs(a.Pod, opts)
		stream, err := req.Stream(streamCtx)
		if err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("get logs for pod %s/%s", ns, a.Pod))
		}
		defer func() { _ = stream.Close() }()

		// Cap how much we buffer to protect context windows; truncation also
		// applies via Wrap, but bounding the read avoids unbounded memory.
		limit := int64(tk.Cfg.MaxOutputBytes) + 1024
		body, err := readAtMost(stream, limit)
		if err != nil && err != errHitLimit {
			return nil, fmt.Errorf("read logs for pod %s/%s: %w", ns, a.Pod, err)
		}

		text := string(body)
		if len(text) == 0 {
			text = "(no logs found)"
		}
		return rpc.TextResult(text), nil
	}
}

var errHitLimit = fmt.Errorf("read hit byte limit")

func readAtMost(r io.Reader, maxBytes int64) ([]byte, error) {
	var out []byte
	buf := make([]byte, 32*1024)
	for int64(len(out)) < maxBytes {
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
	return out, errHitLimit
}
