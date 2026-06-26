# AGENTS.md

Guidance for AI agents (and humans) working in this repository.

## Commands

```bash
go build ./...              # build everything
go vet ./...                # vet
golangci-lint run ./...     # lint (v2; config in .golangci.yml)
gofmt -l .                  # format check (should print nothing)
go test ./...               # run unit tests
go test -race -cover ./...  # tests with race detector + coverage
go test ./internal/tools/operations/   # one package, verbose: -v
```

Before declaring a task done, run **all** of: `go build ./...`, `go vet ./...`,
`gofmt -w .`, and `go test ./...`. They must pass clean.

## Layout

- `cmd/k8s-mcp-server/` — cobra CLI entrypoint (flags → config → clients → server).
- `internal/config/` — config struct, flag+env binding, validation.
- `internal/kube/` — clientset factory (typed + dynamic + discovery + metrics).
- `internal/rpc/` — shared arg validators, result builders, truncation, tables.
- `internal/security/` — policy engine (write/destructive/debug gating, ns allowlist, privileged-target guard).
- `internal/audit/` — per-call structured audit middleware.
- `internal/observe/` — slog logging + optional OTel tracing.
- `internal/mcpserver/` — MCP server bootstrap, registration, transports (stdio/http), CORS, OAuth metadata, tracing middleware.
- `internal/tools/<category>/` — the tools: `core`, `workloads`, `troubleshoot`, `network`, `configstore`, `operations`, `debug`.
- `internal/tools/testutil/` — shared fake-client test helpers (importable from `*_test.go`).

## Adding a tool

1. Add the handler in the relevant `internal/tools/<cat>/` package as a
   `func nameTool(tk *tools.Toolkit) tools.ToolFunc[Args]` closure that captures
   `tk` (do NOT use generic methods — Go forbids them).
2. Register it in the package's `Register` (or a sub-registrar), incrementing the
   returned count. Use `tools.NewReadTool` / `NewWriteTool` / `NewDestructiveTool`
   for correct MCP annotations.
3. Gate mutating/destructive/debug tools with the matching `tk.Policy.Check*` call
   as the first step of the handler (defense in depth).
4. Call `tools.RPCStatusError(err, action)` for API errors so agents get guidance.
5. Add unit tests using `testutil.NewToolkit` + `testutil.TextOf`.

## Security invariants (do not break)

- **Read-only by default.** Mutating tools register only when
  `policy.CanMutate()`; destructive only when `policy.CanDestroy()`; debug only
  when `policy.CanDebug()`. The `mcpserver.register` function enforces this.
- The `security_test.go` regression suite asserts tools are unreachable per mode.
  Keep it green.
- Secrets are masked; reveal needs `--reveal-secrets` AND per-call `reveal:true`.
- `apply_manifest` defaults to dry-run.
- Cluster-scoped / `kube-system` targets require `--allow-privileged-targets`.

## Conventions

- No comments unless explaining non-obvious logic.
- Errors from tools return `rpc.ErrorResult(...)` (tool error, agent-visible) for
  expected failures, or `(nil, tools.RPCStatusError(...))` for API failures.
- Logging goes to **stderr** only — stdout is the stdio MCP transport.
