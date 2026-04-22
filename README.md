# wikios

LLM Wiki 微服务 V1。

运行：

```bash
cp .env.example .env
go run ./cmd/wiki-server
```

服务启动时会自动加载项目根目录下的 `.env` 和 `.env.local`。
如果当前 shell 已经设置了同名环境变量，shell 值优先，不会被 `.env` 覆盖。

默认配置挂载 `/Users/chenhao/Project/knowledge-base`。

前端工作台位于 `web/`，构建产物默认输出到 `web/dist` 并由 Gin 同域挂载。

也可以通过 `WIKIOS_CONFIG` 切换配置文件：

```bash
WIKIOS_CONFIG=configs/config.prod.yaml go run ./cmd/wiki-server
```

前端构建：

```bash
cd web
bun install
bun run build
```

主要接口：

- `GET /healthz`
- `POST /api/v1/public/answer`
- `POST /api/v1/admin/ingest`
- `POST /api/v1/admin/query`
- `POST /api/v1/admin/lint`
- `POST /api/v1/admin/reflect`
- `POST /api/v1/admin/repair/apply-low-risk`
- `POST /api/v1/admin/repair/apply-proposal`
- `POST /api/v1/admin/sync`
- `GET /api/v1/admin/tasks/:id`
