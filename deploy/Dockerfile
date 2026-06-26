# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.26 AS build
WORKDIR /src

# Cache deps first.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/k8s-mcp-server ./cmd/k8s-mcp-server

# ---- runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/k8s-mcp-server /usr/local/bin/k8s-mcp-server

# stdio by default; for in-cluster HTTP use --transport http --listen :8080.
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/k8s-mcp-server"]
