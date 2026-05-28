# WikiOS API 文档

本文档描述 WikiOS v1 HTTP API。WikiOS 是智能 Wiki 知识库微服务，终端 AI 客服只应对接 Customer Chat API；Admin API 面向内置管理后台和可信后台系统。

## 1. 通用约定

### 1.1 Base URL

本地默认：

```text
http://127.0.0.1:9025
```

生产环境请替换为实际域名。

### 1.2 类型说明

| 类型 | 含义 | 示例 |
| --- | --- | --- |
| `string` | 字符串 | `"hello"` |
| `number` | 数字，包含整数和浮点数 | `123` |
| `boolean` | 布尔值 | `true` |
| `object` | JSON 对象 | `{ "channel": "web" }` |
| `array<T>` | T 类型数组 | `array<string>` |
| `enum` | 固定枚举值 | `"query"` |
| `ISO-8601 datetime string` | RFC3339/RFC3339Nano 时间字符串 | `"2026-04-25T08:00:00Z"` |
| `SSE event` | `text/event-stream` 事件 | `event: delta` |
| `multipart file` | `multipart/form-data` 文件字段 | `file=@product-knowledge.md` |

### 1.3 鉴权

| 接口类型 | 鉴权方式 |
| --- | --- |
| Customer Chat API | 不需要管理员登录。调用方应在自己的 AI 客服系统侧做用户鉴权、限流和风控。 |
| Admin API | 不需要 WikiOS 管理员登录。请将 Admin API 放在可信网络内，或在反向代理层增加鉴权、限流和访问控制。 |

### 1.4 统一错误结构

所有 JSON 错误响应使用统一结构：

```json
{
  "error": {
    "code": "BAD_REQUEST",
    "message": "message is required"
  }
}
```

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `error` | `object` | 否 | 错误对象。 | `{ "code": "BAD_REQUEST", "message": "..." }` |
| `error.code` | `string` | 否 | 机器可读错误码。 | `"BAD_REQUEST"` |
| `error.message` | `string` | 否 | 人类可读错误信息。 | `"message is required"` |

常见错误码：

| 错误码 | HTTP 状态 | 含义 |
| --- | ---: | --- |
| `BAD_REQUEST` | `400` | 请求参数无效、缺失或格式错误。 |
| `NOT_FOUND` | `404` | 路由不存在。 |
| `CONTEXT_LIMIT_EXCEEDED` | `413` | 管理员多轮上下文超过限制。 |
| `CUSTOMER_INTENTS_UNAVAILABLE` | `503` | 前置话术管理器不可用。 |
| `REVIEWS_UNAVAILABLE` | `503` | 问题审查队列不可用。 |
| `INVALID_CUSTOMER_INTENTS` | `400` | 前置话术 YAML 校验失败。 |
| `GIT_COMMIT_FAILED` | `400` | Git commit 执行失败。 |
| `GIT_PUSH_FAILED` | `400` | Git push 执行失败。 |
| `NO_COMMITS_TO_PUSH` | `400` | 没有可推送的提交。 |
| `INTERNAL_ERROR` | `500` | 服务端内部错误。 |

## 2. 公共类型

### 2.1 ChatMessage

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `id` | `string` | 否 | 是 | `""` | 调用方消息 ID，用于排查和审查队列追踪。 | `"msg_123"` |
| `role` | `enum` | 是 | 否 | 无 | 消息角色。 | 仅允许 `"user"` 或 `"assistant"`。 |
| `content` | `string` | 是 | 否 | 无 | 消息正文。 | `"住宅IP套餐都有什么？"` |
| `created_at` | `ISO-8601 datetime string` | 否 | 是 | `""` | 消息创建时间。建议调用方传入，方便多轮上下文排序和问题排查。 | `"2026-04-30T14:05:00+08:00"` |

### 2.2 AdminAttachment

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `path` | `string` | 是 | 否 | 无 | mounted wiki 内的上传文档路径。 | `"raw/articles/2026-05-07-product-knowledge.md"` |
| `kind` | `string` | 是 | 否 | 无 | 附件类型。 | `"document"` |
| `name` | `string` | 否 | 是 | `""` | 原始文件名或展示名。 | `"产品知识.md"` |

### 2.3 ContextUsage

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `used_tokens` | `number` | 否 | 当前请求估算使用 token 数。 | `2591` |
| `remaining_tokens` | `number` | 否 | 扣除已用后的剩余 token 数。 | `997409` |
| `max_tokens` | `number` | 否 | 当前配置的最大上下文 token。 | `1000000` |
| `reserve_tokens` | `number` | 否 | 预留给模型输出和工具上下文的 token。 | `8192` |
| `blocked` | `boolean` | 否 | 是否已超过 `max_tokens - reserve_tokens`。 | `false` |
| `estimated` | `boolean` | 否 | 是否为估算值。 | `true` |
| `counter` | `string` | 否 | 计数器类型。 | `"tokenizer"` |
| `tokenizer` | `string` | 是 | tokenizer 名称。 | `"cl100k_base"` |
| `error` | `string` | 是 | token 统计降级或失败原因。 | `"tokenizer unavailable"` |

### 2.4 Execution

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `id` | `string` | 否 | 执行 ID。 | `"exec_550e8400-e29b-41d4-a716-446655440000"` |
| `kind` | `string` | 否 | 执行类型。 | `"lint"` |
| `status` | `enum` | 否 | 执行状态。 | `"RUNNING"`、`"SUCCESS"`、`"FAILED"`、`"PARTIAL_SUCCESS"` |
| `steps` | `array<ExecutionStep>` | 否 | 执行步骤列表。 | `[]` |
| `error` | `string` | 是 | 失败原因。 | `"llm request timeout after 300s"` |
| `started_at` | `ISO-8601 datetime string` | 否 | 开始时间。 | `"2026-04-25T08:00:00Z"` |
| `ended_at` | `ISO-8601 datetime string` | 是 | 结束时间；执行中可能为空或零值。 | `"2026-04-25T08:00:30Z"` |

