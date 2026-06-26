# Kubernetes MCP Server

A production-ready [Model Context Protocol](https://modelcontextprotocol.io) server
that gives AI agents and LLM applications full visibility into — and (opt-in)
control over — a Kubernetes cluster: **manage** resources, **troubleshoot**
problems, and **debug** applications. **37 read-only tools** by default, scaling
to **54** with `--allow-destructive` (plus 4 debug tools behind `--allow-debug`).

## Status: production-ready

All phases complete and verified against a live cluster (including a full
create→verify→delete lifecycle). 11/11 packages have automated unit tests; the
security regression suite asserts tools are unreachable per mode; CI (lint/vet/test),
kind e2e (multi-version), and goreleaser release pipelines are wired.

Written in Go using the official
[MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) and
[`client-go`](https://github.com/kubernetes/client-go).

## Why

Point an MCP-aware client (Claude Desktop, Cursor, opencode, Claude Code, …) at
this server and the agent can: read logs, diagnose a crashing pod, inspect
events, describe any resource, list workloads/services/secrets, and — when you
explicitly unlock it — apply manifests, scale deployments, drain nodes, exec
into containers, and port-forward.

**Safe by default.** The server boots read-only. Mutating, destructive, and
debug tools are not even registered unless you pass the corresponding flag.

## Features

- **37 read tools** across core, workloads, troubleshoot, network, and configstore, plus **17 gated tools** for mutating/destructive operations and **4 debug tools** — up to **54** with `--allow-destructive`.
  `list_pods`, `get_logs`, `describe` (any GVK incl. CRDs), `list_events`,
  `top_pods`/`top_nodes`, `rollout_status`/`rollout_history`, and an automated
  `diagnose_pod`/`diagnose_node` engine (CrashLoopBackOff, ImagePullBackOff,
  OOMKilled, probe failures, scheduling/PVC issues, node pressure).
- **Generic `describe`** over the dynamic client — works for any built-in or CRD.
- **Secrets masked** with change-detection hashes; reveal requires an explicit
  flag **and** per-call opt-in.
- **Bounded output** (configurable truncation) to protect context windows.
- **Audit logging** of every tool call (read/write/destructive classified).
- **Two transports**: `stdio` (local agents) and streamable `http` (shared).
- **Blast-radius controls**: namespace allowlists, tool-category toggles,
  privileged-target guard, dry-run defaults.

## Install

```bash
go install github.com/langazov/go-kubernetes-mcp-server/cmd/k8s-mcp-server@latest
```

or build from source:

```bash
go build -o k8s-mcp-server ./cmd/k8s-mcp-server
```

## Quick start (stdio)

```bash
./k8s-mcp-server --kubeconfig ~/.kube/config
```

Logs go to stderr; MCP traffic flows over stdin/stdout. Read-only by default.

### Unlock more power

```bash
# Read + mutating tools (scale, apply, restart, configmaps, ...)
./k8s-mcp-server --allow-writes

# + destructive tools (delete, drain, cordon)
./k8s-mcp-server --allow-destructive

# + debug tools (exec, ephemeral containers, port-forward, debug pods)
./k8s-mcp-server --allow-debug

# Restrict to specific namespaces
./k8s-mcp-server --namespace team-a --namespace team-b
```

## Client configuration

### Claude Desktop / Claude Code (`claude_desktop_config.json`)

```json
{
  "mcpServers": {
    "kubernetes": {
      "command": "/path/to/k8s-mcp-server",
      "args": ["--kubeconfig", "/Users/you/.kube/config"]
    }
  }
}
```

### opencode

```json
{
  "mcp": {
    "kubernetes": {
      "type": "local",
      "command": ["k8s-mcp-server", "--kubeconfig", "~/.kube/config"]
    }
  }
}
```

### Cursor

Add a "local" MCP server pointing at the `k8s-mcp-server` binary with the
desired flags.

### HTTP (shared / remote)

```bash
./k8s-mcp-server --transport http --listen :8080 --endpoint /mcp --cors-origins https://app.example.com
```

See [`deploy/`](deploy/) for an in-cluster Deployment with RBAC.

## Available tools

**54 tools total** (37 read-only always on; 13 mutating with `--allow-writes`; 4
destructive with `--allow-destructive`; 4 debug with `--allow-debug`). Read-only
tools are always on; mutating, destructive, and debug tools require their flag.

| Category | Flag | Tools |
|---|---|---|
| core | — (read-only) | `cluster_health`, `list_namespaces`, `get_namespace`, `get_api_resources`, `list_nodes`, `get_node`, `describe_node`, `list_events` |
| workloads | — (read-only) | `list_pods`, `get_pod`, `list_deployments`, `get_deployment`, `list_statefulsets`, `list_daemonsets`, `list_replicasets`, `list_jobs`, `list_cronjobs` |
| troubleshoot | — (read-only) | `get_logs`, `describe`, `top_pods`, `top_nodes`, `diagnose_pod`, `diagnose_node`, `rollout_status`, `rollout_history` |
| network | — (read-only) | `list_services`, `get_service`, `get_endpoints`, `list_ingresses`, `list_networkpolicies`, `check_connectivity` |
| configstore | — (read-only) | `list_configmaps`, `get_configmap`, `list_secrets`, `get_secret`, `list_pvcs`, `list_storageclasses` |
| operations | `--allow-writes` | `apply_manifest`, `patch`, `scale`, `rollout_restart`, `rollout_undo`, `label`, `annotate`, `create_namespace`, `create_configmap`, `update_configmap`, `create_secret`, `create_service`, `uncordon_node` |
| operations ⚠ | `--allow-destructive` | `delete_pod`, `delete_manifest`, `cordon_node`, `drain_node` |
| debug | `--allow-debug` | `exec_command`, `add_ephemeral_container`, `port_forward`, `run_debug_pod` |

⚠ = destructive (`destructiveHint: true`). `--allow-destructive` implies `--allow-writes`.

`apply_manifest` uses server-side apply with field manager `k8s-mcp` and
**defaults to dry-run** — pass `dry_run=false` to persist. All mutating tools
support dry-run; privileged targets (`kube-system`, cluster-scoped) require
`--allow-privileged-targets`.

## Configuration

All flags have an `K8S_MCP_*` env equivalent (dashes → underscores).

| Flag | Default | Description |
|---|---|---|
| `--kubeconfig` | in-cluster or `~/.kube/config` | kubeconfig path |
| `--context` | current | named context |
| `--transport` | `stdio` | `stdio` \| `http` |
| `--listen` / `--endpoint` | `:8080` / `/mcp` | HTTP settings |
| `--allow-writes` | `false` | register mutating tools |
| `--allow-destructive` | `false` | register destructive tools |
| `--allow-debug` | `false` | register debug tools |
| `--allow-privileged-targets` | `false` | permit `kube-system`/cluster-scoped |
| `--namespace` | all | namespace allowlist (repeatable) |
| `--reveal-secrets` | `false` | allow per-call secret reveal |
| `--oauth-authorization-servers` | none | OAuth auth-server URLs advertised at `/.well-known/oauth-protected-resource` (HTTP) |
| `--otel-endpoint` | none | OTLP exporter URL for tracing |
| `--max-output-bytes` | `262144` | per-result truncation ceiling |
| `--default-timeout` | `30s` | API-server call deadline |
| `--log-level` / `--log-format` | `info` / `json` | logging |
| `--audit-path` | stderr | audit log destination |

Run `k8s-mcp-server --help` for the full list.

## Development

```bash
go test ./...          # unit tests (config, security, rpc, core tools)
go vet ./...
```

## Status

All phases (0–5) complete: foundation, full read-only tooling, the diagnostic
engine, mutating operations (apply/scale/rollout), destructive ops
(delete/cordon/drain), and debug tooling (exec/ephemeral/port-forward/debug pod)
— verified against a live cluster, including a full create→verify→delete
lifecycle. See [`PLAN.md`](PLAN.md).

## License

MIT
