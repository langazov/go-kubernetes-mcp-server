---
name: go-kubernetes-mcp-server
description: Contribute to the go-kubernetes-mcp-server Go codebase (a Kubernetes MCP server built on the official modelcontextprotocol/go-sdk and client-go). USE FOR: adding or editing MCP tools, tool handlers, tool registration, internal/tools, internal/mcpserver, internal/rpc, internal/security policy, internal/kube clients, internal/config flags, internal/audit, internal/observe, cmd/k8s-mcp-server, writing tool unit tests with testutil fakes, MCP annotations, server-side apply, GVR resolution, namespace/privileged-target guards, secret masking, dry-run defaults, stdio/http transports, CORS/OAuth metadata. Trigger keywords: k8s-mcp, MCP tool, Toolkit, ToolFunc, tools.Wrap, NewReadTool, NewWriteTool, NewDestructiveTool, Register, ResolveGVR, ResolveList, ResolveNS, RPCStatusError, ErrorResult, TextResult, apply_manifest, diagnose_pod, allow-writes, allow-destructive, allow-debug. Also use for build/vet/lint/test workflow in this repo.
---

# go-kubernetes-mcp-server

A production-ready Model Context Protocol (MCP) server in Go that exposes a
Kubernetes cluster to AI agents. Read-only by default; mutating, destructive,
and debug tools are opt-in via flags. Read **`AGENTS.md`** at the repo root too —
it is the short rulebook; this skill is the deeper "how".

## Build / verify (run all before declaring done)

```bash
go build ./...
go vet ./...
gofmt -w .
golangci-lint run ./...     # v2; config in .golangci.yml
go test ./...
go test -race -cover ./...  # optional: race + coverage
go test ./internal/tools/operations/  # one package, verbose: -v
```

All four of build / vet / gofmt / test must pass clean. **No comments in code**
unless explaining genuinely non-obvious logic (this is a hard repo convention).

## Layout (dependency direction: outer → inner; tools never imports mcpserver)

| Path | Role |
|---|---|
| `cmd/k8s-mcp-server/main.go` | cobra CLI: flags → config → clients → `mcpserver.Build` → transport |
| `internal/config/` | `Config` struct, `Defaults()`, cobra flag + `K8S_MCP_*` env binding, `CategoryEnabled`, `Validate` |
| `internal/kube/` | `Clients` container: typed `Core`, `Dynamic`, `Metrics`, `Discovery` (factory builds from rest.Config) |
| `internal/rpc/` | shared arg structs + validators (`ValidateName`, `ValidateNamespace`, `IsPrivilegedNamespace`), result builders (`TextResult`, `ErrorResult`, `JSONResult`, `TruncateText`), `Table` renderer |
| `internal/security/` | `Policy` engine: `CanMutate/CanDestroy/CanDebug`, `Check*` guards, `Verb` (Read/Write/Destructive/Debug) |
| `internal/audit/` | per-call audit middleware; `Attach(ctx, kind, ns, name, dryRun)` decorates the in-flight record |
| `internal/observe/` | slog (stderr only) + optional OTel tracing |
| `internal/mcpserver/` | `Build` assembles server; `register` gates categories by policy; stdio + streamable-HTTP transports, CORS, OAuth metadata |
| `internal/tools/` | **the tools** — shared `Toolkit`, `Wrap`, builders, `ResolveGVR/ResolveList/ResolveNS`, `RPCStatusError`; subpackages per category |
| `internal/tools/testutil/` | fake-client toolkit builder for `*_test.go` |

Tool categories (`internal/tools/<cat>/`): `core`, `workloads`, `troubleshoot`,
`network`, `configstore`, `operations` (mutate/destructive), `debug`.

## The Toolkit + closure pattern (central to this codebase)

Every handler is a **`func nameTool(tk *tools.Toolkit) tools.ToolFunc[Args]`**
closure that captures `tk` and returns the actual `toolFunc`. **Do not** use
generic methods on a receiver — Go forbids parameterized methods, so the closure
form is mandatory and is the universal convention here.