### 2.5 ExecutionStep

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `name` | `string` | 否 | 步骤名称。 | `"llm customer chat"` |
| `tool` | `string` | 是 | 工具名。 | `"llm.chat"`、`"exec.qmd"` |
| `status` | `string` | 否 | 步骤状态。 | `"SUCCESS"`、`"FAILED"` |
| `input` | `object` | 是 | 步骤输入摘要。 | `{ "model": "gpt-compatible-chat" }` |
| `output` | `object` | 是 | 步骤输出摘要。 | `{ "response_preview": "..." }` |
| `duration_ms` | `number` | 是 | 步骤耗时，毫秒。 | `1200` |
| `started_at` | `ISO-8601 datetime string` | 是 | 步骤开始时间。 | `"2026-04-25T08:00:00Z"` |
| `ended_at` | `ISO-8601 datetime string` | 是 | 步骤结束时间。 | `"2026-04-25T08:00:01Z"` |

## 3. Health

### GET `/healthz`

用途：服务健康检查。

鉴权：无。

Content-Type：无请求体。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `status` | `string` | 否 | 服务状态。 | `"ok"` |

#### curl

```bash
curl http://127.0.0.1:9025/healthz
```

## 4. App Config

### GET `/app-config.json`

用途：内置 Web 前端读取当前挂载 Wiki 名称和 Web 状态。

鉴权：无。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `mountedWikiName` | `string` | 否 | 当前挂载 Wiki 展示名。 | `"default-wiki"` |
| `webEnabled` | `boolean` | 否 | 是否启用内置 Web。 | `true` |

#### curl

```bash
curl http://127.0.0.1:9025/app-config.json
```

## 5. Customer Chat API

### POST `/api/v1/customer/chat`

用途：唯一客户问答接口，同时服务外部客户/API 调用和 Wikios 内部测试调用。

鉴权：无。调用方应在自己的业务系统侧做用户鉴权、限流和风控。

Content-Type：`application/json`

#### Request Body

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `message` | `string` | 是 | 否 | 无 | 当前客户消息。 | `"静态 IP 怎么卖？"` |
| `history` | `array<ChatMessage>` | 否 | 是 | `[]` | 最近多轮对话上下文。 | 建议只传必要历史问答。 |
| `session_id` | `string` | 否 | 是 | `""` | 调用方会话 ID。 | `"s_456"` |
| `user_id` | `string` | 否 | 是 | `""` | 调用方用户 ID。 | `"u_123"` |
| `message_id` | `string` | 否 | 是 | `""` | 本轮客户消息 ID。 | `"msg_user_001"` |
| `answer_message_id` | `string` | 否 | 是 | `""` | 调用方预生成的助手消息 ID。 | `"msg_assistant_001"` |
| `message_created_at` | `ISO-8601 datetime string` | 否 | 是 | `""` | 本轮客户消息创建时间。 | `"2026-05-28T10:00:00+08:00"` |
| `context` | `object` | 否 | 是 | `{}` | 调用方扩展上下文。 | `{ "channel": "web" }` |
| `stream` | `boolean` | 否 | 否 | `false` | 是否使用 SSE 流式响应。 | `true` |
| `simulation` | `boolean` | 否 | 否 | `false` | 是否为内部测试。 | 后台测试传 `true`。 |
| `entrypoint` | `enum` | 否 | 否 | `"external"` | 调用来源。 | 仅 `"external"` / `"internal"`。 |

多轮要求：如果客户消息省略主语，例如“这个怎么买？”，调用方应传 `history`，否则 Router 无法准确解析指代。

链路说明：客户问答只走 Router + Retrieval + Specialist。服务端负责 Router 调用、证据检索、候选页面读取、Specialist 调用、JSON 解析、引用过滤、日志和审计记录；客户可见答案由 Specialist 的结构化输出决定，服务端不生成、改写或替换客户可见答案。

#### 非流式 Response

响应头包含 `X-Trace-ID`，可用它读取本地审计 JSONL 详情。响应体只包含：

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `answer` | `string` | 否 | 给终端客户展示的答案。 | `"静态 IP 按个/月计费。"` |
| `received_at` | `ISO-8601 datetime string` | 否 | 服务端接收时间。 | `"2026-05-28T02:00:00Z"` |
| `answered_at` | `ISO-8601 datetime string` | 否 | 服务端完成时间。 | `"2026-05-28T02:00:03Z"` |

客户问答响应体不会返回 Router、Retrieval、Specialist、Prompt、thinking、details 或 trace_id。

#### SSE 事件

`stream=true` 时返回 `text/event-stream`，只输出客户可见事件：

| 事件 | 字段 | 类型 | 含义 |
| --- | --- | --- | --- |
| `delta` | `data.delta` | `string` | 答案增量文本。 |
| `result` | `data.answer` | `string` | 完整答案。 |
| `result` | `data.received_at` / `data.answered_at` | `string` | 接收/完成时间。 |
| `done` | `data.ok` | `boolean` | 是否成功结束。 |

审计详情不随响应返回，后台应读取 `GET /api/v1/admin/customer-chat/traces/:trace_id`。

已删除旧入口，服务端不做兼容转发：

```text
POST /api/v1/public/answer
POST /api/v1/public/answer/stream
POST /api/v1/admin/public-answer/audit
POST /api/v1/admin/public-answer/audit/stream
```

#### curl

