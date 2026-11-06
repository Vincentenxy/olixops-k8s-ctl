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
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/olixops-k8s-ctl ./cmd
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/k8sctl-pat ./cmd/pat

FROM ${RUNTIME_IMAGE} AS runtime

# Existing Kubernetes/Helm operations invoke fixed argv, never a shell command.
RUN apk add --no-cache ca-certificates kubectl helm \
    && addgroup -S -g 10001 app && adduser -S -u 10001 -G app app \
    && mkdir -p /data /app/configs && chown app:app /data
WORKDIR /app
COPY --from=build /out/olixops-k8s-ctl /app/olixops-k8s-ctl
COPY --from=build /out/k8sctl-pat /app/k8sctl-pat
# Keep developer configuration and local credentials out of the runtime image.
COPY configs/config.container.yaml /app/configs/config.yaml
USER 10001:10001
EXPOSE 8091
ENTRYPOINT ["/app/olixops-k8s-ctl"]
