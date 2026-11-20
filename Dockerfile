ARG BASE_IMAGE_REGISTRY=docker.io
ARG GO_IMAGE=${BASE_IMAGE_REGISTRY}/library/golang:1.26-alpine
ARG RUNTIME_IMAGE=${BASE_IMAGE_REGISTRY}/library/alpine:3.22
ARG KUBERNETES_MCP_IMAGE=quay.io/containers/kubernetes_mcp_server:v0.1.2@sha256:c289505b06079d7f265a6fef977ce1abbfbbb4271772db17db5b58edbf44c255

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build-base

ARG TARGETOS=linux
ARG TARGETARCH
ARG GOPROXY=https://proxy.golang.org,direct

ENV GOPROXY=${GOPROXY}

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY internal ./internal
COPY pkg ./pkg

FROM build-base AS build
COPY cmd/main.go ./cmd/main.go
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/k8s-ctl ./cmd

# Independent of source changes: BuildKit can prepare this layer alongside Go.
FROM ${RUNTIME_IMAGE} AS runtime-base

# Existing Kubernetes/Helm operations invoke fixed argv, never a shell command.
RUN apk add --no-cache ca-certificates kubectl helm \
    && addgroup -S -g 10001 app && adduser -S -u 10001 -G app app \
    && mkdir -p /data /app/configs && chown app:app /data
WORKDIR /app

FROM ${KUBERNETES_MCP_IMAGE} AS kubernetes-mcp

FROM runtime-base AS runtime

COPY --from=build /out/k8s-ctl /app/k8s-ctl
COPY --from=kubernetes-mcp /app/kubernetes-mcp-server /usr/local/bin/kubernetes-mcp-server
COPY configs/config.yaml /app/configs/config.yaml
USER 10001:10001
EXPOSE 8901
ENTRYPOINT ["/app/k8s-ctl"]
