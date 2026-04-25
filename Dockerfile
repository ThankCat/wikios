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
COPY internal/llm/prompts /app/internal/llm/prompts

ENV WIKIOS_CONFIG=/app/configs/config.prod.yaml
EXPOSE 8080
VOLUME ["/data/wiki-repo", "/app/.workspace"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
	CMD node -e "fetch('http://127.0.0.1:8080/healthz').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"

CMD ["/app/wiki-server"]
