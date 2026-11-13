# Local dev convenience for macOS/Linux. CI does not use this Makefile;
# `make check` mirrors what its Ubuntu job runs.

.DEFAULT_GOAL := help
.PHONY: help core test-core ui-install dev test check check-remote package fmt clean

help: ## List available targets
	@echo "Targets:"
	@echo "  core        Build bin/harness-core"
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