```bash
curl -i -X POST http://127.0.0.1:9025/api/v1/customer/chat \
  -H 'Content-Type: application/json' \
  -d '{
    "message": "静态 IP 怎么卖？",
    "session_id": "s_456",
    "message_id": "msg_user_001",
    "answer_message_id": "msg_assistant_001",
    "entrypoint": "external"
  }'
```

### POST `/api/v1/customer/context/estimate`

用途：估算客户问答请求上下文占用。

Request Body：同客户问答核心字段，至少包含 `message`。

Response：`{ "mode": "customer", "context_usage": {...} }`。

### GET `/api/v1/admin/customer-chat/traces/:trace_id`

用途：后台按 `X-Trace-ID` 读取本地 customer chat JSONL 审计详情。该接口不是问答入口。

返回内容是本地 JSONL 中对应 `trace_id` 的完整审计记录。Customer Chat JSONL 顶层只包含标准字段，不写入兼容性顶层字段。

#### Response 顶级字段

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `schema_version` | `string` | 否 | 审计 schema 版本。 | `"customer_chat_audit.v1"` |
| `record_type` | `string` | 否 | 记录类型。 | `"customer_chat_trace"` |
| `trace_id` | `string` | 否 | 全链路 trace ID。 | `"trace_xxx"` |
| `session_id` | `string` | 是 | 调用方会话 ID。 | `"s_456"` |
| `time` | `object` | 否 | `received_at`、`answered_at`、`logged_at`、`total_duration_ms`。 | `{}` |
| `runtime` | `object` | 否 | 运行快照，含 `entrypoint`、模型 ID、Router contract 等。 | `{}` |
| `request` | `object` | 否 | 本轮输入与历史问答上下文。 | `{ "message": "静态 IP 怎么卖？" }` |
| `router` | `object` | 否 | Router 模型、耗时、thinking、原始输出和解析 JSON。 | `{}` |
| `retrieval` | `object` | 否 | 服务层受控检索、缓存、候选页和证据源。 | `{}` |
| `specialist` | `object` | 否 | Specialist 模型、耗时、thinking、输入引用、原始输出和解析 JSON。 | `{}` |
| `final` | `object` | 否 | 最终客户可见答案。 | `{ "answer": "..." }` |
| `error` | `object` | 是 | 失败阶段和错误信息；成功时为 `null`。 | `null` |
| `review` | `object` | 否 | 人工审查占位字段。 | `{ "status": "unreviewed" }` |

计数字段语义：

| 字段 | 含义 |
| --- | --- |
| `retrieval.source_count` | 服务层实际提供给 Specialist 的证据源数量。 |
| `final.source_count` | Specialist 最终输出 `specialist.output.sources` 中实际引用的 source 数量。 |

`runtime.git_commit` 用于记录当前运行代码版本。若部署环境没有注入 `WIKIOS_GIT_COMMIT`、`GIT_COMMIT` 或 `VERCEL_GIT_COMMIT_SHA`，该字段为空字符串。

#### curl

```bash
curl http://127.0.0.1:9025/api/v1/admin/customer-chat/traces/trace_xxx
```

### GET `/api/v1/admin/customer-conversations`

用途：后台读取 Customer Chat JSONL 并按 session 聚合客户会话列表。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

#### Query 参数

| 字段 | 类型 | 必填 | 默认值 | 含义 |
| --- | --- | --- | --- | --- |
| `q` | `string` | 否 | `""` | 搜索 session、trace、问题、答案或摘要。 |
| `page` | `number` | 否 | `1` | 页码。 |
| `page_size` | `number` | 否 | `20` | 每页数量，最大 100。 |
| `from` / `start` | `string` | 否 | `""` | 起始日期或时间。 |
| `to` / `end` | `string` | 否 | `""` | 结束日期或时间。 |

#### Response

| 字段 | 类型 | 可为空 | 含义 |
| --- | --- | --- | --- |
| `conversations` | `array<object>` | 否 | 会话摘要列表。 |
| `total` | `number` | 否 | 总会话数。 |
| `page` | `number` | 否 | 当前页码。 |
| `page_size` | `number` | 否 | 当前每页数量。 |
| `has_more` | `boolean` | 否 | 是否还有下一页。 |
| `log` | `object` | 否 | Customer Chat JSONL 日志策略。 |

#### curl

```bash
curl 'http://127.0.0.1:9025/api/v1/admin/customer-conversations?page_size=20&q=静态'
```

### GET `/api/v1/admin/customer-conversations/:session_id`

用途：后台读取某个客户会话的问答消息列表。该接口只返回会话摘要和安全详情，不返回完整 Router、Retrieval、Specialist、Prompt 或 thinking；完整审计请使用 trace 详情接口。

#### curl

```bash
curl http://127.0.0.1:9025/api/v1/admin/customer-conversations/s_456
```

## 6. Admin Dashboard API

### GET `/api/v1/admin/dashboard`

用途：为 SaaS 后台总览页提供聚合状态，包含当前模型、模型数量、待审数量、qmd 状态、Wiki Git 状态和客户问答日志策略。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

Content-Type：无请求体。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `active_model` | `Model` | 是 | 当前启用模型。无启用模型时不返回。 | `{ "display_name": "Qianwen" }` |
| `models_total` | `number` | 否 | 已配置模型数量。 | `2` |
| `review_pending` | `number` | 否 | 待审问题数量。 | `3` |
| `sync.branch` | `string` | 是 | Wiki 当前 Git 分支。 | `"main"` |
| `sync.changed_count` | `number` | 否 | Wiki 未提交变更数。 | `1` |
| `sync.can_push` | `boolean` | 否 | 是否有待推送提交。 | `false` |
| `qmd.ok` | `boolean` | 否 | qmd collection 是否可读取。 | `true` |
| `qmd.index` | `string` | 否 | qmd index 名称。 | `"knowledge-base"` |
| `customer_chat_log.enabled` | `boolean` | 否 | 客户问答日志是否开启。 | `true` |
| `customer_chat_log.redact` | `boolean` | 否 | 客户问答日志是否脱敏。 | `true` |
| `customer_chat_log.retention_days` | `number` | 否 | 客户问答日志保留天数。 | `14` |
| `recent_errors` | `array<object>` | 否 | 聚合过程中可安全展示给管理员的错误摘要。 | `[]` |
| `generated_at` | `ISO-8601 datetime string` | 否 | 聚合生成时间。 | `"2026-05-14T10:00:00Z"` |

