// Package debug implements interactive debugging tools: exec into containers,
// attach ephemeral containers, port-forward, and run throwaway debug pods. All
// tools are gated behind --allow-debug at registration time and run with tight
// timeouts and output caps.
package debug

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

var dbg = security.VerbDebug

// Register registers all debug tools on s and returns the count added.
// It must only be called when policy.CanDebug() is true.
func Register(tk *tools.Toolkit, s *mcp.Server) int {
	n := 0

	mcp.AddTool(s, tools.NewWriteTool("exec_command",
		"Execute a command inside a container and return its combined stdout/stderr (non-interactive). Use to inspect files, run diagnostics, or curl services from inside a pod."),
		tools.Wrap(tk, "exec_command", dbg, execCommand(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("add_ephemeral_container",
		"Attach an ephemeral container (kubectl-debug style) to a running pod to troubleshoot it in place, e.g. a distroless pod with no shell."),
		tools.Wrap(tk, "add_ephemeral_container", dbg, addEphemeralContainer(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("port_forward",
		"Forward a local port to a port on a pod. Returns the local address immediately; the tunnel stays open for the requested duration (default 120s) then auto-closes."),
		tools.Wrap(tk, "port_forward", dbg, portForward(tk)))
	n++

	mcp.AddTool(s, tools.NewWriteTool("run_debug_pod",
		"Run a throwaway debug pod with a chosen image (optionally on a specific node). It is automatically deleted after the requested duration (default 600s)."),
		tools.Wrap(tk, "run_debug_pod", dbg, runDebugPod(tk)))
	n++

	return n
}
