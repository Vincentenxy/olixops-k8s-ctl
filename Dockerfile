ARG BASE_IMAGE_REGISTRY=docker.io
ARG GO_IMAGE=${BASE_IMAGE_REGISTRY}/library/golang:1.26-alpine
ARG RUNTIME_IMAGE=${BASE_IMAGE_REGISTRY}/library/alpine:3.22

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build

ARG TARGETOS=linux
ARG TARGETARCH
ARG GOPROXY=https://proxy.golang.org,direct

ENV GOPROXY=${GOPROXY}

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/main.go ./cmd/main.go
COPY cmd/pat ./cmd/pat
COPY internal ./internal
COPY pkg ./pkg
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/ ./cmd ./cmd/pat \
    && mv /out/cmd /out/olixops-k8s-ctl \
    && mv /out/pat /out/k8sctl-pat

# Independent of source changes: BuildKit can prepare this layer alongside Go.
FROM ${RUNTIME_IMAGE} AS runtime-base

# Existing Kubernetes/Helm operations invoke fixed argv, never a shell command.
RUN apk add --no-cache ca-certificates kubectl helm \
    && addgroup -S -g 10001 app && adduser -S -u 10001 -G app app \
    && mkdir -p /data /app/configs && chown app:app /data
WORKDIR /app

FROM runtime-base AS runtime

COPY --from=build /out/olixops-k8s-ctl /app/olixops-k8s-ctl
COPY --from=build /out/k8sctl-pat /app/k8sctl-pat
# Only this credential-free default config is allowed through .dockerignore.
COPY configs/config.container.yaml /app/configs/config.yaml
USER 10001:10001
EXPOSE 8091
ENTRYPOINT ["/app/olixops-k8s-ctl"]
