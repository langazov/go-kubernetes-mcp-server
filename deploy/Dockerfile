# syntax=docker/dockerfile:1

# goreleaser builds k8s-mcp-server and places the binary in the Docker build
# context; this image only packages it. For a local/standalone build, produce
# the binary first:
#   CGO_ENABLED=0 go build -trimpath -o k8s-mcp-server ./cmd/k8s-mcp-server
FROM gcr.io/distroless/static-debian12:nonroot
COPY k8s-mcp-server /usr/local/bin/k8s-mcp-server

# stdio by default; for in-cluster HTTP use --transport http --listen :8080.
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/k8s-mcp-server"]
