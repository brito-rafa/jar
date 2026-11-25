# syntax=docker/dockerfile:1
# Distroless images for the long-running services, one build target each:
#   docker build --target jar-chat -t jar-chat .   browser and terminal chat (default target)
#   docker build --target jar-mcp  -t jar-mcp  .   read-only MCP tools for agents
# make build-chat and make build-mcp select these targets.
ARG GO_VERSION=1.27.3
FROM golang:${GO_VERSION}-trixie AS builder

WORKDIR /src
ENV CGO_ENABLED=1
COPY go.mod go.sum ./
COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
RUN mkdir -p /out/home

FROM builder AS build-chat
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/jar-chat ./cmd/jar-chat

FROM builder AS build-mcp
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/jar-mcp ./cmd/jar-mcp

# DuckDB uses CGO and libstdc++; the cc image supplies those runtime libraries.
FROM gcr.io/distroless/cc-debian13:nonroot AS runtime
ENV HOME=/home/nonroot \
    LANG=C.UTF-8
WORKDIR /app
# DuckDB installs the Quack client extension beneath HOME at runtime.
COPY --from=builder --chown=65532:65532 /out/home/ /home/nonroot/
COPY LICENSE NOTICE /licenses/
USER 65532:65532

FROM runtime AS jar-mcp
COPY --from=build-mcp /out/jar-mcp /app/
# 8090: jar-mcp default port; tools are served at /mcp.
EXPOSE 8090
ENTRYPOINT ["/app/jar-mcp"]
CMD ["-host", "0.0.0.0", "-port", "8090"]

# Last stage, so it stays the default when --target is omitted.
FROM runtime AS jar-chat
COPY --from=build-chat /out/jar-chat /app/
# 8080: jar-chat browser UI.
EXPOSE 8080
ENTRYPOINT ["/app/jar-chat"]
CMD ["-web", "-host", "0.0.0.0", "-port", "8080", "-open-browser=false"]