```go
func myTool(tk *tools.Toolkit) tools.ToolFunc[myArgs] {
    return func(ctx context.Context, a myArgs) (*mcp.CallToolResult, error) {
        // 1. (mutating/destructive/debug only) policy check FIRST
        if err := tk.Policy.CheckMutating(); err != nil {
            return rpc.ErrorResult("%v", err), nil
        }
        // 2. validate args
        if err := rpc.ValidateName(a.Name); err != nil {
            return rpc.ErrorResult("%v", err), nil
        }
        // 3. resolve namespace + ns allowlist
        ns := tools.ResolveNS(a.Namespace)
        if err := tk.Policy.CheckNamespace(ns); err != nil {
            return rpc.ErrorResult("%v", err), nil
        }
        // 4. decorate audit record
        audit.Attach(ctx, "Pod", ns, a.Name, false)
        // 5. K8s call
        p, err := tk.Clients.Core.CoreV1().Pods(ns).Get(ctx, a.Name, metav1.GetOptions{})
        if err != nil {
            return nil, tools.RPCStatusError(err, "get pod "+ns+"/"+a.Name)
        }
        // 6. build text result
        return rpc.TextResult(describe(p)), nil
    }
}
```

Key types/signatures (memorize these):
- `tools.ToolFunc[In]` = `func(ctx context.Context, in In) (*mcp.CallToolResult, error)`
- `tools.Toolkit{Clients, Policy, Cfg, Audit, Log}`
- `tools.Wrap(tk, name, verb, fn)` → adapts to the SDK's handler signature and adds audit + timeout + panic recovery + truncation automatically
- `tools.NewReadTool(name, desc)` / `NewWriteTool` / `NewDestructiveTool` set correct MCP annotations
- `mcp.AddTool(s, tool, handler)` registers one tool

## Adding a tool (step by step)

1. **Handler**: in the relevant `internal/tools/<cat>/` package, write
   `func nameTool(tk *tools.Toolkit) tools.ToolFunc[Args]`. Define a local
   `Args` struct with `json:"..."` + `jsonschema:"..."` tags (the schema text is
   what agents see — write it clearly).
2. **Register**: add to the package's `Register(tk, s) int` via
   `mcp.AddTool(s, tools.NewXxxTool("name", "desc"), tools.Wrap(tk, "name", verb, nameTool(tk)))`
   and increment the returned count.
3. **Annotations/verb**: read → `NewReadTool` + `security.VerbRead`; mutate →
   `NewWriteTool` + `VerbWrite`; destructive → `NewDestructiveTool` +
   `VerbDestructive`; debug → `NewWriteTool` + `VerbDebug` (debug reuses the
   write annotation but a debug verb).
4. **Gate at registration**: `mcpserver.register` already wraps
   `operations.Register` in `policy.CanMutate()` and `debug.Register` in
   `policy.CanDebug()`; destructive sub-tools are gated inside
   `operations.Register` with `if tk.Policy.CanDestroy()`. Keep this layering.
5. **Defense in depth at call time**: the first line of a mutating/destructive/
   debug handler must call the matching `tk.Policy.Check*` (belt-and-suspenders
   against accidental flag flips).
6. **Errors**: expected/application errors → `return rpc.ErrorResult("...", ...), nil`
   (visible to agent, `isError=true`, lets it self-correct). K8s API errors →
   `return nil, tools.RPCStatusError(err, "action")` (maps NotFound/Forbidden/etc
   to actionable guidance; `Wrap` turns the returned error into a tool error).
7. **Tests**: use `testutil.NewToolkit(t, opts...)` + `testutil.TextOf(res)` +
   `testutil.IsError(res)`. See "Testing" below.

## Resolve helpers (reuse, don't reinvent)

- `tools.ResolveNS(ns)` → "" becomes `"default"`.
- `tools.ResolveList(a ListArgs)` → `(ns, metav1.ListOptions, error)`; honors
  `AllNamespaces` (ns=""), selector sanity-check, `Limit`. Use for every list tool.
- `tools.ResolveGVR(ctx, tk, kind, apiVersion)` → `(schema.GroupVersionResource,
  namespaced, error)` via discovery; works for CRDs. Use for dynamic-client tools
  (apply/patch/delete/label/describe). Pass empty apiVersion to auto-detect.
