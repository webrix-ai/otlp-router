# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:24-alpine AS ui
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/otlp-router ./cmd/otlp-router \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/webrix-ai/otlp-router" \
      org.opencontainers.image.description="Open source OTLP router for AI agents, by Willow" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/otlp-router /otlp-router
# Owned by the runtime user so a fresh named volume at /data is writable.
COPY --from=build --chown=65532:65532 /out/data /data
EXPOSE 4317 4318 8080
# Numeric, so Kubernetes can verify runAsNonRoot.
USER 65532:65532
ENTRYPOINT ["/otlp-router"]
CMD ["--config", "/etc/otlp-router/config.yaml"]
