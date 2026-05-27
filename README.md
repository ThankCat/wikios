# WikiOS

WikiOS 是一个智能 Wiki 知识库微服务，核心职责是连接外挂 LLM-Wiki 仓库、LLM、检索工具和管理后台。它内置 Web 管理后台，但不提供终端 AI 客服前端。

真实业务中的 AI 客服应通过 Public API 对接 WikiOS；后台的“用户会话 / 聊天接口测试”用于内部测试公开问答返回效果。

详细接口文档见：[docs/API.md](docs/API.md)。

## 功能概览

- Public API：面向 AI 客服系统，提供普通 JSON 和 SSE 流式问答。
- Admin Web：内置管理后台，支持上传摄入、健康检查、综合分析、修复、资料库浏览、前置话术、同步。
- 外挂 Wiki：知识库以独立目录或独立 git 仓库挂载，默认生产路径为 `/data/wiki-repo`。
- LLM-Wiki 治理：Wiki 层以挂载知识库的 `AGENT.md` 为最高优先级规则，维护来源、正式知识、政策、流程、对比、概念、实体、综合、意图、链接、根目录报告和演化日志。

## 本地开发

```bash
cp .env.example .env
make dev
```

默认地址：

- API：`http://127.0.0.1:9025`
- Web：`http://127.0.0.1:3000`
- 管理后台：`/dashboard`
- 聊天接口测试：`/conversations` 中的“聊天接口测试”Tab

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

完整部署步骤见：[docs/DOCKER_DEPLOY.md](docs/DOCKER_DEPLOY.md)。

部署入口文件：

```text
Dockerfile
docker-compose.yml
deploy/.env.prod
deploy/config.prod.yaml
deploy/docker-entrypoint.sh
```

生产启动命令：

```bash
docker compose --env-file deploy/.env.prod -f docker-compose.yml up -d --build
```

Docker 部署默认使用 named volume `wikios-wiki-repo` 保存外挂 Wiki。推荐配置 `WIKIOS_WIKI_GIT_URL` + `WIKIOS_WIKI_GIT_TOKEN`，容器首次启动会自动 clone Wiki，后续启动会按配置 pull；后台同步页可检测连接、修复 remote/branch/upstream、提交和推送。SSH deploy key 只作为高级兼容方案保留，具体见 Docker 部署文档。

数据挂载：

| 挂载 | 容器路径 | 用途 |
| --- | --- | --- |
| `wikios-wiki-repo` | `/data/wiki-repo` | 外挂 Wiki 仓库。 |
| `data/workspace` | `/app/.workspace` | SQLite、上传中间文件、服务工作区。 |
| `data/qmd-cache` | `/root/.cache/qmd` | qmd 索引缓存。 |

不要执行 `docker compose down -v`，除非你明确要删除 Wiki volume。

## AI 客服对接

终端 AI 客服只应调用 Public API：

- 普通 JSON：`POST /api/v1/public/answer`
- 外部 Public API 只支持非流式 JSON；后台审查/测试请使用 Admin Public 审查接口。

普通请求示例：

```bash
curl -X POST http://127.0.0.1:9025/api/v1/public/answer \
  -H 'Content-Type: application/json' \
  -d '{
    "question": "这个怎么买？",
    "history": [
      { "role": "user", "content": "住宅IP套餐都有什么？" },
      { "role": "assistant", "content": "住宅IP通常有5M、10M、20M等带宽。" }
    ]
  }'
```

后台审查流式示例：

```bash
curl -N -X POST http://127.0.0.1:9025/api/v1/admin/public-answer/audit/stream \
  -H 'Content-Type: application/json' \
  -d '{"question":"住宅IP怎么购买？","history":[]}'
```

多轮对话必须传 `history`。如果用户问“这个怎么买”“刚才那个多少钱”，但调用方不传历史上下文，服务端无法稳定判断省略主语指向的业务对象。

完整字段说明、返回类型、SSE 事件、错误码见：[docs/API.md](docs/API.md)。

## 管理后台

管理后台地址：

```text
http://127.0.0.1:9025/dashboard
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
- Docker 生产部署：`deploy/config.prod.yaml`

服务启动时会自动加载项目根目录下的 `.env` 和 `.env.local`。如果当前 shell 已设置同名环境变量，shell 值优先。

关键配置：

| 配置 | 含义 |
| --- | --- |
| `mounted_wiki.root` | 外挂 Wiki 根目录，Docker 中默认为 `/data/wiki-repo`。 |
| `mounted_wiki.qmd_index` | qmd index 名称，可通过 `WIKIOS_QMD_INDEX` 配置。 |
| `llm.timeout_sec` | Public 单次 LLM 请求超时默认值，可用 `WIKIOS_LLM_TIMEOUT_SEC` 配置，默认 300 秒。 |
| `llm.admin_timeout_sec` | Admin/摄入类 LLM 请求超时默认值。 |
| `storage.sqlite_path` | 服务 SQLite 数据库路径。 |
| `web.dist_dir` | 内置 Web 静态产物目录。 |
| `public_intents.path` | 前置话术 YAML 路径。 |
| `public_query.response_timeout_sec` | 对外 `/api/v1/public/answer` 整体响应超时，默认 300 秒，适合 routed 两次 LLM 调用；上游网关/客户端超时也要同步放大。 |
| `public_query.answer_log.enabled` | 是否写入 public 问答 JSONL 日志，默认开启。 |
| `public_query.answer_log.redact` | 是否对 public 问答日志做密钥、Token、手机号、邮箱脱敏，默认开启。 |
| `public_query.answer_log.retention_days` | public 问答日志保留天数，默认 14 天。 |
| `WIKIOS_SUPPORT_PHONE` | Public Query 注入给 LLM 的公开客服电话，默认 `400-1080-106`。 |
| `WIKIOS_SUPPORT_WECOM` | Public Query 注入给 LLM 的公开企业微信联系方式，默认 `企业微信`。 |
| `WIKIOS_WIKI_GIT_URL` | 推荐使用 HTTPS Git URL；配置后容器启动时自动 clone/pull 外挂 Wiki。 |
| `WIKIOS_WIKI_GIT_TOKEN` | GitHub fine-grained token，只从环境变量读取，不写入 remote/数据库/前端。 |
| `WIKIOS_WIKI_GIT_USERNAME` | HTTPS token 用户名，默认 `x-access-token`。 |
| `WIKIOS_WIKI_GIT_BRANCH` | 自动 clone/pull 的分支，默认 `main`。 |
| `WIKIOS_WIKI_GIT_PULL_ON_START` | 已有 git 仓库时是否启动自动 pull，默认 `true`。 |
| `WIKIOS_WIKI_GIT_RESET_ON_START` | 是否启动时 hard reset 到远端分支，默认 `false`；开启会丢弃未提交本地改动。 |

模型的 `provider`、`base_url`、`model_name` 和 `api_key` 不再从 YAML 或环境变量读取。请启动服务后在管理后台的“模型”模块新增并启用 OpenAI-compatible 模型；SQLite 中没有启用模型时，public 问答和知识库助手会返回明确的模型配置提示。

## 生产注意事项

- 请修改默认管理员密码。
- 请把 Wiki、workspace、qmd cache 持久化到宿主机。
- 如果公开到公网，建议放在反向代理后，并为 Admin 路径增加额外访问控制。
- Public API 不返回内部路径或执行详情，适合 AI 客服调用。
- Admin SSE 包含执行过程和 reasoning，只能用于可信管理后台。