Dashboard 接口不会返回完整 API Key；模型字段沿用 Model 的遮罩响应。

#### curl

```bash
curl http://127.0.0.1:9025/api/v1/admin/dashboard
```

## 7. Admin Knowledge Assistant API

### POST `/api/v1/admin/knowledge/assistant/chat`

用途：唯一管理员知识库助手接口，用于后台对话和 Wiki 治理操作。默认返回流式 SSE；显式传 `stream:false` 时返回非流式 JSON。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

Content-Type：`application/json`

#### Request Body

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `message` | `string` | 是 | 否 | 无 | 管理员指令或问题。 | `"执行一次健康检查"` |
| `stream` | `boolean` | 否 | 否 | `true` | 是否使用 SSE 流式响应；传 `false` 返回非流式 JSON。 | `false` |
| `mode_hint` | `enum` | 否 | 是 | 自动识别 | 指定治理模式。 | `"lint"` |
| `context` | `object` | 否 | 是 | `{}` | 附加上下文，例如上一轮模式、目标路径。 | `{ "last_mode": "lint" }` |
| `attachments` | `array<AdminAttachment>` | 否 | 是 | `[]` | 已上传或待处理附件摘要。 | `[{ "path": "raw/articles/2026-05-07-product-knowledge.md", "kind": "document" }]` |
| `history` | `array<ChatMessage>` | 否 | 是 | `[]` | 管理员多轮上下文。 | `[{ "role": "user", "content": "..." }]` |

`mode_hint` 允许值：

| 值 | 含义 |
| --- | --- |
| `query` | 查询 Wiki。 |
| `ingest` | 摄入资料。 |
| `lint` | 健康检查。 |
| `reflect` | 综合分析。 |
| `repair` | 修复问题。 |
| `merge` | 合并冲突或重复知识。 |
| `add-question` | 记录开放问题。 |
| `sync` | 同步相关操作。 |

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `mode` | `string` | 否 | 实际执行模式。 | `"lint"` |
| `reply` | `string` | 否 | 管理端展示摘要。 | `"健康检查完成"` |
| `details` | `object` | 否 | 结构化执行详情。 | `{}` |
| `context_usage` | `ContextUsage` | 是 | 上下文占用。 | `{ "used_tokens": 2591, ... }` |
| `execution` | `Execution` | 否 | 执行记录。 | `{ "id": "exec_...", "status": "SUCCESS" }` |

#### curl

```bash
curl -X POST http://127.0.0.1:9025/api/v1/admin/knowledge/assistant/chat \
  -H 'Content-Type: application/json' \
  -d '{
    "message": "执行一次健康检查",
    "stream": false,
    "mode_hint": "lint",
    "context": {},
    "attachments": [],
    "history": []
  }'
```

### POST `/api/v1/admin/knowledge/assistant/chat`（流式）

用途：管理员流式对话和 Wiki 治理操作。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

Content-Type：`application/json`

Response Content-Type：`text/event-stream`

Request Body：同 `POST /api/v1/admin/knowledge/assistant/chat`。`stream` 省略或显式传 `true` 时返回 SSE。

#### SSE 事件

| 事件 | 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- | --- |
| `meta` | `data.mode` | `string` | 否 | 执行模式。 | `"query"` |
| `meta` | `data.execution_id` | `string` | 否 | 执行 ID。 | `"exec_..."` |
| `meta` | `data.started_at` | `ISO-8601 datetime string` | 否 | 开始时间。 | `"2026-04-25T08:00:00Z"` |
| `meta` | `data.context_usage` | `ContextUsage` | 否 | 上下文用量。 | `{}` |
| `prompt` | `data.name` | `string` | 否 | LLM 步骤名。 | `"llm direct admin"` |
| `prompt` | `data.model` | `string` | 否 | 模型名。 | `"gpt-compatible-chat"` |
| `prompt` | `data.messages` | `array<object>` | 否 | 发送给 LLM 的消息。 | `[]` |
| `prompt` | `data.prompt_chars` | `number` | 否 | prompt 字符数。 | `137122` |
| `prompt` | `data.prompt_estimated_tokens` | `number` | 否 | prompt 估算 token。 | `34280` |
| `prompt` | `data.timeout_sec` | `number` | 否 | LLM 请求超时时间。 | `300` |
| `llm_reasoning_delta` | `data.name` | `string` | 否 | LLM 步骤名。 | `"llm direct admin"` |
| `llm_reasoning_delta` | `data.delta` | `string` | 否 | 思考过程增量。 | `"我需要先检查..."` |
| `llm_reasoning_delta` | `data.created_at` | `ISO-8601 datetime string` | 否 | 事件时间。 | `"2026-04-25T08:00:00Z"` |
| `llm_delta` | `data.delta` | `string` | 否 | 正文增量。 | `"健康检查完成"` |
| `step_start` | `data.name` | `string` | 否 | 步骤名称。 | `"qmd update"` |
| `step_start` | `data.tool` | `string` | 否 | 工具名称。 | `"exec.qmd"` |
| `step_finish` | `data` | `ExecutionStep` | 否 | 完成的步骤对象。 | `{ "status": "SUCCESS" }` |
| `result` | `data.reply` | `string` | 否 | 最终展示摘要。 | `"修复完成"` |
| `result` | `data.details` | `object` | 否 | 结构化详情。 | `{}` |
| `result` | `data.execution` | `Execution` | 否 | 执行记录。 | `{}` |
| `error` | `data.message` | `string` | 否 | 错误信息。 | `"CONTEXT_LIMIT_EXCEEDED"` |
| `keepalive` | `data.ts` | `ISO-8601 datetime string` | 否 | 保活时间。 | `"2026-04-25T08:00:00Z"` |
| `done` | `data.execution` | `Execution` | 是 | 最终执行对象。 | `{}` |

