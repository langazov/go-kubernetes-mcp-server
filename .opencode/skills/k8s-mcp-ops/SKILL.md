---
name: k8s-mcp-ops
description: Operate, diagnose, debug, and fix applications running in a Kubernetes cluster through the go-kubernetes-mcp-server MCP tools. USE FOR: troubleshooting a crashing pod, CrashLoopBackOff, ImagePullBackOff, OOMKilled, failing probes, Pending/Unschedulable pods, PVC stuck, node NotReady / MemoryPressure / DiskPressure, bad rollout, no Service endpoints, connectivity/DNS problems; debugging apps via exec, ephemeral containers, port-forward, throwaway debug pods; fixing problems via apply_manifest, patch, scale, rollout_restart, rollout_undo, label/annotate, create/update configmap/secret/service, delete_pod/delete_manifest, cordon/drain. Trigger keywords: diagnose_pod, diagnose_node, get_logs, describe, list_events, top_pods, rollout_status, rollout_undo, apply_manifest, scale, patch, exec_command, add_ephemeral_container, port_forward, run_debug_pod, check_connectivity, CrashLoopBackOff, ImagePullBackOff, OOMKilled, Pending, FailedScheduling, kubectl-equivalent, troubleshoot cluster, fix deployment, debug container. Load this skill whenever the task is to investigate, debug, or remediate something in the connected Kubernetes cluster.
---

# Kubernetes cluster operations (via go-kubernetes-mcp-server)

A runbook for **diagnosing → debugging → fixing** live cluster problems with the
MCP tools. The server is **read-only by default**; mutating/destructive/debug
tools are only registered when the operator started it with `--allow-writes`,
`--allow-destructive`, `--allow-debug`. If a tool below is missing, that is why.

## Golden rules

1. **Investigate before you act.** Always read state (`diagnose_*`, `get_logs`,
   `describe`, `list_events`) before mutating. Never apply a fix you can't explain.
2. **Dry-run first.** `apply_manifest` defaults to `dry_run=true` — keep it for the
   first call, read the result, then pass `dry_run=false` to persist. `patch`,
   `scale`, `create_*` accept a `dry_run` arg too.
3. **Namespace defaults to `default`.** Pass `namespace` explicitly for anything
   not in `default`. For cluster-wide listing use `all_namespaces=true`.
4. **Names are required & validated** (DNS names). Empty/invalid `name` → tool error.
5. **Verify after fixing.** Re-run the diagnose tool or `rollout_status` /
   `list_pods` to confirm the fix worked before declaring done.
6. **Destructive = explicit.** `delete_pod`/`delete_manifest`/`cordon_node`/
   `drain_node` need `--allow-destructive` and are annotated destructive. Prefer
   the least-destructive fix (patch > delete; scale > delete).
7. **Secrets are masked.** `get_secret` shows `••••` + a hash unless the server
   has `--reveal-secrets` AND you pass `reveal=true`. Never echo secret values.
8. **`kube-system` / cluster-scoped = privileged.** Touching them needs
   `--allow-privileged-targets`. Don't "fix" control-plane components casually.

## Triage decision tree (start here)

```
What's the symptom?
│
├─ "Pod is crashing / not working"        → POD playbooks (A)
├─ "Pod stuck Pending / not scheduled"    → SCHEDULING playbooks (B)
├─ "Deployment won't roll out / bad push" → ROLLOUT playbooks (C)
├─ "Can't reach my Service / 503/DNS"     → NETWORK playbooks (D)
├─ "Node problems / eviction / pressure"  → NODE playbooks (E)
└─ "I need to inspect inside a container" → DEBUG playbooks (F)
```

Universal first step for any pod problem: **`diagnose_pod`**. It reads status,
conditions, events, and recent crash logs in one call and tells you the class of
failure + the next step. For nodes: **`diagnose_node`**.

---

## A. Pod failure playbooks

Always start with the automated diagnosis, then drill in.

