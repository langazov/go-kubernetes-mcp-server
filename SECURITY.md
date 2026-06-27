# Security Policy

## Supported versions

This project is pre-1.0 and ships frequently. Security fixes are applied to the
**latest** release line only; older versions are not maintained. Upgrade to the
[newest release](https://github.com/langazov/go-kubernetes-mcp-server/releases)
before reporting.

| Version | Supported          |
| ------- | ------------------ |
| 0.2.x   | :white_check_mark: |
| 0.1.x   | :x:                |
| < 0.1   | :x:                |

## Reporting a vulnerability

**Please do not open a public GitHub issue or PR for security bugs.**

Report privately via GitHub's private vulnerability reporting:

1. Go to <https://github.com/langazov/go-kubernetes-mcp-server/security/advisories/new>
   (the **Security** tab → **Report a vulnerability**).
2. Include: a clear description, reproduction steps, the affected version/commit,
   and impact. A proof-of-concept is appreciated but not required.

What to expect:

| Step | Target |
|---|---|
| Acknowledgement of your report | within **72 hours** |
| Status updates while triaging | at least every **7 days** |
| Decision (accepted / declined / needs more info) | within **30 days** |
| Coordinated fix & disclosure | after a fix is released |

- If the vulnerability is **accepted**, we'll open a private security advisory
  (and request a CVE/GHSA when warranted), prepare a fix, credit you unless you'd
  prefer to stay anonymous, and publish a release + advisory together.
- If **declined**, we'll explain why (e.g. out of scope, intended behavior, or a
  configuration the operator must opt into).

Please give us **90 days** of coordinated-disclosure runway before any public
release of details. Let us know your preferred timeline and we'll work with it.

## Scope

**In scope:** the source code of `k8s-mcp-server` in this repository — its tool
implementations, policy/security gating, transport handling (stdio/HTTP), secret
masking, and build/release artifacts published from this repo.

**Out of scope** (report upstream instead):

- Kubernetes / `client-go` / etcd / kind vulnerabilities.
- The Model Context Protocol specification or the MCP Go SDK.
- Infrastructure you deploy the server into (your cluster, ingress, proxy, CI).

### "Safe by design" is not a security boundary

The server boots **read-only** and gates mutating/destructive/debug tools behind
flags. These are **safety defaults, not authorization**. If you expose the HTTP
transport, you are responsible for authenticating and authorizing clients (mTLS,
`--auth-token`, an OAuth proxy, etc.). A server you deliberately start with
`--allow-destructive`, `--reveal-secrets`, or `--insecure-http` on an open
network is operating as configured and is generally **not** a vulnerability in
this project.

## Hardening checklist

Before considering a deployment production-ready:

- Run **read-only** unless you genuinely need writes.
- Restrict blast radius with `--namespace` and `--allowed-manifest-kinds`.
- Never enable `--reveal-secrets` without an audited reason.
- For HTTP: use `--tls-cert`/`--tls-key` and/or `--auth-token` (avoid
  `--insecure-http`); bind to loopback or a private network.
- Send `--audit-path` output to persistent storage and review it.
- Keep `--allow-privileged-targets` off (the default) to protect `kube-system`
  and cluster-scoped resources.
