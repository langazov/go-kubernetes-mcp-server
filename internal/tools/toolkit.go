// Package tools is the shared foundation for all tool handlers. It holds the
// dependency bundle (Toolkit), common argument types, and a generic Wrap helper
// that adds auditing, request deadlines, panic recovery, and output truncation
// around every tool handler.
package tools

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/langazov/go-kubernetes-mcp-server/internal/kube"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
)

// Toolkit is the dependency bundle shared by all tool handlers.
type Toolkit struct {
	Clients *kube.Clients
	Policy  *security.Policy
	Cfg     *config.Config
	Audit   *audit.Logger
	Log     *slog.Logger

	// ShutdownCtx is cancelled when the server is stopping. Long-lived
	// background work (debug-pod cleanup, port-forward teardown) must derive
	// from it so it does not outlive the server.
	ShutdownCtx context.Context

	// sem caps the number of concurrently in-flight tool calls (DoS / fan-out
	// bound). nil means unlimited.
	sem chan struct{}
}

// InitSemaphore creates the in-flight call cap. Called once during server Build.
func (tk *Toolkit) InitSemaphore(n int) {
	if n > 0 {
		tk.sem = make(chan struct{}, n)
	}
}

// toolFunc is the signature our handlers implement. Dependencies arrive via the
// Toolkit (captured in a closure by the caller) and parsed arguments arrive via in.
type toolFunc[In any] func(ctx context.Context, in In) (*mcp.CallToolResult, error)

// ToolFunc is the exported alias for tool handlers.
type ToolFunc[In any] = toolFunc[In]

// Wrap adapts a toolFunc into the MCP ToolHandlerFor signature. It provides:
//   - a request-scoped audit record (Begin/Finish),
//   - a context deadline from --default-timeout,
//   - panic recovery (never let a panic escape the handler),
//   - output truncation to --max-output-bytes.
//
// Errors returned by f are surfaced as tool errors (isError=true) so agents can
// self-correct, rather than as MCP protocol errors.
func Wrap[In any](tk *Toolkit, name string, verb security.Verb, f toolFunc[In]) mcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (result *mcp.CallToolResult, output any, err error) {
		ctx, finish := tk.Audit.Begin(ctx, name, verb)
		ctx, cancel := context.WithTimeout(ctx, tk.Cfg.DefaultTimeout)
		defer cancel()

		if tk.sem != nil {
			select {
			case tk.sem <- struct{}{}:
				defer func() { <-tk.sem }()
			case <-ctx.Done():
				finish(ctx.Err())
				return rpc.ErrorResult("server busy, try again: %v", ctx.Err()), nil, nil
			}
		}

		defer func() {
			if r := recover(); r != nil {
				tk.Log.Error("panic in tool "+name,
					"panic", fmt.Sprint(r), "stack", string(debug.Stack()))
				err = nil
				result = rpc.ErrorResult("internal error in %s: %v", name, r)
				finish(fmt.Errorf("panic: %v", r))
			}
		}()

		res, callErr := f(ctx, in)
		finish(callErr)
		if callErr != nil {
			return rpc.ErrorResult("%v", callErr), nil, nil
		}
		truncate(res, tk.Cfg.MaxOutputBytes)
		return res, nil, nil
	}
}

// truncate caps the text of every TextContent in a result.
func truncate(res *mcp.CallToolResult, maxBytes int) {
	if res == nil {
		return
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			tc.Text = rpc.TruncateText(tc.Text, maxBytes)
		}
	}
}

// ToolDesc is a pending tool registration: the MCP tool metadata plus a flag
// indicating whether registration is gated behind a policy requirement.
type ToolDesc struct {
	Required security.Verb // VerbRead for always-on; higher verbs gate registration
}

// IsGated reports whether the tool requires elevated policy to be registered.
func (d ToolDesc) IsGated(p *security.Policy) bool {
	switch d.Required {
	case security.VerbDestructive:
		return !p.CanDestroy()
	case security.VerbWrite, security.VerbDebug:
		// Write and debug tools are gated unless their flag is set.
		if d.Required == security.VerbDebug {
			return !p.CanDebug()
		}
		return !p.CanMutate()
	default:
		return false
	}
}