### A0. One-shot diagnosis
- `diagnose_pod` { `namespace`, `name` } → classifies CrashLoopBackOff,
  ImagePullBackOff, OOMKilled, probe failures, PVC issues, scheduling, and shows
  tail of the failing container's logs. Read the `[CRITICAL]/[WARN]` findings.

### A1. CrashLoopBackOff (starts then exits repeatedly)
1. `diagnose_pod` (above) → identifies the failing container.
2. `get_logs` { `namespace`, `pod`, `previous`: true } → **previous** container's
   stderr is almost always the real error. Add `container` if multi-container.
3. If config-driven: `describe` the referenced ConfigMap/Secret, or `list_events`
   { `namespace`, `kind`: "Pod", `name` }.
4. **Fix** (pick one):
   - Wrong args/env → `patch` the Deployment, or `update_configmap` then
     `rollout_restart`.
   - Need config that exists → `create_configmap` / `create_secret`, then restart.
   - Bad image → `rollout_undo` (fastest) or `patch` image, then `rollout_status`.
5. Verify: `rollout_status` { `kind`: "Deployment", `namespace`, `name` } then
   `list_pods` { `namespace`, `selector`: <app labels> }.

### A2. ImagePullBackOff / ErrImagePull
1. `diagnose_pod` → confirms reason. Typical: wrong tag, private registry, no
   `imagePullSecrets`.
2. `describe` { `kind`: "Pod", `namespace`, `name` } to see the exact message.
3. **Fix**: `rollout_undo` to revert a bad image push, or `patch`/`apply_manifest`
   the correct image + `imagePullSecrets`. For a new pull secret:
   `create_secret` { `type`: "kubernetes.io/dockerconfigjson", `string_data`: {...} }
   then patch the ServiceAccount / Pod spec.
4. `delete_pod` { `namespace`, `name` } (destructive) to force immediate re-pull,
   or just `rollout_restart` and wait.

### A3. OOMKilled (exit 137)
1. `diagnose_pod` flags it; confirmed via `describe` (lastState.terminated.reason).
2. **Fix**: `patch`/`apply_manifest` to raise `resources.limits.memory` (and
   usually `requests.memory`), then `rollout_status`. If it's a leak, gather
   evidence with `exec_command` (`cat /proc/1/status`, etc.) before resizing.

### A4. Probe failures / Running but not Ready
1. `diagnose_pod` ("Pod is Running but not Ready") + `list_events`
   { `namespace`, `kind`: "Pod", `name` } → look for reason `Unhealthy`.
2. `describe` { `kind`: "Pod", ... } to read the probe spec + last probe message.
3. `exec_command` into the pod to reproduce the probe path manually
   (e.g. `["sh","-c","wget -qO- localhost:8080/health || true"]`).
4. **Fix**: correct the probe path/port/delay via `patch`, or fix the app so the
   probe passes, then `rollout_status`.

### A5. CreateContainerConfigError
1. `diagnose_pod` says "missing ConfigMap/Secret or invalid env var".
2. `describe` the pod → see which env var/volume is missing.
3. `list_configmaps` / `list_secrets` in the namespace → confirm absence.
4. **Fix**: `create_configmap`/`create_secret`, or `patch` the reference, then
   `rollout_restart`.

---

## B. Scheduling / Pending playbooks

### B1. Pod Pending / Unschedulable / FailedScheduling
1. `diagnose_pod` → "Pending and Unschedulable" with the scheduler message
   (e.g. "Insufficient memory", "node(s) didn't match node selector").
2. `list_events` { `namespace`, `kind`: "Pod", `name` } or
   { `field_selector`: "reason=FailedScheduling" } for the full message.
3. `top_nodes` → see real capacity; `describe_node` on candidate nodes → see
   taints, allocatable, and resident pods.
4. **Fix** (cause-dependent):
   - Requests too big → `patch`/`apply_manifest` lower `resources.requests`.
   - Node selector / affinity / toleration mismatch → `patch` the spec.
   - Genuinely out of capacity → scale the node group (out of MCP scope) or
     `scale` down unrelated workloads to make room.
   - PVC pending (volume stuck) → see B2.

