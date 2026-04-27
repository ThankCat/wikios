# syntax=docker/dockerfile:1

FROM oven/bun:1.2 AS web-build
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web ./
RUN bun run build

FROM golang:1.25-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wiki-server ./cmd/wiki-server

FROM node:24-bookworm AS runtime
WORKDIR /app
RUN npm install -g @tobilu/qmd \
	&& git --version \
	&& python3 --version \
	&& qmd --version

COPY --from=go-build /out/wiki-server /app/wiki-server
COPY --from=web-build /src/web/dist /app/web/dist
COPY configs /app/configs
COPY deploy/config.prod.yaml /app/deploy/config.prod.yaml
COPY internal/llm/prompts /app/internal/llm/prompts
COPY deploy/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

ENV WIKIOS_CONFIG=/app/deploy/config.prod.yaml
EXPOSE 9025
VOLUME ["/data/wiki-repo", "/app/.workspace"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
	CMD node -e "fetch('http://127.0.0.1:9025/healthz').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/wiki-server"]
