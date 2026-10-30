VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE   ?= ghcr.io/webrix-ai/otlp-router

.PHONY: all ui build test lint run dev docker chart-lint sample

all: ui build

ui:
	cd web && npm ci && npm run build

build:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/otlp-router ./cmd/otlp-router

test:
	go test -race ./...

lint:
	go vet ./...
	test -z "$$(gofmt -l .)"
	cd web && npm run typecheck

# Runs the router with an in-memory store. Log in with admin / admin.
run: build
	OTLP_ROUTER_ADMIN_PASSWORD=admin ./bin/otlp-router --config hack/dev-config.yaml

# UI with hot reload on :5173, proxying /api to a router started with `make run`.
dev:
	cd web && npm run dev

sample:
	go run ./hack/send-sample -n 10 -token dev-token

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

chart-lint:
	helm lint charts/otlp-router
	helm template test charts/otlp-router --set willow.enabled=true --set willow.org=acme --set willow.token=x > /dev/null