### B2. PVC stuck (ContainerCreating / FailedMount / Pending)
1. `diagnose_pod` lists mounted PVCs and warns on volume problems.
2. `describe` { `kind`: "PersistentVolumeClaim", `namespace`, `name` } → read
   `phase` and events. `list_pvcs` for the overview.
3. **Fix** depends on storage class / provisioner (often a storage driver or
   capacity issue outside the app) — surface the events to the user rather than
   guessing. If a stale claim blocks recreation: `delete_manifest`
   { `kind`: "PersistentVolumeClaim", ... } (destructive) only if data loss is OK.

---

## C. Rollout playbooks

### C1. Bad deployment / stuck rollout / wrong image pushed
1. `rollout_status` { `kind`: "Deployment", `namespace`, `name` } → tells you if
   it's rolled out and, if not, why (e.g. "X of Y updated", progress deadline).
2. `rollout_history` { `kind`: "Deployment", `namespace`, `name` } → list
   revisions to pick a known-good one.
3. **Fix**:
   - Revert: `rollout_undo` { `kind`: "Deployment", `namespace`, `name` }
     (previous revision) or pass `revision` for a specific one.
   - Then `rollout_status` to confirm, and `list_pods` { `selector` } to verify
     pod health.

### C2. Rollout stuck (progressing too slow / never converging)
1. `rollout_status` + `list_events` { `kind`: "Deployment", `name` }.
2. Usually a readiness probe or resource quota. `diagnose_pod` a new ReplicaSet's
   pod (section A4).
3. **Fix**: `patch` the probe/quotas, or `scale` up to relieve pressure. Last
   resort: `rollout_undo`.

### C3. Need to pick up a config change
- Mutate the config (`update_configmap`), then `rollout_restart`
  { `kind`: "Deployment", `namespace`, `name` } → confirms via `rollout_status`.

---

## D. Network / connectivity playbooks

### D1. Can't reach a Service / 503 / no endpoints
1. `check_connectivity` { `namespace`, `service`, `port`: <svcPort> } → resolves
   DNS, lists ready endpoints, and TCP-dials from the server's namespace. This is
   the single best network triage tool.
2. If "no endpoints" / empty endpoints: `get_service` → check the `selector`; then
   `list_pods` { `selector`: <same selector>, `all_namespaces`: false } → are the
   backing pods Ready? A Service only endpoints Ready pods.
3. `get_endpoints` { `namespace`, `name` } to confirm the endpoint list directly.
4. **Fix**: correct the Service `selector` via `patch`/`apply_manifest`, or fix
   the backing pods (section A), or `create_service` if it's missing.

### D2. DNS / name resolution problems
- `check_connectivity` resolves the cluster DNS name first and reports failure
  there explicitly. If DNS itself is broken, that's a cluster-system issue
  (`kube-system` coredns) — privileged target; surface to the user.

### D3. NetworkPolicy blocking traffic
- `list_networkpolicies` { `namespace` } → review ingress/egress rules. `describe`
  { `kind`: "NetworkPolicy", ... } for full detail. Fix via `patch`/`apply_manifest`.

---

## E. Node playbooks

### E1. Node NotReady / pressure / pods evicted
1. `diagnose_node` { `name` } → readiness, Memory/Disk/PID/Network pressure,
   blocking taints, recent events. One call.
2. `describe_node` { `name` } → full conditions, allocatable/capacity, taints,
   and all pods scheduled to it.
3. `list_nodes` (overview) and `top_nodes` (live usage) for cluster-wide view.
4. **Fix** (typically operational, not app code):
   - Pressure: identify the noisy tenant with `top_pods` { `all_namespaces`: true }
     + `field_selector: "spec.nodeName=<name>"`; consider `scale` down or moving
     workloads.
   - Need to remove a node from rotation: `cordon_node` (no new pods) then
     `drain_node` { `name`, `timeout`: "300s", `delete_emptydir_data`: false }
     (destructive, respects PodDisruptionBudgets). `uncordon_node` to restore.

---

