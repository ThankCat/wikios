# wikios

LLM Wiki 微服务与独立 Web 工作台。

## 启动

```bash
cp .env.example .env
make dev
```

- API 默认启动在 `http://127.0.0.1:8080`
- Web 默认启动在 `http://127.0.0.1:3000`
- 用户页：`/chat`
- 管理员登录页：`/admin/login`

服务启动时会自动加载项目根目录下的 `.env` 和 `.env.local`。如果当前 shell 已经设置了同名环境变量，shell 值优先。

## 常用命令

```bash
make dev
make dev-api
make dev-web
make test
make test-web
make build
make build-web
```

## 当前管理员默认账号

- username: `admin`
- password: `admin123`

可通过 `configs/config.prod.yaml` 或环境变量改成自己的值。

## 主要接口

- `GET /healthz`
- `POST /api/v1/public/answer`
- `POST /api/v1/admin/auth/login`
- `POST /api/v1/admin/auth/logout`
- `GET /api/v1/admin/auth/me`
- `POST /api/v1/admin/chat`
- `POST /api/v1/admin/chat/stream`
- `POST /api/v1/admin/upload`
