# Deploying in-cluster

The server can run inside Kubernetes over the HTTP transport so multiple remote
MCP clients (or an agent gateway) can share it.

## Quick start (read-only)

```bash
docker build -t ghcr.io/langazov/k8s-mcp-server:latest -f deploy/Dockerfile .
kubectl apply -f deploy/deployment.yaml
```

The server uses its mounted service-account token automatically (in-cluster
config). A read-only `ClusterRole` is bound by default.

Connect a client to:

```
http://k8s-mcp-server.default.svc.cluster.local:8080/mcp
```

Use `kubectl port-forward svc/k8s-mcp-server 8080:8080` for local testing.

## Enabling mutating / destructive / debug tools

1. Uncomment the **admin** `ClusterRole`/`ClusterRoleBinding` in
   `deploy/deployment.yaml`.
2. Add the corresponding flags to the container `args`:
   `--allow-writes`, `--allow-destructive`, `--allow-debug`.
3. Redeploy.

## Security recommendations

- Run read-only unless you need writes. Read-only tools cannot harm the cluster.
- Restrict scope with `--namespace` (repeatable) to limit blast radius.
- Front the HTTP endpoint with mTLS or an OAuth proxy; the server itself does
  not authenticate clients.
- Send audit output to a persistent volume via `--audit-path`.
- Keep `--reveal-secrets` off unless you have a strong, audited reason.