## F. Debug-inside-the-cluster playbooks (need `--allow-debug`)

These reach into running workloads. Keep commands **non-interactive** (no TTY);
output is captured and truncated. Pass a `timeout` to avoid hangs.

### F1. Inspect a running container — `exec_command`
- Args: { `namespace`, `pod`, `container`(opt), `command`: [...], `timeout` }.
- Examples:
  - `["sh","-c","env | sort"]` — check env vars / config injection.
  - `["sh","-c","netstat -tlnp 2>/dev/null || ss -tlnp"]` — what's listening.
  - `["sh","-c","wget -qO- localhost:8080/healthz; echo EXIT=$?"]` — reproduce a
     readiness probe manually.
  - `["sh","-c","cat /proc/1/status | grep -i vm"]` — memory accounting for OOM.
  - `["sh","-c","ls -la /etc/config && cat /etc/config/*"]` — mounted config.
- Container defaults to the first container. If `sh` is absent (distroless), use F2.

### F2. Distroless / no-shell pod — `add_ephemeral_container`
- Attach a sidecar debugger that shares the pod's network/process namespace:
  `add_ephemeral_container` { `namespace`, `pod`, `image`: "nicolaka/netshoot",
  `target_container`: "<app>" }.
- Then `exec_command` with `container: "debugger"` (the ephemeral name, default
  `debugger`) to run tools (`curl`, `dig`, `tcpdump`, `ps`) against the app.
- Requires Kubernetes 1.25+. Removed when the pod restarts.

### F3. Reach a pod port locally — `port_forward`
- `port_forward` { `namespace`, `pod`, `port`: "8080:80", `duration`: "120s" } →
  returns the local address; tunnel auto-closes after `duration` (max 1h). Good
  for hitting an admin UI or DB from the server's host. Note: forwards to a Pod,
  not a Service — pick a specific pod.

### F4. Throwaway troubleshooting pod — `run_debug_pod`
- When there's no suitable pod to exec into (e.g. test Service DNS from inside
  the cluster, or probe a node's network):
  `run_debug_pod` { `image`: "nicolaka/netshoot", `namespace`, `node`(opt),
  `duration`: "600s", `service_account`(opt) }. Auto-deletes after the duration.
- Then `exec_command` into the returned pod name:
  `["sh","-c","nslookup my-service && curl -sv my-service:80"]`.
- Pin to a node with `node` to debug node-local networking/storage.

### F5. Network path from inside the cluster
- Spin `run_debug_pod` (netshoot/curl), then `exec_command` a sequence:
  `nslookup` → `getent hosts` → `curl -sv` the Service → `traceroute`. Pair with
  the read-side `check_connectivity` (D1) which probes from the server instead.

---

## G. Fixing — the mutate toolbox (need `--allow-writes` / `--allow-destructive`)

General discipline: **describe → dry-run → apply → verify.**

| Goal | Tool | Key args |
|---|---|---|
| Create/update any resource (built-in or CRD) | `apply_manifest` | `manifest` (YAML/JSON, multi-doc), `namespace`, `dry_run`(default true!), `field_manager`(default `k8s-mcp`) |
| Targeted field change | `patch` | `kind`, `api_version`(auto if omitted), `namespace`, `name`, `patch`(JSON), `patch_type`(strategic/merge/json), `dry_run` |
| Resize a workload | `scale` | `kind`(Deployment/StatefulSet/ReplicaSet), `namespace`, `name`, `replicas`, `dry_run` |
| Restart to pick up config | `rollout_restart` | `kind`(Deployment/StatefulSet/DaemonSet), `namespace`, `name` |
| Revert a bad push | `rollout_undo` | `kind`=Deployment, `namespace`, `name`, `revision`(opt) |
| Add labels/annotations | `label` / `annotate` | `kind`, `api_version`, `namespace`, `name`, `items`{k:v}, `overwrite` |
| New namespace | `create_namespace` | `name`, `labels`, `dry_run` |
| Config data | `create_configmap` / `update_configmap` | `namespace`, `name`, `data`, `dry_run` |
| Secret (values never echoed) | `create_secret` | `namespace`, `name`, `type`, `string_data`, `dry_run` |
| Expose pods | `create_service` | `namespace`, `name`, `selector`, `port`, `target_port`(opt), `type`(opt), `dry_run` |
| Force pod recreation | `delete_pod` ⚠ | `namespace`, `name`, `grace_period_seconds`(0=force), `dry_run` |
| Delete any resource | `delete_manifest` ⚠ | `kind`, `api_version`, `namespace`, `name`, `cascade`(background/foreground/orphan), `dry_run` |
| Remove node from scheduling | `cordon_node` ⚠ | `name` |
| Evict + cordon a node | `drain_node` ⚠ | `name`, `timeout`, `delete_emptydir_data`, `force` |
| Restore node scheduling | `uncordon_node` | `name` |

