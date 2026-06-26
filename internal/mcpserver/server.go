// Package mcpserver assembles the MCP server: it builds the dependency Toolkit,
// creates the MCP server with the right capabilities, registers tool categories
// according to configuration and security policy, and runs the chosen transport.
package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/langazov/go-kubernetes-mcp-server/internal/kube"
	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/configstore"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/core"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/debug"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/network"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/operations"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/troubleshoot"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/workloads"
)

// Name/Version surfaced to MCP clients during initialization.
const (
	ServerName    = "kubernetes-mcp-server"
	ServerVersion = "0.1.0"
)

// App bundles the assembled server and its dependencies.
type App struct {
	Server      *mcp.Server
	Clients     *kube.Clients
	Policy      *security.Policy
	Config      *config.Config
	Log         *slog.Logger
	Audit       *audit.Logger
	ClusterName string
	toolCount   int
}

// Build constructs the server from a config + connected clients.
func Build(cfg *config.Config, clients *kube.Clients, policy *security.Policy,
	logger *slog.Logger, auditor *audit.Logger, clusterName string) (*App, error) {
	tk := &tools.Toolkit{
		Clients: clients,
		Policy:  policy,
		Cfg:     cfg,
		Audit:   auditor,
		Log:     logger,
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:       ServerName,
		Title:      "Kubernetes MCP Server",
		Version:    ServerVersion,
		WebsiteURL: "https://github.com/langazov/go-kubernetes-mcp-server",
	}, &mcp.ServerOptions{
		Logger:       logger,
		Instructions: instructions(cfg, policy, clusterName),
		Capabilities: &mcp.ServerCapabilities{
			Tools: &mcp.ToolCapabilities{ListChanged: true},
		},
	})

	app := &App{
		Server:      srv,
		Clients:     clients,
		Policy:      policy,
		Config:      cfg,
		Log:         logger,
		Audit:       auditor,
		ClusterName: clusterName,
	}

	app.toolCount = app.register(tk, srv)
	logger.Info("registered tools",
		"count", app.toolCount,
		"allow_writes", policy.CanMutate(),
		"allow_destructive", policy.CanDestroy(),
		"allow_debug", policy.CanDebug(),
		"namespace_allowlist", len(cfg.Namespaces),
	)
	return app, nil
}

func (a *App) register(tk *tools.Toolkit, s *mcp.Server) int {
	total := 0
	reg := func(category string, fn func(*tools.Toolkit, *mcp.Server) int) {
		if a.Config.CategoryEnabled(category) {
			total += fn(tk, s)
		}
	}
	reg("core", core.Register)
	reg("workloads", workloads.Register)
	reg("troubleshoot", troubleshoot.Register)
	reg("network", network.Register)
	reg("configstore", configstore.Register)

	// Mutating tools — only when --allow-writes is set.
	if a.Config.CategoryEnabled("operations") && a.Policy.CanMutate() {
		total += operations.Register(tk, s)
	}
	// Debug tools — only when --allow-debug is set.
	if a.Config.CategoryEnabled("debug") && a.Policy.CanDebug() {
		total += debug.Register(tk, s)
	}

	return total
}

// Run starts the server on the configured transport. It blocks until the context
// is cancelled or the transport errors.
func (a *App) Run(ctx context.Context) error {
	switch a.Config.Transport {
	case "http":
		return a.runHTTP(ctx)
	default:
		return a.runStdio(ctx)
	}
}

func (a *App) runStdio(ctx context.Context) error {
	a.Log.Info("starting MCP server (stdio transport)")
	return a.Server.Run(ctx, &mcp.StdioTransport{})
}

func (a *App) runHTTP(ctx context.Context) error {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return a.Server }, nil)
	mux := http.NewServeMux()
	mux.Handle(a.Config.Endpoint, handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	a.registerOAuthMetadata(mux)

	addr := a.Config.Listen
	srv := &http.Server{
		Addr:              addr,
		Handler:           withCORS(mux, a.Config.CORSOrigins),
		ReadHeaderTimeout: 10 * time.Second,
	}

	a.Log.Info("starting MCP server (http transport)", "listen", addr, "endpoint", a.Config.Endpoint)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return fmt.Errorf("http server: %w", err)
	}
}

func instructions(cfg *config.Config, p *security.Policy, cluster string) string {
	mode := "read-only"
	switch {
	case p.CanDestroy():
		mode = "read-write-destructive"
	case p.CanMutate():
		mode = "read-write"
	case p.CanDebug():
		mode = "read-debug"
	}
	ns := "all namespaces"
	if cfg.HasNamespaceAllowlist() {
		ns = fmt.Sprintf("namespaces %v", cfg.Namespaces)
	}
	return fmt.Sprintf(
		"Kubernetes MCP server connected to cluster %q (%s). "+
			"Use get_api_resources to discover kinds, list_* tools to explore, and diagnose_pod/diagnose_node to troubleshoot. "+
			"Logs, events, describe, and top tools are available. "+
			"Secrets are masked unless reveal=true is supported.",
		cluster, mode+" across "+ns)
}

// withCORS wraps a handler with permissive CORS for the given origins when any
// are configured (browser-based MCP clients). Empty origins = no CORS headers.
func withCORS(h http.Handler, origins []string) http.Handler {
	if len(origins) == 0 {
		return h
	}
	allowAll := false
	for _, o := range origins {
		if o == "*" {
			allowAll = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := allowAll
		if !allowed {
			for _, o := range origins {
				if o == origin {
					allowed = true
					break
				}
			}
		}
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			if !allowAll {
				w.Header().Set("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization, Mcp-Session-Id, Last-Event-ID")
			w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}