注意：管理员 SSE 可能包含工具调用、prompt 摘要和 reasoning。它只能用于管理后台，禁止直接暴露给终端客户。

已删除旧入口，服务端不做兼容转发：

```text
POST /api/v1/admin/chat
POST /api/v1/admin/chat/stream
```

#### curl

```bash
curl -N -X POST http://127.0.0.1:9025/api/v1/admin/knowledge/assistant/chat \
  -H 'Content-Type: application/json' \
  -d '{
    "message": "执行一次健康检查",
    "stream": true,
    "mode_hint": "lint",
    "history": []
  }'
```

### POST `/api/v1/admin/context/estimate`

用途：估算管理员请求上下文大小。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

Content-Type：`application/json`

Request Body：同 `POST /api/v1/admin/knowledge/assistant/chat` 的上下文字段；该接口始终返回 JSON，不返回 SSE。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `mode` | `string` | 否 | 识别出的执行模式。 | `"query"` |
| `context_usage` | `ContextUsage` | 否 | 上下文占用。 | `{ "blocked": false }` |

若上下文超过限制，管理员知识库助手接口返回 HTTP `413` 和 `CONTEXT_LIMIT_EXCEEDED`。

#### curl

```bash
curl -X POST http://127.0.0.1:9025/api/v1/admin/context/estimate \
  -H 'Content-Type: application/json' \
  -d '{
    "message": "执行一次健康检查",
    "stream": false,
    "mode_hint": "lint",
    "history": []
  }'
```

## 8. Upload API

### POST `/api/v1/admin/upload`

用途：上传文档并交给管理员直连模式按 mounted wiki 的 `AGENT.md` INGEST 流程摄入。服务端只做路径安全、类型白名单和保存原始文档，不做结构化数据解析或分段预处理。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

Content-Type：`multipart/form-data`

#### Form 参数

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `file` | `multipart file` | 是 | 否 | 无 | 待上传文档。仅支持 `.md`、`.markdown`、`.txt`、`.text`、`.doc`、`.docx`、`.rtf`；不支持表格、JSON、图片或其它结构化数据。 | `产品知识.md` |

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `reply` | `string` | 否 | 展示摘要。 | `"上传完成"` |
| `details` | `object` | 否 | 摄入详情。 | `{}` |
| `execution` | `Execution` | 是 | 执行记录。 | `{}` |

`details` 常见字段包括 `stored_path`、`media_kind`、`source_format`、`artifacts`、`output_files`、`warnings`、`steps`。上传文档保存到 `raw/articles/`，`media_kind` 和 `source_format` 为 `"document"`。

#### curl

```bash
curl -X POST http://127.0.0.1:9025/api/v1/admin/upload \
  -F 'file=@产品知识.md'
```

### POST `/api/v1/admin/upload/stream`

用途：上传文档并流式返回摄入过程。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

Content-Type：`multipart/form-data`

Response Content-Type：`text/event-stream`

Form 参数：同 `POST /api/v1/admin/upload`。

SSE 事件：同管理员执行流，常见事件包括 `meta`、`prompt`、`llm_reasoning_delta`、`llm_delta`、`step_start`、`step_finish`、`result`、`error`、`keepalive`、`done`。上传流不返回结构化分段或分类事件。

#### curl

```bash
curl -N -X POST http://127.0.0.1:9025/api/v1/admin/upload/stream \
  -F 'file=@产品知识.md'
```

## 9. Wiki Browser API

### GET `/api/v1/admin/wiki/tree`

用途：查看外挂 Wiki 目录。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

#### Query 参数

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `path` | `string` | 否 | 是 | `""` | 相对 mounted wiki 根目录的路径。 | `"wiki/knowledge"` |

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `path` | `string` | 否 | 当前目录路径。 | `"wiki/knowledge"` |
| `items` | `array<WikiTreeItem>` | 否 | 目录项。 | `[]` |

#### WikiTreeItem

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `name` | `string` | 否 | 文件或目录名。 | `"static-ip.md"` |
| `path` | `string` | 否 | 相对 mounted wiki 根目录路径。 | `"wiki/knowledge/static-ip.md"` |
| `is_dir` | `boolean` | 否 | 是否目录。 | `false` |
| `size` | `number` | 否 | 字节大小。 | `1234` |
| `modified_at` | `ISO-8601 datetime string` | 否 | 修改时间。 | `"2026-04-25T08:00:00Z"` |
| `preview` | `enum` | 否 | 预览类型。 | `"markdown"` |

`preview` 允许值：`markdown`、`download`。Markdown 文件返回正文，其它文件作为下载处理。

#### curl

```bash
curl 'http://127.0.0.1:9025/api/v1/admin/wiki/tree?path=wiki%2Fknowledge'
```

### GET `/api/v1/admin/wiki/file`

用途：在线查看 Wiki 文件。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

