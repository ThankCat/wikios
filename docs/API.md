# WikiOS API 文档

本文档描述 WikiOS v1 HTTP API。WikiOS 是智能 Wiki 知识库微服务，终端 AI 客服只应对接 Public API；Admin API 面向内置管理后台和可信后台系统。

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
| Public API | 不需要管理员登录。调用方应在自己的 AI 客服系统侧做用户鉴权、限流和风控。 |
| Admin API | 需要管理员会话。先调用 `POST /api/v1/admin/auth/login`，成功后服务端写入 HTTP-only Cookie，同时返回 `token`。后续请求可以使用 Cookie，也可以使用 `Authorization: Bearer <token>` 或 `X-WikiOS-Admin-Session: <token>`。 |

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
| `error.code` | `string` | 否 | 机器可读错误码。 | `"UNAUTHORIZED"` |
| `error.message` | `string` | 否 | 人类可读错误信息。 | `"admin login required"` |

常见错误码：

| 错误码 | HTTP 状态 | 含义 |
| --- | ---: | --- |
| `BAD_REQUEST` | `400` | 请求参数无效、缺失或格式错误。 |
| `UNAUTHORIZED` | `401` | 未登录或管理员 session 无效。 |
| `NOT_FOUND` | `404` | 路由不存在。 |
| `CONTEXT_LIMIT_EXCEEDED` | `413` | 管理员多轮上下文超过限制。 |
| `PUBLIC_INTENTS_UNAVAILABLE` | `503` | 前置话术管理器不可用。 |
| `REVIEWS_UNAVAILABLE` | `503` | 问题审查队列不可用。 |
| `INVALID_PUBLIC_INTENTS` | `400` | 前置话术 YAML 校验失败。 |
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
| `name` | `string` | 否 | 步骤名称。 | `"llm public answer"` |
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

## 5. Public API

### POST `/api/v1/public/answer`

用途：真实 AI 客服系统调用的普通 JSON 问答接口。

鉴权：无。

Content-Type：`application/json`

#### Request Body

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `question` | `string` | 是 | 否 | 无 | 当前用户问题。 | `"我想买5M住宅IP，怎么购买？"` |
| `stream` | `boolean` | 否 | 否 | `false` | 是否使用 SSE 流式返回；不传时为普通 JSON。 | `true` |
| `user_id` | `string` | 否 | 是 | `""` | 调用方用户 ID，用于业务侧追踪；当前服务不强依赖。 | `"u_123"` |
| `session_id` | `string` | 否 | 是 | `""` | 调用方会话 ID，用于业务侧追踪；当前服务不强依赖。 | `"s_456"` |
| `question_message_id` | `string` | 否 | 是 | `""` | 本轮用户消息 ID，用于审查队列和日志追踪。 | `"msg_user_001"` |
| `answer_message_id` | `string` | 否 | 是 | `""` | 调用方预生成的助手消息 ID，用于审查队列和日志追踪。 | `"msg_assistant_001"` |
| `question_created_at` | `ISO-8601 datetime string` | 否 | 是 | `""` | 本轮用户消息创建时间；缺失时服务端使用接收时间。 | `"2026-04-30T14:05:00+08:00"` |
| `context` | `object` | 否 | 是 | `{}` | 调用方扩展上下文。 | `{ "channel": "web" }` |
| `history` | `array<ChatMessage>` | 否 | 是 | `[]` | 最近多轮对话上下文。 | 最近 8 轮以内较合适。 |

多轮要求：如果用户问题省略主语，例如“这个怎么买？”，调用方必须传 `history`，否则检索无法知道“这个”指向住宅 IP、套餐或其他主题。

Public Query 行为说明：服务端只做硬安全拦截、`wiki/forbidden` 命中判断、qmd 检索、候选页面读取和 LLM 调用；普通问题的语义理解、证据/自答/澄清/拒答选择由 `public_answer_system.md` 驱动。候选页面只会来自 public-safe 目录，`wiki/unconfirmed`、`wiki/forbidden`、模板和 outputs 不会作为正式证据传给 LLM。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `answer` | `string` | 否 | 给终端客户展示的答案。不会返回内部路径、prompt 或 raw JSON。 | `"您可以选择住宅IP的5M带宽套餐后，在官网下单购买。"` |
| `received_at` | `ISO-8601 datetime string` | 是 | 服务端接收本轮问题的时间。 | `"2026-04-30T06:05:00Z"` |
| `answered_at` | `ISO-8601 datetime string` | 是 | 服务端生成完成答案的时间。 | `"2026-04-30T06:05:03Z"` |
| `user_intent` | `PublicUserIntent \| null` | 是 | 用户业务意图。无申请改价或切换 IP 意图时固定为 `null`。 | `{ "type": "price_adjustment", "price_info": {...} }` |
| `details` | `object` | 是 | 安全处理后的思考过程和执行摘要；不包含 raw prompt。 | `{ "reasoning": "..." }` |

