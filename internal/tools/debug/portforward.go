package debug

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

const maxConcurrentForwards = 5

var (
	forwardsMu sync.Mutex
	forwards   int
)

func acquireForward() error {
	forwardsMu.Lock()
	defer forwardsMu.Unlock()
	if forwards >= maxConcurrentForwards {
		return fmt.Errorf("port-forward limit reached (%d concurrent); close an existing forward and retry", maxConcurrentForwards)
	}
	forwards++
	return nil
}

func releaseForward() {
	forwardsMu.Lock()
	defer forwardsMu.Unlock()
	if forwards > 0 {
		forwards--
	}
}

type portForwardArgs struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Pod       string `json:"pod" jsonschema:"the pod name"`
	Port      string `json:"port" jsonschema:"forward spec: 'LOCAL:REMOTE' or just 'REMOTE' (e.g. 8080:80 or 8080)"`
	Duration  string `json:"duration,omitempty" jsonschema:"how long to keep the tunnel open (e.g. 120s; default 120s, max 1h)"`
}

func portForward(tk *tools.Toolkit) tools.ToolFunc[portForwardArgs] {
	return func(ctx context.Context, a portForwardArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckDebug(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Pod); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if a.Port == "" {
			return rpc.ErrorResult("port is required, e.g. 8080:80"), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.CheckScope(ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := acquireForward(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		duration := 120 * time.Second
		if a.Duration != "" {
			if d, err := parseDur(a.Duration); err == nil && d > 0 {
				duration = d
			}
		}
		if duration > time.Hour {
			duration = time.Hour
		}
		audit.Attach(ctx, "Pod", ns, a.Pod, false)
		audit.AttachArgs(ctx, map[string]any{"port": a.Port, "duration": duration.String()})
		ports := []string{normalizePort(a.Port)}

		// Build the SPDY port-forward URL via the REST client.
		req := tk.Clients.Core.CoreV1().RESTClient().Post().
			Resource("pods").
			Namespace(ns).
			Name(a.Pod).
			SubResource("portforward")
		url := req.URL()

		transport, upgrader, err := spdy.RoundTripperFor(tk.Clients.RESTConfig)
		if err != nil {
			releaseForward()
			return rpc.ErrorResult("create transport: %v", err), nil
		}
		dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url)

		stopChan := make(chan struct{}, 1)
		readyChan := make(chan struct{}, 1)
		out := &strings.Builder{}
		pf, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, ports, stopChan, readyChan, out, io.Discard)
		if err != nil {
			releaseForward()
			return rpc.ErrorResult("create port forwarder: %v", err), nil
		}

		// Run the forwarder; once ready, return the address immediately. The
		// tunnel stays open for `duration` (or until server shutdown) in the
		// background, then closes.
		errChan := make(chan error, 1)
		go func() { errChan <- pf.ForwardPorts() }()

		select {
		case <-readyChan:
			// Forwarder is up.
		case err := <-errChan:
			releaseForward()
			return rpc.ErrorResult("port forward failed: %v\n%s", err, out.String()), nil
		case <-time.After(10 * time.Second):
			close(stopChan)
			releaseForward()
			return rpc.ErrorResult("port forward did not become ready within 10s\n%s", out.String()), nil
		}

		// Schedule teardown against the server lifetime (not the per-call ctx,
		// which is cancelled when this handler returns).
		go func() {
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-shutdownDone(tk):
			}
			close(stopChan)
			releaseForward()
		}()

		local := ports[0]
		return rpc.TextResult(fmt.Sprintf(
			"Port forward active for pod %s/%s: 127.0.0.1:%s -> pod:%s\n"+
				"The tunnel will stay open for %s, then close automatically.\n",
			ns, a.Pod, strings.SplitN(local, ":", 2)[0], strings.SplitN(local, ":", 2)[1], duration)), nil
	}
}

// shutdownDone returns a channel that closes when the server is stopping, or nil
// (never fires) when no shutdown context is wired (e.g. in unit tests).
func shutdownDone(tk *tools.Toolkit) <-chan struct{} {
	if tk.ShutdownCtx == nil {
		return nil
	}
	return tk.ShutdownCtx.Done()
}

// normalizePort ensures the spec is "local:remote".
func normalizePort(p string) string {
	if !strings.Contains(p, ":") {
		return p + ":" + p
	}
	return p
}

// randSuffix returns a short random suffix for debug-pod names.
func randSuffix() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 5)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

func nodeOrNone(n string) string {
	if n == "" {
		return "<auto>"
	}
	return n
}

// cleanupPodAfter deletes a pod after ttl seconds, best-effort. It is bound to
// the server's shutdown context so it cannot outlive the server, and re-checks
// the debug policy before deleting (so flipping --allow-debug off stops cleanup
// activity rather than continuing to issue deletes).
func cleanupPodAfter(tk *tools.Toolkit, ns, name string, ttl int64) {
	ctx := tk.ShutdownCtx
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(time.Duration(ttl+30) * time.Second) // grace beyond sleep
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return
	}
	if err := tk.Policy.CheckDebug(); err != nil {
		return
	}
	delCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_ = tk.Clients.Core.CoreV1().Pods(ns).Delete(delCtx, name, metav1.DeleteOptions{})
}