- `tools.AgeStr(metav1.Time)` → kubectl-style age.

## Security invariants (do NOT break — `security_test.go` enforces)

- **Read-only by default.** Mutating tools register only when `policy.CanMutate()`;
  destructive only when `policy.CanDestroy()`; debug only when `policy.CanDebug()`.
  Enforcement is at **registration time** (unreachable tools can't be invoked) AND
  repeated at **call time** inside each handler.
- **Privileged-target guard**: `kube-system`, `kube-public`, `kube-node-lease`, and
  cluster-scoped resources require `--allow-privileged-targets`. Use
  `tk.Policy.CheckTarget(ns, clusterScoped)` (helpers: `policyTarget` in operations).
- **Namespace allowlist**: `tk.Policy.CheckNamespace(ns)`.
- **Secrets masked** by default (`••••` + SHA-256/12 hash for change detection).
  Reveal needs `--reveal-secrets` boot flag AND per-call `reveal:true`.
  Never echo/audit/log secret payloads.
- **`apply_manifest` defaults to dry-run** (`dry_run=true`); agent must pass
  `dry_run=false` to persist. Manifest-kind allowlist via `CheckManifestKind`.
- Forbidden-image list applies to `run_debug_pod`/ephemeral (`CheckImage`).
- `--allow-destructive` implies `--allow-writes` (applied in `security.FromConfig`).

## Result formatting

- Lists → `rpc.NewTable(headers...).AddRow(...).Render()` → `rpc.TextResult(...)`.
- Detail → sectioned text (mirror `kubectl describe`).
- Truncation is automatic (`Wrap` calls `rpc.TruncateText` to `--max-output-bytes`,
  default 256 KiB) — do not truncate manually.
- Never return a bare `error` to the client as a protocol error for expected
  failures; use `rpc.ErrorResult` (tool error) so agents recover.

## Testing (`*_test.go`, same package)

```go
tk := testutil.NewToolkit(t,
    testutil.WithConfig(func(c *config.Config) { c.AllowWrites = true }),
    testutil.WithObjs(&appsv1.Deployment{...}),          // typed clientset
    testutil.WithDynamicObjs(&corev1.ConfigMap{...}),    // dynamic client
    testutil.WithMetricsObjs(...),                        // metrics client
    testutil.WithResources(...),                          // discovery lists
)
res, err := myTool(tk)(context.Background(), myArgs{...})
out := testutil.TextOf(res)
if testutil.IsError(res) { t.Fatalf(...) }
```

- Fakes: `kubefake`, `dynamicfake`, `metricsfake` are pre-wired; default
  discovery (pods/deployments/services/configmaps/nodes/...) is always included.
- `testutil.ClientsFor(tk)` → concrete fake clients for assertions / reactor setup.
- Scale subresource needs `testutil.RegisterScaleReactors(typed)` (fakes don't
  implement it). Dynamic apply/patch needs `testutil.RegisterApplyReactor(dyn, resource)`.
- Always add a test that the handler is **blocked** without its flag
  (e.g. `TestApplyManifestBlockedWithoutWriteFlag`) — this is the regression net
  for the security invariants.

## Transports & server lifecycle

- `stdio` (default): logs → **stderr** (stdout is the MCP transport, keep it clean).
- `http`: `--transport http --listen :8080 --endpoint /mcp`, plus `/healthz`,
  CORS (`--cors-origins`), OAuth Protected-Resource Metadata (`/.well-known/...`).
- `mcpserver.Build` constructs `Toolkit`, the `*mcp.Server`, registers tools, and
  logs the count + policy posture.

## Conventions checklist

- No comments unless non-obvious.
- Handlers are closures `func nameTool(tk *tools.Toolkit) tools.ToolFunc[Args]`.
- snake_case tool names (`verb_noun`), matching MCP/LLM norms.
- Logging to stderr only.
- Reuse `rpc` validators/result builders and `tools.Resolve*` helpers.
- Gate mutating/destructive/debug tools at registration AND call time.
- `go build ./... && go vet ./... && gofmt -w . && go test ./...` clean before "done".