`PublicUserIntent`：

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `type` | `enum` | 否 | `price_adjustment` 表示申请改价/优惠；`switch_ip` 表示切换 IP。 | `"price_adjustment"` |
| `price_info` | `PublicPriceInfo` | 是 | 仅 `type=price_adjustment` 时返回。 | `{ "expected_price": "90元/个" }` |

`PublicPriceInfo`：

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `expected_price` | `string` | 否 | 知识库中对应套餐的可申请优惠价，保留币种和单位。 | `"90元/个"` |
| `product_type` | `enum` | 否 | `static`、`dynamic`、`box`；住宅 IP 使用 `box`。 | `"static"` |
| `product_bandwidth` | `integer` | 否 | 仅 `static`、`box` 使用，取值 `5`、`10`、`20`；不适用为 `0`。 | `5` |
| `intended_purchase_quantity` | `integer` | 否 | 仅 `static`、`box` 使用；不适用为 `0`。 | `10` |
| `box_usage_time` | `integer` | 否 | 仅 `dynamic` 包时套餐使用，取值 `7`、`30`、`90`、`180`、`360`；不适用为 `0`。 | `30` |
| `box_usage_quantity_min` | `integer` | 否 | 仅 `dynamic` 包量套餐使用；不适用为 `0`。 | `1000` |
| `box_usage_quantity_max` | `integer` | 否 | 仅 `dynamic` 包量套餐使用；不适用为 `0`。 | `5000` |

#### curl

```bash
curl -X POST http://127.0.0.1:9025/api/v1/public/answer \
  -H 'Content-Type: application/json' \
  -d '{
    "question": "这个怎么买？",
    "session_id": "s_456",
    "question_message_id": "msg_user_001",
    "answer_message_id": "msg_assistant_001",
    "question_created_at": "2026-04-30T14:05:00+08:00",
    "history": [
      { "id": "msg_user_000", "role": "user", "content": "住宅IP套餐都有什么？", "created_at": "2026-04-30T14:04:30+08:00" },
      { "id": "msg_assistant_000", "role": "assistant", "content": "住宅IP通常有5M、10M、20M等带宽。", "created_at": "2026-04-30T14:04:32+08:00" }
    ]
  }'
```

### POST `/api/v1/public/answer/stream`

用途：兼容旧调用方的流式问答接口，用于打字机效果。新调用方也可以直接向 `POST /api/v1/public/answer` 传 `stream:true` 获取同样的 SSE 响应。

鉴权：无。

Content-Type：`application/json`

Response Content-Type：`text/event-stream`

Request Body：同 `POST /api/v1/public/answer`；此兼容入口会强制按流式处理。

#### SSE 事件

| 事件 | 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- | --- |
| `meta` | `data.stream` | `boolean` | 否 | 表示当前为流式响应。 | `true` |
| `meta` | `data.received_at` | `ISO-8601 datetime string` | 否 | 服务端接收本轮问题的时间。 | `"2026-04-30T06:05:00Z"` |
| `delta` | `data.delta` | `string` | 否 | 答案增量文本。 | `"您可以选择"` |
| `llm_reasoning_delta` | `data.delta` | `string` | 是 | 安全处理后的过程说明增量。 | `"正在检索公开证据"` |
| `step_finish` | `data` | `object` | 是 | 安全处理后的执行步骤摘要。 | `{ "name": "检索公开证据" }` |
| `result` | `data.answer` | `string` | 否 | 完整答案。 | `"您可以选择住宅IP..."` |
| `result` | `data.answered_at` | `ISO-8601 datetime string` | 否 | 服务端生成完成答案的时间。 | `"2026-04-30T06:05:03Z"` |
| `result` | `data.user_intent` | `PublicUserIntent \| null` | 是 | 同普通 JSON 响应中的 `user_intent`。 | `null` |
| `error` | `data.message` | `string` | 否 | 错误信息。 | `"llm request timeout after 90s"` |
| `done` | `data.ok` | `boolean` | 否 | 是否成功结束。 | `true` |