#### Query 参数

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `path` | `string` | 是 | 否 | 无 | 相对 mounted wiki 根目录的文件路径。 | `"wiki/knowledge/static-ip.md"` |

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `path` | `string` | 否 | 文件路径。 | `"wiki/knowledge/static-ip.md"` |
| `name` | `string` | 否 | 文件名。 | `"static-ip.md"` |
| `size` | `number` | 否 | 字节大小。 | `1234` |
| `modified_at` | `ISO-8601 datetime string` | 否 | 修改时间。 | `"2026-04-25T08:00:00Z"` |
| `preview` | `enum` | 否 | 预览类型。 | `"markdown"` |
| `content` | `string` | 是 | Markdown 文件内容。 | `"# 静态 IP"` |
| `download_url` | `string` | 否 | 下载地址。 | `"/api/v1/admin/wiki/download?path=..."` |

字段出现规则：

| preview | 字段 |
| --- | --- |
| `markdown` | 返回 `content`。 |
| `download` | 不返回文件内容，只返回 `download_url`。 |

#### curl

```bash
curl 'http://127.0.0.1:9025/api/v1/admin/wiki/file?path=wiki%2Fknowledge%2Fstatic-ip.md'
```

### GET `/api/v1/admin/wiki/download`

用途：下载 Wiki 文件。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

#### Query 参数

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `path` | `string` | 是 | 否 | 无 | 相对 mounted wiki 根目录的文件路径。 | `"raw/files/demo.pdf"` |

Response：文件流，不返回 JSON。

路径限制：所有 Wiki Browser API 只能访问 mounted wiki 根目录；禁止 `.git/`；禁止 `../` 路径穿越。

#### curl

```bash
curl -o demo.pdf 'http://127.0.0.1:9025/api/v1/admin/wiki/download?path=raw%2Ffiles%2Fdemo.pdf'
```

## 10. Sync API

### GET `/api/v1/admin/sync/status`

用途：查看外挂 Wiki git 状态、待提交文件和待推送提交。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `branch` | `string` | 否 | 当前分支。 | `"main"` |
| `remote` | `string` | 否 | 默认远端。 | `"origin"` |
| `ahead` | `number` | 否 | 本地领先远端提交数。 | `1` |
| `behind` | `number` | 否 | 本地落后远端提交数。 | `0` |
| `changed_count` | `number` | 否 | 待提交文件数量。 | `2` |
| `push_count` | `number` | 否 | 待推送提交数量。 | `1` |
| `can_push` | `boolean` | 否 | 是否可以推送。 | `true` |
| `can_commit` | `boolean` | 否 | 是否有可提交文件。 | `true` |
| `repo_ready` | `boolean` | 否 | Wiki root 是否已经是 Git 仓库。 | `true` |
| `remote_ready` | `boolean` | 否 | 默认 remote 是否存在。 | `true` |
| `branch_ready` | `boolean` | 否 | 当前分支/upstream 是否可用。 | `true` |
| `auth_configured` | `boolean` | 否 | HTTPS Token 或 SSH 非交互凭据是否已配置。 | `true` |
| `needs_setup` | `boolean` | 否 | 是否需要执行同步配置修复。 | `false` |
| `clean` | `boolean` | 否 | 是否无待提交、无待推送、无 behind。 | `true` |
| `remote_url_redacted` | `string` | 是 | 脱敏后的 remote URL；不会返回 token。 | `"https://github.com/acme/wiki.git"` |
| `setup_hint` | `string` | 是 | 同步配置缺失或异常时的处理提示。 | `"请配置 WIKIOS_WIKI_GIT_TOKEN"` |
| `commits_to_push` | `array<SyncCommitInfo>` | 否 | 待推送提交。 | `[]` |
| `recent_commits` | `array<SyncCommitInfo>` | 否 | 最近提交。 | `[]` |
| `files` | `array<SyncStatusFile>` | 否 | 待提交文件。 | `[]` |

#### curl

```bash
curl http://127.0.0.1:9025/api/v1/admin/sync/status
```

### POST `/api/v1/admin/sync/test`

用途：检测 Git remote/branch 是否能通过非交互方式访问。HTTPS 默认使用 `WIKIOS_WIKI_GIT_URL` + `WIKIOS_WIKI_GIT_TOKEN`，token 不写入 remote，不返回前端。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `ok` | `boolean` | 否 | 是否检测成功。 | `true` |
| `remote` | `string` | 否 | 默认 remote。 | `"origin"` |
| `branch` | `string` | 否 | 检测分支。 | `"main"` |
| `stdout` | `string` | 是 | Git stdout。 | `""` |
| `stderr` | `string` | 是 | Git stderr。 | `""` |
| `exit_code` | `number` | 否 | Git 退出码。 | `0` |

#### curl

```bash
curl -X POST http://127.0.0.1:9025/api/v1/admin/sync/test
```

### POST `/api/v1/admin/sync/setup`

用途：修复同步配置。空 Wiki root 且配置了 `WIKIOS_WIKI_GIT_URL` 时会 clone；已有 Git 仓库时会设置 remote、fetch branch、配置 upstream。不执行 hard reset，不删除或覆盖已有非 Git 内容。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `ok` | `boolean` | 否 | 是否修复成功。 | `true` |
| `action` | `enum` | 否 | 执行动作：`clone` 或 `setup`。 | `"setup"` |
| `status` | `SyncStatusResponse` | 否 | 修复后的同步状态。 | `{...}` |
| `stdout` | `string` | 是 | Git stdout。 | `""` |
| `stderr` | `string` | 是 | Git stderr。 | `""` |
| `exit_code` | `number` | 否 | Git 退出码。 | `0` |

#### curl

```bash
curl -X POST http://127.0.0.1:9025/api/v1/admin/sync/setup
```

