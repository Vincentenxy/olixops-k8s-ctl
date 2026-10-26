FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/olixops-k8s-ctl ./cmd

FROM alpine:3.22

# Existing Kubernetes/Helm operations invoke fixed argv, never a shell command.
RUN apk add --no-cache ca-certificates kubectl helm \
    && addgroup -S -g 10001 app && adduser -S -u 10001 -G app app \
    && mkdir -p /data && chown app:app /data
WORKDIR /app
COPY --from=build /out/olixops-k8s-ctl /app/olixops-k8s-ctl
COPY configs/config.yaml /app/configs/config.yaml
ENV CATALOG_DATABASE_PATH=/data/olixops.db
USER app
EXPOSE 8091
ENTRYPOINT ["/app/olixops-k8s-ctl"]
