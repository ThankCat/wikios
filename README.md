# WikiOS

WikiOS 是一个智能 Wiki 知识库微服务，核心职责是连接外挂 LLM-Wiki 仓库、LLM、检索工具和管理后台。它内置 Web 管理后台，但不是完整的终端 AI 客服前端。

真实业务中的 AI 客服应通过 Public API 对接 WikiOS；当前 `/chat` 页面仅用于测试用户输入和公开问答返回效果。

详细接口文档见：[docs/API.md](docs/API.md)。

## 功能概览

- Public API：面向 AI 客服系统，提供普通 JSON 和 SSE 流式问答。
- Admin Web：内置管理后台，支持上传摄入、健康检查、综合分析、修复、资料库浏览、前置话术、同步。
- 外挂 Wiki：知识库以独立目录或独立 git 仓库挂载，默认生产路径为 `/data/wiki-repo`。
- LLM-Wiki 治理：Wiki 层负责记录来源、概念、实体、FAQ、链接、报告和演化日志；企业适配和客服话术由 server/profile 管理。

## 本地开发

```bash
cp .env.example .env
make dev
```

默认地址：

- API：`http://127.0.0.1:8080`
- Web：`http://127.0.0.1:3000`
- 用户测试页：`/chat`
- 管理员登录页：`/admin/login`

常用命令：

```bash
make dev
make dev-api
make dev-web
make test
make test-web
make build
make build-web
```

## Docker 部署

### 1. 准备外挂 Wiki

推荐把 Wiki 作为独立 git 仓库放在部署目录下：

```bash
git clone <your-wiki-repo> ./knowledge-base
```

最小结构建议：

```text
knowledge-base/
  AGENT.md
  USER_GUIDE.md
  raw/
  wiki/
    index.md
    log.md
    outputs/
```

生产部署采用宿主机目录挂载。容器不会内置真实 Wiki，不会启动时 clone，也不会自动改变你的 Wiki 结构。

### 2. 配置环境变量

```bash
cp .env.example .env
```

至少配置：

```env
DEEPSEEK_API_KEY=your-deepseek-api-key
WIKIOS_DEFAULT_ADMIN_USERNAME=admin
WIKIOS_DEFAULT_ADMIN_PASSWORD=change-me
WIKIOS_QMD_INDEX=zy-knowledge-base
```

可选配置：

```env
WIKIOS_LLM_ADMIN_TIMEOUT_SEC=300
WIKIOS_CONTEXT_MAX_TOKENS=1000000
WIKIOS_CONTEXT_RESERVE_TOKENS=8192
WIKIOS_CONTEXT_COUNTER=tokenizer
WIKIOS_CONTEXT_TOKENIZER=cl100k_base
WIKIOS_KNOWLEDGE_PROFILE=siyetian
WIKIOS_KNOWLEDGE_PROFILE_PATH=/app/configs/knowledge_profiles/siyetian.yaml
```

### 3. 启动

```bash
docker compose -f docker-compose.example.yml up -d --build
```

检查服务：

```bash
curl http://127.0.0.1:8080/healthz
```

访问管理后台：

```text
http://127.0.0.1:8080/admin/login
```

### 4. qmd 初始化

首次部署时，如果外挂 Wiki 尚未建立 qmd collection，可执行：

```bash
docker compose -f docker-compose.example.yml exec wikios \
  sh -lc 'qmd --index "$WIKIOS_QMD_INDEX" collection add wiki/ --name wiki'
```

后续更新可由管理后台操作触发，也可以手动执行：

```bash
docker compose -f docker-compose.example.yml exec wikios \
  sh -lc 'qmd --index "$WIKIOS_QMD_INDEX" update'
```

## 目录挂载说明

`docker-compose.example.yml` 默认挂载：

| 宿主机路径 | 容器路径 | 用途 |
| --- | --- | --- |
| `./knowledge-base` | `/data/wiki-repo` | 外挂 Wiki 仓库。 |
| `./data/workspace` | `/app/.workspace` | SQLite、上传中间文件、服务工作区。 |
| `./data/qmd-cache` | `/root/.cache/qmd` | qmd 索引缓存，可选但推荐持久化。 |