#### curl

```bash
curl -N -X POST http://127.0.0.1:9025/api/v1/public/answer \
  -H 'Content-Type: application/json' \
  -d '{"question":"住宅IP怎么购买？","history":[],"stream":true}'
```

## 6. Admin Auth API

### POST `/api/v1/admin/auth/login`

用途：管理员登录。

鉴权：无。

Content-Type：`application/json`

#### Request Body

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `username` | `string` | 是 | 否 | 无 | 管理员用户名。 | `"admin"` |
| `password` | `string` | 是 | 否 | 无 | 管理员密码。生产环境不能使用默认密码。 | `"your-secure-password"` |

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `token` | `string` | 否 | 管理员会话 token。管理后台默认依赖 HTTP-only Cookie；外部集成在 Cookie 不可用时可通过 `Authorization: Bearer <token>` 传回。 | `"sess_..."` |
| `expires_at` | `ISO-8601 datetime string` | 否 | 会话过期时间。 | `"2026-05-07T06:05:00Z"` |
| `user.id` | `string` | 否 | 管理员用户 ID。 | `"1"` |
| `user.username` | `string` | 否 | 管理员用户名。 | `"admin"` |

成功后服务端写入 HTTP-only Cookie，默认名称为 `wikios_admin_session`。WikiOS 管理后台不把 token 写入 `localStorage` 或 `sessionStorage`；如果外部系统需要对接 Admin API，可以在自己的安全存储策略下使用响应里的 `token`。

#### curl

```bash
curl -c cookie.txt -X POST http://127.0.0.1:9025/api/v1/admin/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"your-secure-password"}'
```

### POST `/api/v1/admin/auth/logout`

用途：管理员退出登录。

鉴权：管理员 Cookie 或 Bearer token。

Content-Type：无请求体。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `ok` | `boolean` | 否 | 是否退出成功。 | `true` |

#### curl

```bash
curl -b cookie.txt -X POST http://127.0.0.1:9025/api/v1/admin/auth/logout
```

### GET `/api/v1/admin/auth/me`

用途：获取当前登录管理员。

鉴权：管理员 Cookie 或 Bearer token。

Content-Type：无请求体。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `user.id` | `string` | 否 | 管理员用户 ID。 | `"1"` |
| `user.username` | `string` | 否 | 管理员用户名。 | `"admin"` |

未登录返回 `401 UNAUTHORIZED`。

#### curl

```bash
curl -b cookie.txt http://127.0.0.1:9025/api/v1/admin/auth/me
```

Bearer token 示例：

```bash
curl -H "Authorization: Bearer <token>" http://127.0.0.1:9025/api/v1/admin/auth/me
```

## 7. Admin Chat API

### POST `/api/v1/admin/chat`

用途：管理员非流式对话和 Wiki 治理操作。

鉴权：管理员 Cookie 或 Bearer token。

Content-Type：`application/json`

#### Request Body

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `message` | `string` | 是 | 否 | 无 | 管理员指令或问题。 | `"执行一次健康检查"` |
| `stream` | `boolean` | 否 | 否 | `false` | 前端展示偏好；非流式接口通常传 `false`。 | `false` |
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
curl -b cookie.txt -X POST http://127.0.0.1:9025/api/v1/admin/chat \
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

### POST `/api/v1/admin/chat/stream`

用途：管理员流式对话和 Wiki 治理操作。

鉴权：管理员 Cookie 或 Bearer token。

Content-Type：`application/json`

Response Content-Type：`text/event-stream`

Request Body：同 `POST /api/v1/admin/chat`。

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

#### curl

```bash
curl -N -b cookie.txt -X POST http://127.0.0.1:9025/api/v1/admin/chat/stream \
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

鉴权：管理员 Cookie 或 Bearer token。

Content-Type：`application/json`

Request Body：同 `POST /api/v1/admin/chat`。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `mode` | `string` | 否 | 识别出的执行模式。 | `"query"` |
| `context_usage` | `ContextUsage` | 否 | 上下文占用。 | `{ "blocked": false }` |

若上下文超过限制，`/api/v1/admin/chat` 返回 HTTP `413` 和 `CONTEXT_LIMIT_EXCEEDED`。

#### curl

```bash
curl -b cookie.txt -X POST http://127.0.0.1:9025/api/v1/admin/context/estimate \
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

