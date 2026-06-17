SHELL := /bin/zsh
NEXT_PUBLIC_API_BASE_URL ?= http://127.0.0.1:9025
QMD_HTTP_PORT ?= 8181

.PHONY: help dev dev-api dev-web qmd-daemon build build-web test test-web install-web

help:
	@echo "Available targets:"
	@echo "  make dev         Start qmd daemon, API and web dev servers together"
	@echo "  make dev-api     Start the Go API"
	@echo "  make dev-web     Start the Next.js web app"
	@echo "  make qmd-daemon  Refresh the qmd index and run the warm mcp http server"
	@echo "  make build       Build the Go API"
	@echo "  make build-web   Build the Next.js web app"
	@echo "  make test        Run Go tests"
	@echo "  make test-web    Run web type checks"
	@echo "  make install-web Install web dependencies with bun"

dev:
	@bash -lc 'set -euo pipefail; trap "kill 0" EXIT INT TERM; \
		( $(MAKE) qmd-daemon 2>&1 | sed "s/^/[qmd] /" ) & \
		( $(MAKE) dev-api 2>&1 | sed "s/^/[api] /" ) & \
		( $(MAKE) dev-web 2>&1 | sed "s/^/[web] /" ) & \
		wait'

# Keeps the local embedding/rerank models warm so customer-chat retrieval is
# sub-second instead of paying a ~15s cold model load on every request.
qmd-daemon:
	@bash -lc 'set -euo pipefail; \
		if curl -s -o /dev/null -m 2 http://localhost:$(QMD_HTTP_PORT)/mcp; then \
			echo "qmd mcp http already running on :$(QMD_HTTP_PORT)"; \
			while curl -s -o /dev/null -m 2 http://localhost:$(QMD_HTTP_PORT)/mcp; do sleep 5; done; \
			exit 0; \
		fi; \
		echo "refreshing default index"; qmd update >/dev/null && qmd embed >/dev/null; \
		echo "starting qmd mcp --http on :$(QMD_HTTP_PORT)"; exec qmd mcp --http'

dev-api:
	go run ./cmd/wiki-server

dev-web:
	@if [ ! -d web/node_modules ]; then \
		echo "[web] dependencies missing, running bun install"; \
		cd web && bun install; \
	fi
	cd web && NEXT_PUBLIC_API_BASE_URL=$(NEXT_PUBLIC_API_BASE_URL) bun run dev

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