如果需要在管理后台使用同步推送功能，外挂 Wiki 必须是 git 仓库，并配置好 remote、branch 和容器内 git 凭据。SSH 推送可以额外挂载只读 `~/.ssh`，或改用 HTTPS token remote。

## AI 客服对接

终端 AI 客服只应调用 Public API：

- 普通 JSON：`POST /api/v1/public/answer`
- 流式 SSE：`POST /api/v1/public/answer/stream`

普通请求示例：

```bash
curl -X POST http://127.0.0.1:8080/api/v1/public/answer \
  -H 'Content-Type: application/json' \
  -d '{
    "question": "这个怎么买？",
    "history": [
      { "role": "user", "content": "住宅IP套餐都有什么？" },
      { "role": "assistant", "content": "住宅IP通常有5M、10M、20M等带宽。" }
    ]
  }'
```

流式请求示例：

```bash
curl -N -X POST http://127.0.0.1:8080/api/v1/public/answer/stream \
  -H 'Content-Type: application/json' \
  -d '{"question":"住宅IP怎么购买？","history":[]}'
```

多轮对话必须传 `history`。如果用户问“这个怎么买”“刚才那个多少钱”，但调用方不传历史上下文，服务端无法稳定判断省略主语指向的业务对象。

完整字段说明、返回类型、SSE 事件、错误码见：[docs/API.md](docs/API.md)。

## 管理后台

管理后台地址：

```text
http://127.0.0.1:8080/admin/login
```

默认账号来自配置或环境变量：

```env
WIKIOS_DEFAULT_ADMIN_USERNAME=admin
WIKIOS_DEFAULT_ADMIN_PASSWORD=admin123
```

管理后台用于：

- 上传并摄入资料
- 执行健康检查
- 综合分析、修复、合并
- 修改前置话术
- 浏览外挂 Wiki 文件
- 查看同步状态、生成提交信息、提交和推送 Wiki 变更

管理员 API 会返回执行详情、工具过程和 reasoning，不应暴露给终端客户。

## 接口文档

正式接口文档：[docs/API.md](docs/API.md)

文档包含：

- 每个接口的鉴权方式、Method、Path、Content-Type
- Query 参数和 Body 参数的类型、含义、是否必填、是否可为空、默认值
- Response 字段类型和含义
- SSE 事件格式
- 错误码
- curl 示例

## 配置文件

默认配置：

- 本地：`configs/config.local.yaml`
- 生产：`configs/config.prod.yaml`

服务启动时会自动加载项目根目录下的 `.env` 和 `.env.local`。如果当前 shell 已设置同名环境变量，shell 值优先。

关键配置：

| 配置 | 含义 |
| --- | --- |
| `mounted_wiki.root` | 外挂 Wiki 根目录，Docker 中默认为 `/data/wiki-repo`。 |
| `mounted_wiki.qmd_index` | qmd index 名称，可通过 `WIKIOS_QMD_INDEX` 配置。 |
| `llm.api_key` | LLM API Key。 |
| `llm.timeout_sec` | Public LLM 请求超时。 |
| `llm.admin_timeout_sec` | Admin/摄入类 LLM 请求超时。 |
| `storage.sqlite_path` | 服务 SQLite 数据库路径。 |
| `web.dist_dir` | 内置 Web 静态产物目录。 |
| `public_intents.path` | 前置话术 YAML 路径。 |
| `knowledge_profile.path` | 企业 profile 配置路径。 |

## 生产注意事项

- 请修改默认管理员密码。
- 请把 Wiki、workspace、qmd cache 持久化到宿主机。
- 如果公开到公网，建议放在反向代理后，并为 Admin 路径增加额外访问控制。
- Public API 不返回内部路径或执行详情，适合 AI 客服调用。
- Admin SSE 包含执行过程和 reasoning，只能用于可信管理后台。