⚠ = destructive (`--allow-destructive`). `rollout_undo`/`rollout_restart` are
mutating (need `--allow-writes`). `apply_manifest` uses server-side apply with
`field_manager=k8s-mcp`, force=true; defaults to **dry-run** — pass
`dry_run=false` to persist.

### Patch snippet patterns (JSON for merge/strategic)
- Change an image:
  `patch` { `kind`: "Deployment", `name`, `patch`: '{"spec":{"template":{"spec":{"containers":[{"name":"app","image":"repo:tag"}]}}}}', `patch_type`: "strategic" }
- Bump memory limit:
  `patch` { `kind`: "Pod`(or Deployment template)`, `patch`: '{"spec":{"containers":[{"name":"app","resources":{"limits":{"memory":"512Mi"}}}]}', `patch_type`: "strategic" }
- Change a Service selector:
  `patch` { `kind`: "Service", `name`, `patch`: '{"spec":{"selector":{"app":"web-v2"}}}', `patch_type`: "merge" }

When patching arrays by name, prefer `strategic` (built-ins) or `merge` with the
full replacement; for CRDs use `merge` (strategic isn't supported on CRDs).

---

## H. Effective investigation patterns

- **Wide first, narrow second.** `list_pods`/`list_events` with
  `all_namespaces=true` or a `selector` to locate, then `diagnose_pod`/`describe`
  the specific object.
- **Events are the cluster's error log.** `list_events`
  { `field_selector`: "reason=FailedScheduling" } or
  `{ "involvedObject.kind=Pod,involvedObject.name=<pod>" }`. Events are
  namespace-scoped; for node events omit namespace.
- **`describe` works on any GVK incl. CRDs** — pass `kind` (+ optional
  `api_version`); omit `namespace` for cluster-scoped resources.
- **`get_api_resources`** to discover exact kinds/apiVersions when unsure (esp.
  for CRDs before `describe`/`patch`).
- **Log windows:** `get_logs` `since` (seconds) or `since_time` (RFC3339) to
  bound; `tail` to cap lines; `previous:true` for a crashed container; `follow`
  for a ~20s live tail (request/response, not a long stream).
- **Correlate:** `rollout_status` (where's the rollout) + `list_events` (why) +
  `diagnose_pod` (the new pod's health) together explain most rollout failures.

## I. Safety / when to stop and ask

- **Stop and confirm with the user before** any destructive op (`delete_*`,
  `drain_node`, `cordon_node` on a busy node), or any write to `kube-system` /
  cluster-scoped resources, or deleting/overwriting a ConfigMap/Secret that other
  apps may depend on. Summarize what will change and the blast radius.
- **Prefer non-destructive remediation:** `patch`/`apply_manifest` >
  `rollout_undo`/`rollout_restart` > `scale` > `delete_*`.
- **Don't guess at storage/DNS failures** — they're usually cluster-system
  issues; surface the events rather than mutating blindly.
- **Never reveal or log secret values.** Operate on secret *keys*, not contents.
- If a tool returns an error mentioning "disabled" or "requires
  --allow-*-targets/--allow-writes", the operator didn't enable that mode — tell
  the user which flag is needed rather than retrying.