鉴权：管理员 Cookie 或 Bearer token。

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
curl -b cookie.txt -X POST http://127.0.0.1:9025/api/v1/admin/upload \
  -F 'file=@产品知识.md'
```

### POST `/api/v1/admin/upload/stream`

用途：上传文档并流式返回摄入过程。

鉴权：管理员 Cookie 或 Bearer token。

Content-Type：`multipart/form-data`

Response Content-Type：`text/event-stream`

Form 参数：同 `POST /api/v1/admin/upload`。

SSE 事件：同管理员执行流，常见事件包括 `meta`、`prompt`、`llm_reasoning_delta`、`llm_delta`、`step_start`、`step_finish`、`result`、`error`、`keepalive`、`done`。上传流不返回结构化分段或分类事件。

#### curl

```bash
curl -N -b cookie.txt -X POST http://127.0.0.1:9025/api/v1/admin/upload/stream \
  -F 'file=@产品知识.md'
```

## 9. Wiki Browser API

### GET `/api/v1/admin/wiki/tree`

用途：查看外挂 Wiki 目录。

鉴权：管理员 Cookie 或 Bearer token。

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
curl -b cookie.txt 'http://127.0.0.1:9025/api/v1/admin/wiki/tree?path=wiki%2Fknowledge'
```

### GET `/api/v1/admin/wiki/file`

用途：在线查看 Wiki 文件。

鉴权：管理员 Cookie 或 Bearer token。

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
curl -b cookie.txt 'http://127.0.0.1:9025/api/v1/admin/wiki/file?path=wiki%2Fknowledge%2Fstatic-ip.md'
```

### GET `/api/v1/admin/wiki/download`

用途：下载 Wiki 文件。

鉴权：管理员 Cookie 或 Bearer token。

#### Query 参数

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `path` | `string` | 是 | 否 | 无 | 相对 mounted wiki 根目录的文件路径。 | `"raw/files/demo.pdf"` |

Response：文件流，不返回 JSON。

路径限制：所有 Wiki Browser API 只能访问 mounted wiki 根目录；禁止 `.git/`；禁止 `../` 路径穿越。

#### curl

```bash
curl -b cookie.txt -o demo.pdf 'http://127.0.0.1:9025/api/v1/admin/wiki/download?path=raw%2Ffiles%2Fdemo.pdf'
```

## 10. Sync API

### GET `/api/v1/admin/sync/status`

用途：查看外挂 Wiki git 状态、待提交文件和待推送提交。

鉴权：管理员 Cookie 或 Bearer token。

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
| `commits_to_push` | `array<SyncCommitInfo>` | 否 | 待推送提交。 | `[]` |
| `recent_commits` | `array<SyncCommitInfo>` | 否 | 最近提交。 | `[]` |
| `files` | `array<SyncStatusFile>` | 否 | 待提交文件。 | `[]` |

#### curl

```bash
curl -b cookie.txt http://127.0.0.1:9025/api/v1/admin/sync/status
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

鉴权：管理员 Cookie 或 Bearer token。

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
curl -b cookie.txt -X POST http://127.0.0.1:9025/api/v1/admin/sync/generate-message \
  -H 'Content-Type: application/json' \
  -d '{"paths":["wiki/knowledge/static-ip.md"]}'
```

### POST `/api/v1/admin/sync/commit`

用途：提交选择的 Wiki 文件。

鉴权：管理员 Cookie 或 Bearer token。

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
curl -b cookie.txt -X POST http://127.0.0.1:9025/api/v1/admin/sync/commit \
  -H 'Content-Type: application/json' \
  -d '{
    "paths": ["wiki/knowledge/static-ip.md"],
    "message": "docs: update static ip knowledge"
  }'
```

### POST `/api/v1/admin/sync/push`

用途：推送当前分支到远端。

鉴权：管理员 Cookie 或 Bearer token。

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
curl -b cookie.txt -X POST http://127.0.0.1:9025/api/v1/admin/sync/push \
  -H 'Content-Type: application/json' \
  -d '{"remote":"origin","branch":"main"}'
