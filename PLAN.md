# Kubernetes MCP Server — Implementation Plan

A production-ready Model Context Protocol (MCP) server written in Go that lets AI
agents and LLM applications **fully manage** a Kubernetes cluster, **troubleshoot**
problems, and **debug** applications running on it.

---

## Table of Contents

1. [Goals & Non-Goals](#1-goals--non-goals)
2. [Technology Decisions](#2-technology-decisions)
3. [Design Principles](#3-design-principles)
4. [Architecture](#4-architecture)
5. [Project Structure](#5-project-structure)
6. [Configuration Reference](#6-configuration-reference)
7. [Security Model](#7-security-model)
8. [Tool Catalogue](#8-tool-catalogue)
9. [Output Formatting & Result Contracts](#9-output-formatting--result-contracts)
10. [Error Handling](#10-error-handling)
11. [Observability](#11-observability)
12. [Testing Strategy](#12-testing-strategy)
13. [Packaging & Deployment](#13-packaging--deployment)
14. [CI/CD](#14-cicd)
15. [Phased Delivery](#15-phased-delivery)
16. [Dependencies](#16-dependencies)
17. [Open Questions / Risks](#17-open-questions--risks)

---

## 1. Goals & Non-Goals

### Goals

- **Full lifecycle management** of Kubernetes resources (built-ins + CRDs) via MCP tools.
- **Troubleshooting** primitives: logs, events, describe, resource metrics, automated
  diagnosis of common failure modes (CrashLoopBackOff, ImagePullBackOff, OOM, node
  pressure, failed readiness/liveness, PVC pending, etc.).
- **Debugging** primitives: exec into containers, ephemeral containers, port-forward,
  throwaway debug pods.
- **Safe by default**, powerful when explicitly unlocked.
- **Production-grade**: configurable, observable, auditable, testable, deployable as a
  container or a local stdio binary.

### Non-Goals (v1)

- Not a Kubernetes dashboard or UI — this is a headless MCP tool surface.
- Not an AI agent itself — it exposes tools that agents *call*; it makes no LLM requests.
- Not a replacement for RBAC — it operates as the identity in the kubeconfig/SA it is
  given; Kubernetes RBAC still applies. We add our own blast-radius controls on top.
- Not multi-tenant SaaS — one server process targets one cluster context at a time
  (multi-context *switching* is supported, not concurrent multi-cluster fan-out).

---

## 2. Technology Decisions

| Concern | Decision | Rationale |
|---|---|---|
| MCP SDK | **`github.com/modelcontextprotocol/go-sdk/mcp`** (official) | v1.x stable, maintained by MCP org + Google, type-safe struct-based tool definitions (`mcp.AddTool` generic signature), supports latest spec. Avoid `mark3labs/mcp-go` for prod (still v0.x, breaking changes). |
| Kubernetes client | **`k8s.io/client-go`** (typed + dynamic) | Official client. Typed clients for ergonomics; dynamic client for generic describe/apply/patch/delete across CRDs. |
| Metrics | **`k8s.io/metrics`** + `k8s-client-go` metric client | For `top_pods`/`top_nodes`. Degrades gracefully if metrics-server absent. |
| CLI framework | **`spf13/cobra`** + `pflag` | Standard Go CLI; subcommands, flag parsing, help generation. |
| Config | cobra flags + env vars (no heavy dep) | Keep dependency surface small; bind env with `TF_*`-style prefix `K8S_MCP_*`. |
| Logging | **`log/slog`** (stdlib) | Structured logging, context-aware, zero external deps. |
| Tracing/metrics | **OpenTelemetry** (`go.opentelemetry.io/otel`) optional | Hook into MCP server `WithHooks`; export to OTLP. |
| Go version | **1.24+** (dev box has 1.26) | Required by recent client-go + official MCP SDK. Set `go 1.24` in go.mod for broad compat. |
| Linting | `golangci-lint` | Enforce quality gates in CI. |
| Release | `goreleaser` | Multi-arch binaries + Docker images + SBOM. |

---

## 3. Design Principles

1. **Safe by default.** The server boots in read-only mode. Mutating tools are not even
   registered unless `--allow-writes` is set; destructive tools require
   `--allow-destructive`. This is enforced at *registration* time, not at call time —
   unreachable tools cannot be invoked even by a malicious/prompt-injected client.
2. **Blast-radius controls layered above Kubernetes RBAC.** We don't trust the MCP client.
   Namespace allowlists, tool-category toggles, dry-run defaults, and privileged-target
   guards all run server-side.
3. **Defense in depth on untrusted input.** Every tool argument is parsed via typed
   structs and validated (names, namespaces, label selectors, time durations) before it
   touches the API server. No raw string interpolation into requests.
4. **No secret leakage.** Secret values are masked (`••••`) by default. Reveal requires
   both the `--reveal-secrets` boot flag **and** a per-call `reveal:true` argument. Secret
   data is never written to logs/audit.
5. **Bounded output.** Logs, describe, exec, and list results are truncated to a
   configurable byte ceiling (default 256 KiB) with explicit `…truncated (N bytes omitted)`
   markers so context windows don't explode.
6. **Everything has a timeout.** Every API-server call inherits a context deadline
   (`--default-timeout`, default 30s). Long-lived operations (exec, port-forward) take an
   explicit per-call `--timeout`.
7. **Generic where possible, typed where it helps.** Use the dynamic client for
   describe/apply/patch/delete so CRDs work out of the box; use typed clients where the
   ergonomics (e.g. logs, exec, top) justify it.
8. **Observable & auditable.** Every tool call is logged with verb/resource/namespace;
   mutating calls are flagged. Optional OTel tracing.

---

## 4. Architecture

Layered, with strict dependency direction (outer → inner):

```
┌─────────────────────────────────────────────────────────────┐
│  cmd/k8s-mcp-server  (cobra CLI: flags, transport wiring)   │
├─────────────────────────────────────────────────────────────┤
│  internal/mcpserver  (MCP server, capabilities, transports) │
│    └─ tools/* (tool definitions + handlers, per category)   │
├─────────────────────────────────────────────────────────────┤
│  internal/rpc        (arg structs, validation, formatting)  │
│  internal/security   (policy engine: writes, ns, dry-run)   │
│  internal/audit      (structured audit log middleware)      │
│  internal/observe    (slog, OTel, MCP hooks)                │
├─────────────────────────────────────────────────────────────┤
│  internal/kube       (clientset factory, context manager)   │
└─────────────────────────────────────────────────────────────┘
```

- **Tools layer** depends on `kube`, `rpc`, `security`. It does **not** import `mcpserver`.
- **`mcpserver`** assembles the MCP server, registers the chosen tool set, and selects a
  transport (stdio or streamable-HTTP).
- **`security` policy engine** is consulted inside each handler (and at registration) so
  policy is centralized, not scattered.
- **`audit`** is implemented as MCP tool-handler middleware so every call is captured
  uniformly.

### Runtime data flow (one tool call)

```
MCP client ──(JSON-RPC)──▶ mcpserver ──▶ tool handler
                                        ├─ parse typed args (rpc)
                                        ├─ policy check (security)
                                        ├─ audit log start
                                        ├─ kube call w/ ctx deadline
                                        ├─ format + truncate (rpc)
                                        ├─ audit log end
                                        └─ return CallToolResult
```

### Transports

- **stdio** (default): for local agents (Claude Desktop, Cursor, opencode, Claude Code).
  Zero network surface.
- **streamable-HTTP** (`--transport http --listen :8080 --endpoint /mcp`): for shared /
  remote deployments. Supports CORS and OAuth Protected-Resource Metadata (RFC 9728) so
  clients can discover auth requirements.

---

## 5. Project Structure

```
go-kubernetes-mcp-server/
├── cmd/
│   └── k8s-mcp-server/
│       └── main.go                  # cobra root, flag wiring, transport select, exit codes
├── internal/
│   ├── config/
│   │   ├── config.go                # Config struct, defaults, validation
│   │   ├── flags.go                 # cobra flag + env binding (K8S_MCP_*)
│   │   └── config_test.go
│   ├── kube/
│   │   ├── factory.go               # build clientset/dynamic/discovery/metrics from rest.Config
│   │   ├── context.go               # kubeconfig loading, in-cluster detect, context switching
│   │   ├── clients.go               # typed Clients container (kubernetes, dynamic, metrics, discovery)
│   │   └── factory_test.go
│   ├── mcpserver/
│   │   ├── server.go                # NewServer: implementation, capabilities, hooks
│   │   ├── register.go              # register tools by category based on config (policy-aware)
│   │   ├── transport.go             # stdio vs streamable-HTTP wiring
│   │   └── server_test.go
│   ├── tools/
│   │   ├── toolkit.go               # shared tool builder, common arg types (NamespaceArg, etc.)
│   │   ├── core/
│   │   │   ├── namespaces.go        # list/get/create/delete namespace
│   │   │   ├── nodes.go             # list/get/describe node, conditions
│   │   │   ├── events.go            # list events (field/label selectors)
│   │   │   ├── discovery.go         # get_api_resources
│   │   │   └── health.go            # cluster_health
│   │   ├── workloads/
│   │   │   ├── pods.go              # list/get pod
│   │   │   ├── deployments.go       # list/get/scale/restart/rollback
│   │   │   ├── statefulsets.go
│   │   │   ├── daemonsets.go
│   │   │   ├── replicasets.go
│   │   │   └── jobs.go              # jobs + cronjobs
│   │   ├── troubleshoot/
│   │   │   ├── logs.go              # get_logs (tail/since/previous/container/follow-poll)
│   │   │   ├── describe.go          # generic describe (dynamic client, any GVK)
│   │   │   ├── top.go               # top_pods, top_nodes
│   │   │   ├── rollout.go           # rollout_status, rollout_history
│   │   │   └── diagnose.go          # diagnose_pod, diagnose_node (heuristic engine)
│   │   ├── debug/
│   │   │   ├── exec.go              # exec_command
│   │   │   ├── ephemeral.go         # add_ephemeral_container
│   │   │   ├── portforward.go       # port_forward
│   │   │   └── debugpod.go          # run_debug_pod
│   │   ├── network/
│   │   │   ├── services.go
│   │   │   ├── endpoints.go
│   │   │   ├── ingress.go
│   │   │   ├── networkpolicy.go
│   │   │   └── connectivity.go      # DNS/service resolution probe
│   │   ├── configstore/
│   │   │   ├── configmaps.go
│   │   │   ├── secrets.go           # masked by default
│   │   │   ├── pv.go                # PV + PVC
│   │   │   └── storage.go           # storageclasses
│   │   └── operations/
│   │       ├── apply.go             # apply_manifest (SSA, dry-run default)
│   │       ├── patch.go             # patch
│   │       ├── labels.go            # label/annotate
│   │       ├── scale.go             # scale (generic)
│   │       └── nodes_ops.go         # cordon/drain/uncordon
│   ├── rpc/
│   │   ├── args.go                  # shared arg structs + validators (name, ns, selector, duration)
│   │   ├── result.go                # text/json result builders, truncation
│   │   ├── table.go                 # tabular formatter (kustomize-style columns)
│   │   └── args_test.go
│   ├── security/
│   │   ├── policy.go                # Policy struct + AllowWrite/AllowDestructive/NS allowlist
│   │   ├── guard.go                 # CheckMutating/CheckDestructive/CheckNamespace helpers
│   │   └── policy_test.go
│   ├── audit/
│   │   ├── audit.go                 # middleware: logs start/end, verb, resource, outcome
│   │   └── audit_test.go
│   └── observe/
│       ├── logger.go                # slog setup (level, format, destination)
│       └── tracing.go               # OTel hooks (optional)
├── deploy/
│   ├── Dockerfile                   # multi-stage, distroless runtime
│   ├── deployment.yaml              # k8s Deployment + ServiceAccount + RBAC examples
│   └── README.md                    # how to run in-cluster
├── .github/
│   └── workflows/
│       ├── ci.yml                   # lint, vet, test (unit + envtest)
│       └── e2e.yml                  # kind matrix, full tool catalogue
├── .golangci.yml
├── .goreleaser.yaml
├── go.mod
├── go.sum
├── PLAN.md                          # this file
├── README.md                        # (later) user docs + client config examples
└── LICENSE
```

---

## 6. Configuration Reference

All flags have an env equivalent (prefix `K8S_MCP_`, dashes → underscores).

### Cluster connection

| Flag | Env | Default | Description |
|---|---|---|---|
| `--kubeconfig` | `K8S_MCP_KUBECONFIG` | `~/.kube/config` or in-cluster | Path to kubeconfig. If empty and running in-cluster, use service account. |
| `--context` | `K8S_MCP_CONTEXT` | current-context | Named kubeconfig context to use. |
| `--cluster-name` | `K8S_MCP_CLUSTER_NAME` | context name | Friendly name surfaced in `cluster_health` output. |
| `--default-timeout` | `K8S_MCP_DEFAULT_TIMEOUT` | `30s` | Context deadline for API-server calls. |
| `--qps` / `--burst` | `K8S_MCP_QPS` / `K8S_MCP_BURST` | `50` / `100` | client-go rate limits. |

### Transport

| Flag | Env | Default | Description |
|---|---|---|---|
| `--transport` | `K8S_MCP_TRANSPORT` | `stdio` | `stdio` \| `http`. |
| `--listen` | `K8S_MCP_LISTEN` | `:8080` | HTTP listen address. |
| `--endpoint` | `K8S_MCP_ENDPOINT` | `/mcp` | HTTP MCP endpoint path. |
| `--cors-origins` | `K8S_MCP_CORS_ORIGINS` | none | Comma-separated allowed origins for HTTP transport. |

### Security / blast radius

| Flag | Env | Default | Description |
|---|---|---|---|
| `--allow-writes` | `K8S_MCP_ALLOW_WRITES` | `false` | Register mutating tools. |
| `--allow-destructive` | `K8S_MCP_ALLOW_DESTRUCTIVE` | `false` | Register destructive tools (delete, drain). Implies `--allow-writes`. |
| `--allow-debug` | `K8S_MCP_ALLOW_DEBUG` | `false` | Register exec/ephemeral/port-forward/debug-pod. |
| `--allow-privileged-targets` | `K8S_MCP_ALLOW_PRIVILEGED_TARGETS` | `false` | Permit ops targeting `kube-system` and cluster-scoped resources. |
| `--namespace` | `K8S_MCP_NAMESPACE` | all | Repeatable; namespace allowlist. Empty = all namespaces. |
| `--reveal-secrets` | `K8S_MCP_REVEAL_SECRETS` | `false` | Allow per-call secret reveal. |
| `--allowed-manifest-kinds` | `K8S_MCP_ALLOWED_MANIFEST_KINDS` | all | Restrict `apply_manifest` to specific GVKs (e.g. `Deployment.v1.apps`). |
| `--forbidden-images` | `K8S_MCP_FORBIDDEN_IMAGES` | none | Block `run_debug_pod`/ephemeral from using these images. |

### Tooling

| Flag | Env | Default | Description |
|---|---|---|---|
| `--enable-categories` | `K8S_MCP_ENABLE_CATEGORIES` | all enabled by policy | Comma list: `core,workloads,troubleshoot,debug,network,configstore,operations`. |
| `--output-format` | `K8S_MCP_OUTPUT_FORMAT` | `text` | `text` \| `json`. Default result rendering. |
| `--max-output-bytes` | `K8S_MCP_MAX_OUTPUT_BYTES` | `262144` (256 KiB) | Per-result truncation ceiling. |
| `--max-log-lines` | `K8S_MCP_MAX_LOG_LINES` | `1000` | Default `--tail` for `get_logs` when unspecified. |

### Observability

| Flag | Env | Default | Description |
|---|---|---|---|
| `--log-level` | `K8S_MCP_LOG_LEVEL` | `info` | `debug`\|`info`\|`warn`\|`error`. |
| `--log-format` | `K8S_MCP_LOG_FORMAT` | `json` | `json` \| `text`. |
| `--audit-path` | `K8S_MCP_AUDIT_PATH` | stderr | Where to write audit lines. |
| `--otel-endpoint` | `K8S_MCP_OTEL_ENDPOINT` | none | OTLP exporter URL. |

---

## 7. Security Model

### Layers (outer = least trusted)

1. **MCP client (untrusted)** — may be prompt-injected. All input is hostile.
2. **Argument validation (`rpc`)** — typed structs, regex-validated names/namespaces,
   bounded durations, sanitized selectors.
3. **Policy engine (`security`)** — boot-time gating + per-call checks.
4. **Kubernetes RBAC** — the server's identity still must be permitted by the cluster.

### Policy enforcement points

- **Registration time**: `mcpserver.register.go` only adds a tool if its required flag is
  set. A tool that isn't registered cannot be discovered or called. This is stronger than
  "registered but rejected at runtime".
- **Call time**: every mutating/destructive handler calls `security.Guard` as its first
  step (belt-and-suspenders; defends against accidental flag flips via reconfiguration).
- **Client time**: namespace allowlist is applied by constructing the K8s client with a
  namespace-scoped rest config where possible, and re-checked in handlers for cross-namespace
  calls (list across all namespaces is allowed only if no allowlist is set).

### Tool annotations (MCP)

Every tool sets MCP hints so well-behaved clients can present confirmations:

- `readOnlyHint: true` → pure reads.
- `readOnlyHint: false` + `destructiveHint: false` → mutating, non-destructive (scale, apply).
- `destructiveHint: true` → delete, drain.
- `idempotentHint` → set where applicable (apply is idempotent).
- `openWorldHint: false` → the cluster is a closed, known world.

### Secret handling

- `list_secrets` returns metadata + key names only.
- `get_secret` returns `••••` for each value unless `reveal:true` AND `--reveal-secrets`.
- A redacted hash (SHA-256, first 12 chars) of each value is always shown so an agent can
  detect *change* without seeing content.
- Secret payloads are never emitted to logs, audit, or traces (centralized redaction in
  `audit` middleware keyed on tool name).

### Dry-run

- `apply_manifest` defaults to `dry_run: true`. Agent must explicitly pass `dry_run:false`
  to actually apply.
- `patch`, `scale`, `label`, `annotate` accept a `dry_run` arg.

### Privileged-target guard

- Operations targeting `kube-system`, `kube-public`, `kube-node-lease`, or cluster-scoped
  resources (nodes, PVs, clusterroles) require `--allow-privileged-targets`.
- This stops an agent from "fixing" control-plane components by accident.

---

## 8. Tool Catalogue

Naming convention: `<verb>_<noun>` (snake_case, matching MCP/LLM norms). Each entry below
lists **args** and the **K8s API** it maps to. `*` = gated.

### 8.1 Core (read-only, default-on)

| Tool | Args | Maps to | Notes |
|---|---|---|---|
| `list_namespaces` | — | `CoreV1().Namespaces().List` | |
| `get_namespace` | `name` | `…Get` | |
| `get_api_resources` | `namespaced?` | `Discovery.ServerResources` | All GVKs incl. CRDs. |
| `cluster_health` | — | discovery + `/healthz` + node conditions | Rollup: API ok? nodes Ready? metrics-server? |
| `list_nodes` | `selector?` | `CoreV1().Nodes().List` | |
| `get_node` | `name` | `…Get` | |
| `describe_node` | `name` | Get + conditions + allocatable + taints + pods scheduled | Like `kubectl describe node`. |
| `list_events` | `namespace?`, `field_selector?`, `kind?`, `name?`, `limit?` | `CoreV1().Events(ns).List` | Defaults to last 1h, sorted by time. |

### 8.2 Workloads (read; *mutate)

| Tool | Args | Maps to |
|---|---|---|
| `list_pods` | `namespace?`, `selector?`, `field_selector?`, `all_namespaces?` | `CoreV1().Pods(ns).List` |
| `get_pod` | `namespace?`, `name` | `…Get` |
| `list_deployments` / `get_deployment` | ns, (name) | `AppsV1().Deployments` |
| `list_statefulsets` / `get_statefulset` | ns, (name) | `AppsV1().StatefulSets` |
| `list_daemonsets` / `get_daemonset` | ns, (name) | `AppsV1().DaemonSets` |
| `list_replicasets` | ns | `AppsV1().ReplicaSets` |
| `list_jobs` / `list_cronjobs` | ns | `BatchV1()` |
| `*scale` | `kind`, `namespace?`, `name`, `replicas`, `dry_run?` | dynamic `Scale` subresource |
| `*rollout_restart` | `kind`, `namespace?`, `name` | patch template annotation |
| `*rollout_undo` | `kind`, `namespace?`, `name`, `revision?` | rollback via rollout history |

### 8.3 Troubleshoot (read-only)

| Tool | Args | Maps to | Notes |
|---|---|---|---|
| `get_logs` | `namespace?`, `pod`, `container?`, `tail?` (≤ max-log-lines), `since?`, `since_time?`, `previous?`, `timestamps?`, `follow?` | `CoreV1().Pods(ns).GetLogs` | `follow` implemented as bounded poll loop returning appended chunk (MCP is request/response). |
| `describe` | `kind`, `group?`, `version?`, `namespace?`, `name` | dynamic client `Get` + related objects | Generic; works for CRDs. Builds a `kubectl describe`-style report. |
| `top_pods` | `namespace?`, `selector?`, `all_namespaces?` | metrics client | Graceful error if metrics-server absent. |
| `top_nodes` | — | metrics client | |
| `rollout_status` | `kind`, `namespace?`, `name`, `timeout?` | watch + status conditions | |
| `rollout_history` | `kind`, `namespace?`, `name` | ReplicaSets + annotations | |
| `diagnose_pod` | `namespace?`, `name` | Get pod + events + recent logs | Heuristic engine: classifies CrashLoopBackOff, ImagePullBackOff, OOMKilled, probe failures, PVC pending, scheduling (Pending/Unschedulable). Returns findings + suggested next tool calls. |
| `diagnose_node` | `name` | Get node + conditions + events | Detects MemoryPressure/DiskPressure/PIDPressure/NetworkUnavailable, NotReady, taints blocking scheduling. |

### 8.4 Debug (gated by `--allow-debug`)

| Tool | Args | Maps to | Notes |
|---|---|---|---|
| `exec_command` | `namespace?`, `pod`, `container?`, `command[]`, `timeout?` | `CoreV1().RESTClient().Post…exec` | Output capped; denies host-path-bound commands via arg inspection heuristics; non-interactive only. |
| `add_ephemeral_container` | `namespace?`, `pod`, `image`, `command[]?`, `target_container?` | `CoreV1().Pods(ns).UpdateEphemeralContainers` | kubectl-debug style. Blocked by `--forbidden-images`. |
| `port_forward` | `namespace?`, `pod_or_service`, `port` (e.g. `8080:80`), `local_port?`, `timeout?` | `net/http` + spdy portforwarder | Single port; auto-stops at timeout or client cancel; returns local address. |
| `run_debug_pod` | `image`, `node?`, `namespace?`, `command[]?`, `timeout?`, `service_account?` | create Pod + cleanup | Throwaway; auto-deleted at timeout or via follow-up `delete_pod`. Defaults `--restart=Never`. |

### 8.5 Network (read; *mutate)

| Tool | Args | Maps to |
|---|---|---|
| `list_services` / `get_service` | ns, (name) | `CoreV1().Services` |
| `get_endpoints` | ns, `name` | `CoreV1().Endpoints` (+ EndpointSlices) |
| `list_ingresses` | ns | `NetworkingV1().Ingresses` |
| `list_networkpolicies` | ns | `NetworkingV1().NetworkPolicies` |
| `check_connectivity` | `namespace?`, `service` | resolve service DNS + endpoint readiness | Returns resolved IPs + backend pod readiness; no traffic sent. |
| `*create_service` | ns, manifest-args | `CoreV1().Services().Create` |

### 8.6 Config & Storage (read; *mutate/*destructive)

| Tool | Args | Maps to |
|---|---|---|
| `list_configmaps` / `get_configmap` | ns, (name) | `CoreV1().ConfigMaps` |
| `list_secrets` | ns | metadata + keys only | No values ever. |
| `get_secret` | ns, `name`, `reveal?` | masked unless reveal+flag | Includes per-key SHA-256/12 for change detection. |
| `*create_secret` / `*update_secret` | ns, name, type, data | `…Create/Update` | Data accepted; stored securely, never echoed beyond hashes. |
| `list_pvcs` / `get_pvc` | ns, (name) | `CoreV1().PersistentVolumeClaims` |
| `list_pvs` | — | `CoreV1().PersistentVolumes` | cluster-scoped. |
| `list_storageclasses` | — | `StorageV1().StorageClasses` |

### 8.7 Operations (mutate/destructive)

| Tool | Args | Maps to | Notes |
|---|---|---|---|
| `*create_namespace` | `name`, `labels?` | `…Create` | |
| `*apply_manifest` | `manifest` (YAML/JSON, multi-doc ok), `namespace?`, `dry_run?`(default true), `field_manager?`(default `k8s-mcp`) | dynamic SSA (`ServerSideApply=true,Force=true`) | Refuses cluster-scoped/kube-system unless privileged flag. Kind allowlist applies. |
| `*patch` | `kind`, ns, name, `patch` (JSON), `type?`(strategic/merge/json) | dynamic `Patch` | |
| `*label` / `*annotate` | `kind`, ns, name, key/value pairs, `overwrite?` | dynamic patch | |
| `*delete_manifest` ⚠ | `kind`, ns, name, `cascade?`(default background) | dynamic `Delete` | destructive. |
| `*delete_pod` ⚠ | ns, name | `CoreV1().Pods().Delete` | destructive; grace period arg. |
| `*cordon_node` ⚠ | `name` | patch `unschedulable:true` | destructive (scheduling impact). |
| `*uncordon_node` | `name` | patch `unschedulable:false` | mutating. |
| `*drain_node` ⚠ | `name`, `timeout?`, `delete_emptydir_data?`, `disable_evict?` | evict loop + cordon | destructive; respects PodDisruptionBudgets. |

⚠ = destructive (`destructiveHint: true`, requires `--allow-destructive`).

---

## 9. Output Formatting & Result Contracts

- Default transport of results: `mcp.TextContent` with human-friendly text (tables, sections).
- `--output-format json` returns compact JSON (the raw object or a structured summary).
- **Truncation**: all results pass through `rpc.Truncate(text, maxOutputBytes)` which
  appends `\n…truncated (N more bytes, raise --max-output-bytes)`.
- **Tables**: `rpc.Table` renders aligned columns for list tools (NAME, READY, STATUS,
  RESTARTS, AGE for pods).
- **Describe**: sectioned text mirroring `kubectl describe` (Name/Labels/Status/Events/…).
- **Structured errors**: never a bare `error`; return
  `mcp.NewToolResultError("kubectl-like message")` with `isError=true` so the agent gets an
  actionable string. Unexpected panics are caught by `server.WithRecovery()`.

---

## 10. Error Handling

- **K8s `*errors.StatusError`**: map `Reason` (NotFound, Forbidden, Conflict,
  Invalid, Timeout, TooManyRequests, Unauthorized) to user-facing guidance
  ("Pod 'x' not found in namespace 'y'. Call list_pods to see available pods.").
- **Forbidden (403)**: surface that RBAC likely lacks permission; suggest the specific
  verb/resource and remind that the server's identity governs access.
- **Timeout**: distinguish context-deadline (raise `--default-timeout`) from API-server
  watch timeout.
- **Metrics absent**: `top_*` returns a clear "metrics-server not detected; install it to
  use top_*" message, not a stack trace.
- **Validation errors**: returned as `isError` results with the offending field, before any
  API call.
- **No panics escape**: recovery middleware + every handler returns a result, not a panic.

---

## 11. Observability

### Logging (`log/slog`)

- JSON to stderr by default (stdout is reserved for stdio MCP transport).
- Request-scoped attrs: tool name, namespace, resource kind/name, duration, outcome.

### Audit (`internal/audit`)

- One structured line per tool call: `timestamp, session_id, tool, verb(read/write/
  destructive), kind, namespace, name, dry_run, success, duration_ms, error`.
- Mutating/destructive calls flagged.
- Secret-returning tools log only that a secret was accessed, never contents.

### Tracing (optional OTel)

- MCP `WithHooks` → span per tool call; K8s client spans via `net/http` interceptor.
- Export to `--otel-endpoint` (OTLP/HTTP).

### Health

- `cluster_health` tool doubles as a liveness signal for remote deployments.
- HTTP transport exposes `/healthz` (separate from `/mcp`).

---

## 12. Testing Strategy

### Unit (`*_test.go`)

- Handlers tested with **fake clientsets** (`k8s.io/client-go/kubernetes/fake`,
  `dynamicfake`, `metricsfake`).
- `rpc` validators: table-driven for names, namespaces, selectors, durations.
- `security` policy: assert tools register/don't-register per flag combinations.
- Coverage gate: ≥ 80% on `internal/`.

### Integration (`envtest`)

- Spin a real API server + etcd via `sigs.k8s.io/controller-runtime/pkg/envtest`.
- Validate server-side-apply semantics, scale subresource, status reads, SSA conflicts.

### End-to-end (`kind` in CI)

- Matrix: Kubernetes v1.29 / v1.31.
- A real MCP client (official Go SDK client) drives the server over stdio.
- Scenarios: deploy an app → diagnose a crashing pod → read logs → fix via apply → verify
  rollout → port-forward → exec → cleanup. Assert full happy path per phase.

### Security regression tests

- Boot with defaults → enumerate tools via `tools/list` → assert **zero** write/debug/
  destructive tools present.
- Boot with `--allow-writes` only → assert no destructive tools.
- Namespace allowlist → assert cross-namespace list returns error.

---

## 13. Packaging & Deployment

### Local (stdio)

```
go build -o k8s-mcp-server ./cmd/k8s-mcp-server
./k8s-mcp-server --kubeconfig ~/.kube/config
```

### Docker (`deploy/Dockerfile`)

- Multi-stage: `golang:1.24` build → `gcr.io/distroless/static-debian12` runtime.
- Non-root user (65532:65532), `USER` set, no shell.
- Multi-arch: `linux/amd64`, `linux/arm64`.

### In-cluster (`deploy/deployment.yaml`)

- Deployment + ServiceAccount.
- Example RBAC: **read-only ClusterRole** by default; a commented-out **admin** ClusterRole
  binding for write/destructive modes.
- Run over HTTP transport behind an in-cluster Service; pair with an auth layer (mTLS /
  OAuth proxy) for remote access.

### Release (`goreleaser`)

- GitHub Releases with checksums + SBOM (Syft).
- Homebrew tap (optional).
- Container images pushed to GHCR: `ghcr.io/<owner>/k8s-mcp-server:{version}{,-latest}`.

---

## 14. CI/CD

`.github/workflows/ci.yml` (on PR):
- `actions/setup-go@v5` (1.24).
- `golangci-lint` (`.golangci.yml`: errcheck, govet, staticcheck, ineffassign, gocyclo,
  gosec, revive).
- `go test -race -coverprofile` + coverage upload.
- `envtest`-based integration tests.

`.github/workflows/e2e.yml` (on merge to main + nightly):
- Create `kind` cluster.
- Build server, run it, run client-driven e2e suite.
- Upload server logs + audit on failure.

`.github/workflows/release.yml` (on tag):
- `goreleaser` → binaries + Docker images + SBOM.

---

## 15. Phased Delivery

Each phase is independently shippable and ends with tests passing + the new tools usable
via a real MCP client.

### Phase 0 — Foundation
- go.mod, cobra CLI, config + flags, kube factory (kubeconfig + in-cluster + context),
  MCP server over stdio, security policy engine, audit middleware, slog.
- Prove pipeline with `list_namespaces` + `get_pod`.
- CI scaffold (lint/test/build).
- **Exit criteria**: `tools/list` returns the 2 tools; both work against a real cluster;
  read-only enforced.

### Phase 1 — Read-only troubleshooting (highest value)
- `get_logs`, `describe`, `list_events`, `top_pods`, `top_nodes`, `list_pods`/`get_pod`,
  `list_deployments`, `diagnose_pod`, `diagnose_node`.
- **Exit criteria**: an agent can debug a crashing pod with no write tools exposed.

### Phase 2 — Full read coverage
- Core (nodes, namespaces, discovery, health), workloads reads, network reads,
  configstore reads, `rollout_status`, `rollout_history`.
- Generic dynamic `describe` for any GVK incl. CRDs.
- **Exit criteria**: complete read-only operator experience.

### Phase 3 — Mutating operations
- `scale`, `rollout_restart`, `rollout_undo`, `create/update/delete_configmap`,
  `create_secret`, `label`, `annotate`, `patch`, `apply_manifest` (SSA, dry-run default),
  `create_namespace`.
- Wire `--allow-writes`, kind allowlist, privileged-target guard.
- **Exit criteria**: an agent can apply a manifest and scale a deployment safely.

### Phase 4 — Destructive + cluster ops
- `delete_manifest`, `delete_pod`, `cordon/uncordon/drain_node`.
- Wire `--allow-destructive`.
- **Exit criteria**: destructive ops work behind the flag; PDBs respected by drain.

### Phase 5 — Debug tooling
- `exec_command`, `add_ephemeral_container`, `port_forward`, `run_debug_pod`.
- Wire `--allow-debug`; tight timeouts + output caps.
- **Exit criteria**: agent can exec, port-forward, and attach ephemeral containers.

### Phase 6 — Production hardening & release
- HTTP transport + OAuth Protected-Resource Metadata + CORS.
- OTel tracing; multi-context runtime switching.
- `goreleaser` release pipeline, multi-arch Docker, in-cluster manifests, SBOM.
- README with client config examples (Claude Desktop, Cursor, opencode, Claude Code).
- **Exit criteria**: v1.0.0 tagged release.

---

## 16. Dependencies

```
module github.com/emilo/go-kubernetes-mcp-server  (TBD: confirm module path)

go 1.24

require (
    github.com/modelcontextprotocol/go-sdk v1.6.1   // official MCP SDK
    k8s.io/api            v0.32.x                    // types
    k8s.io/apimachinery   v0.32.x                    // runtime, dynamic, schema
    k8s.io/client-go      v0.32.x                    // clients, exec, port-forward
    k8s.io/metrics        v0.32.x                    // top
    sigs.k8s.io/controller-runtime v0.20.x           // envtest only
    github.com/spf13/cobra v1.8.x                    // CLI
    go.opentelemetry.io/otel v1.30.x                 // optional tracing
)
```

Pin client-go to a single minor across `k8s.io/*` to avoid skew. Target client-go 1.32
(supports clusters 1.29–1.32; client-go is backward-compatible ±1-2 minors).

---

## 17. Open Questions / Risks

| # | Question / Risk | Mitigation / Default |
|---|---|---|
| 1 | Module path (org owner)? | Assume `github.com/emilo/go-kubernetes-mcp-server`; confirm before tagging. |
| 2 | Follow-log semantics over request/response MCP. | Implement as bounded poll returning appended chunk; cap iterations + total bytes. Avoid true streaming. |
| 3 | Port-forward lifecycle with non-streaming clients. | Time-bounded single forward session per call; auto-teardown at timeout. |
| 4 | Exec interactivity. | v1: non-interactive only (command[] + capture output). No TTY/stream attach. |
| 5 | Prompt injection causing destructive ops. | Defense-in-depth: gated flags + dry-run defaults + privileged-target guard + destructiveHint for client confirmations + audit. |
| 6 | metrics-server not installed. | Graceful degradation; clear guidance in `top_*` and `cluster_health`. |
| 7 | Multi-cluster fan-out. | Out of scope v1; single active context, runtime switchable. |
| 8 | Large list result DoS. | Server-side `Limit` + `--max-output-bytes` truncation; paginate via `continue` token exposed in result. |

---

*Living document — update as phases land and decisions firm up.*