#### SyncStatusFile

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `path` | `string` | 否 | 文件路径。 | `"wiki/knowledge/static-ip.md"` |
| `old_path` | `string` | 是 | rename 的旧路径。 | `"wiki/knowledge/old-static-ip.md"` |
| `status` | `string` | 否 | Git 状态主码。 | `"M"` |
| `index` | `string` | 是 | 暂存区状态。 | `"M"` |
| `worktree` | `string` | 是 | 工作区状态。 | `"M"` |
| `preview` | `enum` | 否 | 预览类型。 | `"markdown"` |
| `default_on` | `boolean` | 否 | 前端默认是否勾选。 | `true` |
| `deleted` | `boolean` | 否 | 是否删除文件。 | `false` |

#### SyncCommitInfo

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `hash` | `string` | 否 | 短提交哈希。 | `"abc1234"` |
| `date` | `string` | 否 | 提交日期。 | `"2026-04-25"` |
| `author` | `string` | 否 | 作者。 | `"Admin"` |
| `subject` | `string` | 否 | 提交标题。 | `"docs: update static ip knowledge"` |

### POST `/api/v1/admin/sync/generate-message`

用途：根据选择文件生成提交信息。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

Content-Type：`application/json`

#### Request Body

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `paths` | `array<string>` | 是 | 否 | 无 | 需要生成提交信息的文件。 | 必须来自 `/sync/status`，不可为空数组。 |

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `message` | `string` | 否 | 生成的提交信息。 | `"docs: update static ip knowledge"` |
| `rule` | `string` | 否 | 生成规则说明。 | `"LLM generated from selected wiki diff"` |
| `paths` | `array<string>` | 否 | 本次使用的文件路径。 | `["wiki/knowledge/static-ip.md"]` |

#### curl

```bash
curl -X POST http://127.0.0.1:9025/api/v1/admin/sync/generate-message \
  -H 'Content-Type: application/json' \
  -d '{"paths":["wiki/knowledge/static-ip.md"]}'
```

### POST `/api/v1/admin/sync/commit`

用途：提交选择的 Wiki 文件。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

Content-Type：`application/json`

#### Request Body

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `paths` | `array<string>` | 是 | 否 | 无 | 要提交的文件路径。 | 必须来自 `/sync/status`，不可为空数组。 |
| `message` | `string` | 是 | 否 | 无 | Git commit message。 | `"docs: update static ip knowledge"` |

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `ok` | `boolean` | 否 | 是否提交成功。 | `true` |
| `hash` | `string` | 否 | 新提交短哈希。 | `"abc1234"` |
| `stdout` | `string` | 是 | Git stdout。 | `""` |
| `stderr` | `string` | 是 | Git stderr。 | `""` |
| `exit_code` | `number` | 否 | Git 退出码。 | `0` |

#### curl

```bash
curl -X POST http://127.0.0.1:9025/api/v1/admin/sync/commit \
  -H 'Content-Type: application/json' \
  -d '{
    "paths": ["wiki/knowledge/static-ip.md"],
    "message": "docs: update static ip knowledge"
  }'
```

### POST `/api/v1/admin/sync/push`

用途：推送当前分支到远端。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

Content-Type：`application/json`

#### Request Body

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `remote` | `string` | 否 | 是 | 配置中的 `sync.remote` | Git 远端名。 | `"origin"` |
| `branch` | `string` | 否 | 是 | 配置中的 `sync.branch` | Git 分支名。 | `"main"` |

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `ok` | `boolean` | 否 | 是否推送成功。 | `true` |
| `remote` | `string` | 否 | 推送远端。 | `"origin"` |
| `branch` | `string` | 否 | 推送分支。 | `"main"` |
| `stdout` | `string` | 是 | Git stdout。 | `""` |
| `stderr` | `string` | 是 | Git stderr。 | `""` |
| `exit_code` | `number` | 否 | Git 退出码。 | `0` |

没有未推送提交时返回 `NO_COMMITS_TO_PUSH`。

#### curl

```bash
curl -X POST http://127.0.0.1:9025/api/v1/admin/sync/push \
  -H 'Content-Type: application/json' \
  -d '{"remote":"origin","branch":"main"}'
```

## 11. LLM Model API

用途：管理 OpenAI-compatible 模型，并切换 WikiOS 全站当前启用模型。所有接口默认不要求 WikiOS 登录，请在可信网络或反向代理鉴权后使用。API Key 保存到 SQLite，但响应中只返回 `has_api_key` 和 `api_key_mask`，不会回显完整密钥。

