SHELL := /bin/zsh

.PHONY: help dev dev-api dev-web build build-web test test-web install-web

help:
	@echo "Available targets:"
	@echo "  make dev         Start API and web dev servers together"
	@echo "  make dev-api     Start the Go API"
	@echo "  make dev-web     Start the Next.js web app"
	@echo "  make build       Build the Go API"
	@echo "  make build-web   Build the Next.js web app"
	@echo "  make test        Run Go tests"
	@echo "  make test-web    Run web type checks"
	@echo "  make install-web Install web dependencies with bun"

dev:
	@bash -lc 'set -euo pipefail; trap "kill 0" EXIT INT TERM; \
		( $(MAKE) dev-api 2>&1 | sed "s/^/[api] /" ) & \
		( $(MAKE) dev-web 2>&1 | sed "s/^/[web] /" ) & \
		wait'

dev-api:
	go run ./cmd/wiki-server

dev-web:
	@if [ ! -d web/node_modules ]; then \
		echo "[web] dependencies missing, running bun install"; \
		cd web && bun install; \
	fi
	cd web && bun run dev

build:
	go build ./cmd/wiki-server

build-web:
	cd web && bun run build

test:
	go test ./...

test-web:
	cd web && bun run check

install-web:
	cd web && bun install
