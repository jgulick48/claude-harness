# Local dev convenience for macOS/Linux. CI does not use this Makefile;
# `make check` mirrors what its Ubuntu job runs.

.DEFAULT_GOAL := help
.PHONY: help core web test-core ui-install dev test check check-remote package fmt clean docker.build docker.push

help: ## List available targets
	@echo "Targets:"
	@echo "  core        Build bin/harness-core"
	@echo "  web         Build the web UI and copy it into internal/webserve/dist (embedded by core)"
	@echo "  docker.build  Build the Docker image (IMAGE, VERSION from ui/package.json)"
	@echo "  docker.push   buildx build + push for PLATFORMS (default linux/amd64,linux/arm64) on BUILDER (casp-build)"
	@echo "  test-core   Run Go tests with -race"
	@echo "  ui-install  Install Electron app dependencies (npm install in ui/)"
	@echo "  dev         Build core and start the Electron app in dev mode"
	@echo "  test        Build core, run Go tests, then UI tests"
	@echo "  check       Run what the Ubuntu CI job runs, e2e included (scripts/check.sh)"
	@echo "  check-remote  Same, on the build host (CHECK_HOST, default casp-build-01)"
	@echo "  package     Build core and create an unsigned Electron dir build"
	@echo "  fmt         Run gofmt -w on the Go sources"
	@echo "  clean       Remove bin/, ui/out and ui/dist"

core:
	mkdir -p bin
	go build -o bin/harness-core ./cmd/harness-core

web:
	cd ui && npm run build:web
	find internal/webserve/dist -mindepth 1 ! -name .keep ! -name .gitignore -delete
	cp -R ui/out/web/. internal/webserve/dist/

test-core:
	go test -race ./...

ui-install:
	cd ui && npm install

dev: core
	cd ui && npm run dev

test: core test-core
	cd ui && npm test

check:
	scripts/check.sh

check-remote:
	scripts/check-remote.sh

package: core
	cd ui && npm run package

fmt:
	gofmt -w .

clean:
	rm -rf bin ui/out ui/dist

IMAGE ?= docker.io/jgulick48/claude-harness
VERSION ?= $(shell sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' ui/package.json | head -1)
PLATFORMS ?= linux/amd64,linux/arm64
BUILDER ?= casp-build

docker.build:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

docker.push:
	docker buildx build --builder $(BUILDER) --platform $(PLATFORMS) --build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(VERSION) -t $(IMAGE):latest --push .
