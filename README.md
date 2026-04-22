# wikios

LLM Wiki 微服务 V1。

运行：

```bash
cp .env.example .env
export DEEPSEEK_API_KEY=...
export WIKIOS_ADMIN_TOKEN=...
go run ./cmd/wiki-server
```

默认配置挂载 `/Users/chenhao/Project/knowledge-base`。

也可以通过 `WIKIOS_CONFIG` 切换配置文件：

```bash
WIKIOS_CONFIG=configs/config.prod.yaml go run ./cmd/wiki-server
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
