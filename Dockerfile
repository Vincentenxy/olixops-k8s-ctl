FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/olixops-k8s-ctl ./cmd

FROM alpine:3.22

RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/olixops-k8s-ctl /app/olixops-k8s-ctl
COPY configs/config.yaml /app/configs/config.yaml
USER app
EXPOSE 8091
ENTRYPOINT ["/app/olixops-k8s-ctl"]
