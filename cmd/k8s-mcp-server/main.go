// Command k8s-mcp-server is a Model Context Protocol server that exposes
// Kubernetes cluster management, troubleshooting, and debugging tools to MCP
// clients (Claude Desktop, Cursor, opencode, Claude Code, and any MCP client).
//
// It is safe by default: read-only. Start with --allow-writes to enable
// mutating tools, --allow-destructive for delete/drain, and --allow-debug for
// exec/port-forward/ephemeral containers.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/langazov/go-kubernetes-mcp-server/internal/kube"
	"github.com/langazov/go-kubernetes-mcp-server/internal/mcpserver"
	"github.com/langazov/go-kubernetes-mcp-server/internal/observe"
	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
)

// Build metadata, injected by goreleaser ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cmd := newRootCommand()
	if err := cmd.Execute(); err != nil {
		// Errors are already logged; exit non-zero.
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "k8s-mcp-server",
		Short: "Kubernetes MCP server",
		Long: "A Model Context Protocol server for Kubernetes: manage resources, " +
			"troubleshoot problems, and debug applications. Read-only by default.\n\n" +
			"Run with no transport flag to serve over stdio for local MCP clients.",
		SilenceUsage: true,
		RunE:         run,
	}
	config.AddFlags(root)
	return root
}

func run(cmd *cobra.Command, _ []string) error {
	cfg, err := config.FromFlags(cmd.Flags())
	if err != nil {
		return err
	}

	logger := observe.NewLogger(cfg.LogLevel, cfg.LogFormat)
	auditor := audit.NewLogger(slog.New(slog.NewJSONHandler(observe.AuditSink(cfg.AuditPath, os.Stderr), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}).WithGroup("audit")))

	logger.Info("starting kubernetes-mcp-server",
		"version", version, "commit", commit, "build_date", date,
		"transport", cfg.Transport,
		"allow_writes", cfg.AllowWrites,
		"allow_destructive", cfg.AllowDestructive,
		"allow_debug", cfg.AllowDebug,
	)

	// Connect to Kubernetes.
	restCfg, clusterName, err := kube.BuildRESTConfig(cfg)
	if err != nil {
		return fmt.Errorf("connect to cluster: %w", err)
	}
	clients, err := kube.NewClients(restCfg)
	if err != nil {
		return fmt.Errorf("build clients: %w", err)
	}
	logger.Info("connected to cluster", "name", clusterName)

	policy := security.FromConfig(cfg)

	app, err := mcpserver.Build(&cfg, clients, policy, logger, auditor, clusterName)
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}

	// Optional OTel tracing.
	tracer, tracerShutdown := observe.InitTracing(cfg.OTPLEndpoint, "k8s-mcp-server", logger)
	app.SetTracer(tracer)
	defer func() {
		if err := tracerShutdown(context.Background()); err != nil {
			logger.Warn("tracer shutdown", "error", err)
		}
	}()

	// Graceful shutdown on SIGINT / SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx); err != nil {
		logger.Error("server exited with error", "error", err)
		return err
	}
	logger.Info("server stopped")
	return nil
}
