// Package troubleshoot implements diagnostic and observability tools: logs,
// resource metrics (top), generic describe, rollout status/history, and the
// automated diagnose engine.
package troubleshoot

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

var read = security.VerbRead

// Register registers all troubleshoot tools on s and returns the number added.
func Register(tk *tools.Toolkit, s *mcp.Server) int {
	registerLogs(tk, s)
	registerTop(tk, s)
	registerDescribe(tk, s)
	registerDiagnose(tk, s)
	registerRollout(tk, s)
	return 8
}