```

## 11. LLM Model API

用途：管理 OpenAI-compatible 模型，并切换 WikiOS 全站当前启用模型。所有接口都需要管理员 Cookie 或 Bearer token。API Key 保存到 SQLite，但响应中只返回 `has_api_key` 和 `api_key_mask`，不会回显完整密钥。

### Endpoints

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `GET` | `/api/v1/admin/models` | 获取模型列表。 |
| `POST` | `/api/v1/admin/models` | 新增模型。 |
| `GET` | `/api/v1/admin/models/:id` | 获取单个模型。 |
| `PUT` | `/api/v1/admin/models/:id` | 更新模型，`api_key` 留空时保留原密钥。 |
| `DELETE` | `/api/v1/admin/models/:id` | 删除模型；删除当前模型后全站进入未启用模型状态。 |
| `POST` | `/api/v1/admin/models/:id/activate` | 切换当前启用模型。 |

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
curl -b cookie.txt -X POST http://127.0.0.1:9025/api/v1/admin/models \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"生产客服模型","provider":"openai-compatible","base_url":"https://api.example.com/v1","model_name":"gpt-compatible-chat","api_key":"sk-xxx","timeout_sec":90,"admin_timeout_sec":300}'
```

## 12. Review API

审查队列以 mounted wiki 文件为事实来源：`wiki/unconfirmed/` 保存待审问题，`wiki/forbidden/` 保存驳回后的禁答问题。所有接口都需要管理员 Cookie 或 Bearer token。

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

## 13. Public Intents API

### GET `/api/v1/admin/public-intents`

用途：读取前置话术 YAML 源码和加载状态。

鉴权：管理员 Cookie 或 Bearer token。

#### Response

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `source` | `string` | 否 | 当前 YAML 源码。 | `"version: 1\n..."` |
| `status` | `PublicIntentsStatus` | 否 | 加载状态。 | `{}` |

#### PublicIntentsStatus

| 字段 | 类型 | 可为空 | 含义 | 示例 |
| --- | --- | --- | --- | --- |
| `path` | `string` | 否 | YAML 文件路径。 | `"configs/public_intents.yaml"` |
| `loaded_at` | `ISO-8601 datetime string` | 是 | 最近成功加载时间。 | `"2026-04-25T08:00:00Z"` |
| `error` | `string` | 是 | 当前错误。 | `"invalid yaml"` |
| `warnings` | `array<string>` | 是 | 校验警告。 | `[]` |
| `rule_count` | `number` | 否 | 已加载规则数量。 | `8` |

#### curl

```bash
curl -b cookie.txt http://127.0.0.1:9025/api/v1/admin/public-intents
```

### PUT `/api/v1/admin/public-intents`

用途：保存完整前置话术 YAML。保存前会强校验；校验失败不写文件、不替换内存缓存。

鉴权：管理员 Cookie 或 Bearer token。

Content-Type：`application/json`

#### Request Body

| 字段 | 类型 | 必填 | 可为空 | 默认值 | 含义 | 约束/示例 |
| --- | --- | --- | --- | --- | --- | --- |
| `source` | `string` | 是 | 否 | 无 | 完整 YAML 源码。 | 必须是合法 public intents 配置。 |

Response：同 `GET /api/v1/admin/public-intents`。

#### curl

```bash
curl -b cookie.txt -X PUT http://127.0.0.1:9025/api/v1/admin/public-intents \
  -H 'Content-Type: application/json' \
  -d '{"source":"version: 1\nrules: []\n"}'
```

## 14. 接入建议

- 终端 AI 客服优先使用 `/api/v1/public/answer`；需要流式时传 `stream:true`，旧的 `/api/v1/public/answer/stream` 仅作为兼容入口保留。
- 终端 AI 客服必须自己维护用户会话，并把最近对话作为 `history` 传入。
- 不要把 Admin SSE 暴露给终端客户，因为其中包含管理员执行细节和 reasoning。
- Admin API 要放在可信网络或加反向代理鉴权；默认 Cookie session 只解决后台登录，不等同于公网安全策略。
- Wiki 同步接口会操作外挂 wiki 的 git 仓库，生产环境需要正确配置 remote、branch 和凭据。
