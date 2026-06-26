# Installation

Choose one of the methods below, then point an MCP client at the `k8s-mcp-server`
binary. See the [client configuration](README.md#client-configuration) section for
how to wire it into Claude Desktop, Cursor, opencode, and others.

## Homebrew (macOS & Linux)

```bash
brew install --cask langazov/tap/k8s-mcp-server
```

Upgrades:

```bash
brew upgrade --cask k8s-mcp-server
```

> **macOS note:** the binary is not code-signed/notarized. On first run you may
> see "cannot be opened because it is from an unidentified developer." Allow it
> under *System Settings → Privacy & Security*, or run
> `xattr -dr com.apple.quarantine $(which k8s-mcp-server)`.

## Pre-built binaries

Download an archive for your platform from the
[GitHub Releases](https://github.com/langazov/go-kubernetes-mcp-server/releases):

| OS | Architecture | Archive |
| --- | --- | --- |
| macOS | Intel, Apple Silicon | `k8s-mcp-server_<ver>_Darwin_amd64.tar.gz`, `..._arm64.tar.gz` |
| Linux | amd64, arm64 | `k8s-mcp-server_<ver>_Linux_amd64.tar.gz`, `..._arm64.tar.gz` |
| Windows | amd64, arm64 | `k8s-mcp-server_<ver>_Windows_amd64.zip`, `..._arm64.zip` |

Extract it and put the binary on your `PATH`:

```bash
tar -xzf k8s-mcp-server_*_$(go env GOOS)_$(go env GOARCH).tar.gz
sudo mv k8s-mcp-server /usr/local/bin/
k8s-mcp-server --help
```

## Docker image

Multi-arch images (`linux/amd64`, `linux/arm64`) are published to GHCR:

```bash
docker pull ghcr.io/langazov/k8s-mcp-server:latest
```

Run it over stdio for a local MCP client (mount your kubeconfig read-only):

```bash
docker run --rm -i \
  -v ~/.kube/config:/kubeconfig:ro \
  ghcr.io/langazov/k8s-mcp-server:latest --kubeconfig /kubeconfig
```

Pinned versions are also available: `ghcr.io/langazov/k8s-mcp-server:v0.1.2`.

## Go install

Requires Go 1.26+:

```bash
go install github.com/langazov/go-kubernetes-mcp-server/cmd/k8s-mcp-server@latest
```

The binary lands in `$(go env GOPATH)/bin`.

## Build from source

```bash
git clone https://github.com/langazov/go-kubernetes-mcp-server.git
cd go-kubernetes-mcp-server
go build -o k8s-mcp-server ./cmd/k8s-mcp-server
./k8s-mcp-server --help
```

## In-cluster (HTTP transport)

For a shared, remote deployment inside Kubernetes, see
[`deploy/`](deploy/) for a Deployment + ServiceAccount + RBAC and run with
`--transport http`. A read-only `ClusterRole` is bound by default.

## Verify

```bash
k8s-mcp-server --help
k8s-mcp-server --kubeconfig ~/.kube/config   # stdio, read-only
```

Logs go to stderr; MCP traffic flows over stdin/stdout.