### Endpoints

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/api/v1/admin/models` | 获取模型列表。 |
| `POST` | `/api/v1/admin/models` | 新增模型。 |
| `GET` | `/api/v1/admin/models/:id` | 获取单个模型。 |
| `PUT` | `/api/v1/admin/models/:id` | 更新模型，`api_key` 留空时保留原密钥。 |
| `DELETE` | `/api/v1/admin/models/:id` | 删除模型；删除当前模型后全站进入未启用模型状态。 |
| `POST` | `/api/v1/admin/models/:id/activate` | 切换当前启用模型。 |
| `POST` | `/api/v1/admin/models/:id/test` | 使用该模型发起一次最小连接测试。 |

### Model

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `id` | `string` | 否 | 模型 ID。 | `"2b0b..."` |
| `display_name` | `string` | 否 | 显示名称。 | `"生产客服模型"` |
| `provider` | `string` | 否 | 服务商标识。 | `"openai-compatible"` |
| `base_url` | `string` | 否 | OpenAI-compatible 端点。 | `"https://api.example.com/v1"` |
| `model_name` | `string` | 否 | 请求体中的模型名。 | `"gpt-compatible-chat"` |
| `has_api_key` | `boolean` | 否 | 是否已配置密钥。 | `true` |
| `api_key_mask` | `string` | 是 | 遮罩后的密钥。 | `"sk-1...abcd"` |
| `is_active` | `boolean` | 否 | 是否为全站当前模型。 | `true` |
| `timeout_sec` | `number` | 否 | 普通请求超时秒数。 | `90` |
| `admin_timeout_sec` | `number` | 否 | 管理任务超时秒数。 | `300` |

#### curl

```bash
curl -X POST http://127.0.0.1:9025/api/v1/admin/models \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"生产客服模型","provider":"openai-compatible","base_url":"https://api.example.com/v1","model_name":"gpt-compatible-chat","api_key":"sk-xxx","timeout_sec":90,"admin_timeout_sec":300}'
```

## 12. Review API

审查队列以 mounted wiki 文件为事实来源：`wiki/unconfirmed/` 保存待审问题，`wiki/forbidden/` 保存驳回后的禁答问题。所有接口默认不要求 WikiOS 登录，请在可信网络或反向代理鉴权后使用。

### GET `/api/v1/admin/reviews/count`

用途：读取待审查数量。

Response：`{ "pending_count": 3 }`

### GET `/api/v1/admin/reviews/next`

用途：读取下一条待审查问题。可选 query `cursor=<review_id>`，用于跳过当前条读取后续条目。

Response 字段：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `item` | `object` | 当前待审条目；没有待审时为空。 |
| `pending_count` | `number` | 当前待审总数。 |
| `remaining_count` | `number` | 当前条之后还剩多少条。 |
| `target_paths` | `array<object>` | 可写入的目标知识页列表。 |

### POST `/api/v1/admin/reviews/:id/approve`

用途：通过当前待审问题，把管理员确认后的知识沉淀到指定正式知识页或意图页。

Request Body：

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `question` | `string` | 否 | 管理员修正后的问题；为空则使用原问题。 |
| `answer` | `string` | 否 | 管理员确认或修改后的回答；为空则使用待审草稿。 |
| `target_path` | `string` | 否 | 目标知识页；为空则使用待审条目的建议路径。例如 `wiki/knowledge/static-ip.md`、`wiki/policies/refund-policy.md` 或 `wiki/intents/pending-customer-questions.md`。 |

Response：`{ "ok": true, "item": {...}, "pending_count": 2 }`

### POST `/api/v1/admin/reviews/:id/reject`

用途：驳回当前待审问题，移动到 `wiki/forbidden/`，后续相似问题禁答。

Request Body：

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `reason` | `string` | 否 | 驳回原因；为空时使用默认原因。 |

Response：`{ "ok": true, "item": {...}, "pending_count": 2 }`

### POST `/api/v1/admin/reviews/:id/delete`

用途：删除当前待审问题，不写入正式知识页，也不写入 `wiki/forbidden/`。适用于误输入、无意义内容、重复测试等不需要沉淀的问题。

Request Body：无。

Response：`{ "ok": true, "item": {...}, "pending_count": 2 }`

## 13. Customer Intents API

### GET `/api/v1/admin/customer-intents`

用途：读取前置话术 YAML 源码和加载状态。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `source` | `string` | 否 | 当前 YAML 源码。 | `"version: 1\n..."` |
| `status` | `CustomerIntentsStatus` | 否 | 加载状态。 | `{}` |

#### CustomerIntentsStatus

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `path` | `string` | 否 | YAML 文件路径。 | `"configs/customer_intents.yaml"` |
| `loaded_at` | `ISO-8601 datetime string` | 是 | 最近成功加载时间。 | `"2026-04-25T08:00:00Z"` |
| `error` | `string` | 是 | 当前错误。 | `"invalid yaml"` |
| `warnings` | `array<string>` | 是 | 校验警告。 | `[]` |
| `rule_count` | `number` | 否 | 已加载规则数量。 | `8` |

#### curl

```bash
curl http://127.0.0.1:9025/api/v1/admin/customer-intents
```

### PUT `/api/v1/admin/customer-intents`

用途：保存完整前置话术 YAML。保存前会强校验；校验失败不写文件、不替换内存缓存。

鉴权：无。请将 Admin API 部署在可信网络内，或在反向代理层增加鉴权。

Content-Type：`application/json`

#### Request Body

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `source` | `string` | 是 | 否 | 无 | 完整 YAML 源码。 | 必须是合法 customer intents 配置。 |

Response：同 `GET /api/v1/admin/customer-intents`。

#### curl

```bash
curl -X PUT http://127.0.0.1:9025/api/v1/admin/customer-intents \
  -H 'Content-Type: application/json' \
  -d '{"source":"version: 1\nrules: []\n"}'
```

## 14. 接入建议

- 终端 AI 客服统一使用 `/api/v1/customer/chat`；默认非流式 JSON，需要端侧逐字输出时可传 `stream:true`，但响应仍只包含客户可见内容。
- 终端 AI 客服必须自己维护用户会话，并把最近对话作为 `history` 传入。
- Wikios 内部客户问答测试也使用 `/api/v1/customer/chat`，传 `entrypoint:"internal"`、`simulation:true`，需要流式展示时传 `stream:true`。
- 客户问答审计详情只通过 `GET /api/v1/admin/customer-chat/traces/:trace_id` 读取；客户问答响应体不返回 details、thinking 或 trace_id。
- 后台知识库助手使用 `/api/v1/admin/knowledge/assistant/chat`，默认流式；显式传 `stream:false` 返回非流式 JSON。
- 不要把 Admin 接口暴露给终端客户，因为其中可能包含管理员执行细节、prompt 摘要和 reasoning。
- Admin API 要放在可信网络或加反向代理鉴权；WikiOS 当前不内置后台登录，公网部署必须在反向代理或上游网关增加访问控制。
- Wiki 同步接口会操作外挂 wiki 的 git 仓库，生产环境需要正确配置 remote、branch 和凭据。
