# 四叶天客服机器人 · 对接文档

本文档面向接入方（搭档），用于把四叶天智能客服接入你自己的机器人 / 前端。读完本文即可完成对接，无需阅读完整的 `docs/API.md`。

- 你只需要对接 **一个接口**：`POST /api/v1/customer/chat`。
- 每次把「客户本轮消息 + 最近几轮历史」发给它，拿到「要展示给客户的答案 + 一组结构化元数据（含业务意图 `user_intent`）」。
- 客户可见正文永远是响应里的 `answer` 字段；`user_intent` 等字段是给你做自动化路由的辅助信号。

---

## 1. 基本信息

| 项 | 值 |
| --- | --- |
| 协议 | HTTP / HTTPS |
| 方法 | `POST` |
| 路径 | `/api/v1/customer/chat` |
| 请求 `Content-Type` | `application/json` |
| 字符编码 | UTF-8 |
| 鉴权 | 接口本身**不鉴权**。用户鉴权、限流、风控由你的业务系统负责。 |
| 本地联调地址 | `http://127.0.0.1:9025` |
| 生产地址 | `https://<由我方提供的域名>` |

> 建议：把这个接口放在你自己的后端转发，不要在浏览器里直接暴露我方地址，便于你做鉴权和限流。

---

## 2. 请求

### 2.1 请求字段

| 字段 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `message` | `string` | 是 | — | 客户本轮原始消息。 |
| `history` | `array<ChatMessage>` | 否 | `[]` | 最近多轮对话上下文，按时间从旧到新。只传必要历史即可。 |
| `session_id` | `string` | 否 | `""` | 你侧的会话 ID，建议传入以串联多轮、便于排查。 |
| `user_id` | `string` | 否 | `""` | 你侧的用户 ID。 |
| `message_id` | `string` | 否 | `""` | 本轮客户消息 ID。 |
| `answer_message_id` | `string` | 否 | `""` | 你预生成的助手消息 ID。 |
| `message_created_at` | ISO-8601 | 否 | `""` | 本轮消息创建时间，如 `"2026-06-08T10:00:00+08:00"`。 |
| `context` | `object` | 否 | `{}` | 自定义扩展上下文，如 `{ "channel": "web" }`。 |
| `client_channel` | `enum` | 否 | `"web"` | 客户端渠道。可选 `"web"` / `"mobile_app"`；也可放在 `context.client_channel` 中。 |
| `stream` | `boolean` | 否 | `false` | 是否使用 SSE 流式返回（见第 6 节）。 |
| `entrypoint` | `enum` | 否 | `"external"` | 调用来源，生产固定传 `"external"`。 |
| `simulation` | `boolean` | 否 | `false` | 是否为内部测试。生产必须传 `false`（或不传）。`true` 会跳过人工复核入队等副作用。 |

### 2.2 ChatMessage 结构（`history` 的元素）

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `role` | `enum` | 是 | 仅 `"user"` 或 `"assistant"`。 |
| `content` | `string` | 是 | 消息正文。 |
| `id` | `string` | 否 | 你侧消息 ID。 |
| `created_at` | ISO-8601 | 否 | 消息创建时间，建议传入以便排序。 |

### 2.3 多轮要求（重要）

当客户消息省略主语时（例如「这个怎么买？」「换一个呢？」「静态IP」这种只补一个槽位的短答），**必须**带上 `history`，否则服务端无法正确理解指代，可能答非所问。

多轮对接建议：

- 同一个客户会话使用同一个 `session_id`。
- 每一轮请求都把前面必要的用户/助手消息按旧→新放进 `history`。
- `history` 只放客户可见内容，也就是你上一轮真正展示给客户的 `answer`。
- 不要把 Router、trace、内部审计、prompt、知识库路径等内部信息放进 `history`。
- 如果客户从一个话题切到另一个话题，仍然传最近历史；服务端会以本轮消息为主，必要时继承产品、数量、场景等上下文。

示例：客户先问价格，再用「这个怎么买」追问购买入口。

```json
{
  "message": "这个怎么买",
  "session_id": "s_456",
  "entrypoint": "external",
  "history": [
    { "role": "user", "content": "静态 IP 5M 多少钱" },
    { "role": "assistant", "content": "静态 IP 5M 数据中心共享型原价为 25 元/个/月，独享型原价为 300 元/个/月。请告诉我您需要购买的数量，以便核算对应折扣价。" }
  ]
}
```

示例：客户从平台归属地选型切到排障。

```json
{
  "message": "我连上了，但是抖音还是显示本地",
  "session_id": "s_789",
  "entrypoint": "external",
  "history": [
    { "role": "user", "content": "改抖音 IP 归属地应该买哪个" },
    { "role": "assistant", "content": "改抖音 IP 归属地这类场景，更建议先看静态 IP；要相对稳定城市出口可看数据中心静态 IP，想更贴近家庭宽带场景可看住宅 IP。平台显示可能会有延迟，也会受平台 IP 库影响。" }
  ]
}
```

### 2.4 手机 App 渠道

如果请求来自手机 App，请传：

```json
{ "client_channel": "mobile_app" }
```

或：

```json
{ "context": { "client_channel": "mobile_app" } }
```

`mobile_app` 渠道会按 App 端可见能力收窄回答范围，例如只回答 App 内可操作的安装、登录、购买、连接、VPN 权限、归属地延迟和使用排障。普通网页或客服后台调用不传即可，默认是 `web`。

---

## 3. 响应（非流式）

HTTP `200`，响应头含 `X-Trace-ID`（可用于我方后台定位该轮审计记录）。

| 字段 | 类型 | 是否恒返回 | 说明 |
| --- | --- | --- | --- |
| `answer` | `string` | 是 | **唯一要展示给客户的正文**。可能含 Markdown（列表、表格、代码块）。 |
| `answer_mode` | `string` | 是 | 本轮回答性质，见第 5 节。 |
| `review_required` | `boolean` | 是 | 模型是否建议本轮进入人工复核（供我方后台使用，你侧通常无需处理）。 |
| `source_count` | `number` | 是 | 本轮引用的知识来源数量（≥0）。 |
| `user_intent` | `object \| null` | 是 | 本轮识别到的业务意图；**无意图时恒为 `null`**。见第 4 节。 |
| `received_at` | ISO-8601 | 是 | 服务端接收时间。 |
| `answered_at` | ISO-8601 | 是 | 服务端完成时间。 |

> 响应体不会返回内部链路（Router/检索/Specialist/Prompt/thinking 等）。需要审计详情时用 `X-Trace-ID` 找我方后台。

完整响应示例：

```json
{
  "answer": "数据中心 IP 批量采购可以走商务报价，具体折扣以最终核算为准。",
  "answer_mode": "evidence",
  "review_required": false,
  "source_count": 1,
  "user_intent": {
    "type": "discount",
    "extra": { "product_type": "datacenter_ip", "quantity": 1000 }
  },
  "received_at": "2026-06-08T02:00:00Z",
  "answered_at": "2026-06-08T02:00:03Z"
}
```

---

## 4. `user_intent` 业务意图（对接重点）

`user_intent` 是给你做自动化路由的结构化信号。规则：

- 该字段**每轮都会返回**；本轮没有任何明确意图时为 `null`。
- 每轮**最多一个**意图；多个条件同时满足时按优先级取一个：`refund > discount > switch_ip > wecom`。
- `answer` 始终是要发给客户的主体内容；`user_intent` 只是额外的处理钩子，不要用它替代 `answer`。

### 4.1 四种意图

| `type` | 含义 | 触发条件 | `extra` | 建议机器人动作（你自行决定） |
| --- | --- | --- | --- | --- |
| `wecom` | 加企业微信/转人工 | 客户想联系人工，且明确要微信/企业微信作为联系方式 | 无 | 推送企业微信二维码/客服联系方式，或转人工入口 |
| `refund` | 退款 | 客户有强烈、明确的退款意愿（仅抱怨不算） | 无 | 引导进入退款/售后流程，或转人工售后 |
| `switch_ip` | 切换 IP | 客户想换成一个不同的 IP，且产品不是动态 IP | 无 | `answer` 已含切换指引，可直接展示；也可附帮助卡片 |
| `discount` | 申请优惠 | 客户有强烈优惠意愿，且产品明确（非动态 IP），且有可解析的数量 | `{ product_type, quantity }` | 触发商务报价/优惠流程，并把 `product_type` 和 `quantity` 带给销售 |

### 4.2 `extra` 结构（仅 `discount` 出现）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `product_type` | `string` | 产品枚举：`static_ip` / `dynamic_ip` / `overseas_ip` / `residential_ip` / `datacenter_ip` / `unlimited_ip` / `mobile_proxy`。 |
| `quantity` | `number` | 客户表达的预购数量（正整数）。 |

非 `discount` 意图不带 `extra`（字段缺省或为 `null`）。

### 4.3 处理伪代码

```ts
const data = await callCustomerChat(payload);

// 始终先把答案展示给客户
renderAssistantMessage(data.answer);

// 再根据业务意图做自动化（可选）
switch (data.user_intent?.type) {
  case "wecom":
    showWecomContactCard();
    break;
  case "refund":
    routeToRefundFlow();
    break;
  case "switch_ip":
    // answer 已含切换指引，这里可附加帮助卡片
    showSwitchIpHelp();
    break;
  case "discount":
    const { product_type, quantity } = data.user_intent.extra ?? {};
    notifySales({ product_type, quantity });
    break;
  default:
    // user_intent === null，无需特殊处理
    break;
}
```

---

## 5. `answer_mode` 说明

| 值 | 含义 | 你侧处理建议 |
| --- | --- | --- |
| `evidence` | 主要事实都有知识库依据 | 正常展示 |
| `mixed` | 部分有依据、部分为通用引导 | 正常展示 |
| `self_answer` | 不依赖知识库即可作答（寒暄、联系方式等） | 正常展示 |
| `clarification` | 本轮在向客户追问以澄清 | 正常展示（`answer` 本身就是一句反问） |
| `refusal` | 合规拒答/不能承诺 | 正常展示 |

所有 `answer_mode` 下都直接展示 `answer` 即可；该字段只是让你知道这轮答案的性质，便于做埋点或样式区分。

---

## 6. 流式响应（SSE）

请求体里传 `"stream": true`，响应为 `text/event-stream`，仅输出客户可见事件：

| 事件 `event` | `data` 字段 | 说明 |
| --- | --- | --- |
| `delta` | `data.delta` (`string`) | 答案增量文本，逐段拼接展示。 |
| `result` | `data.answer` (`string`) | 完整答案。 |
| `result` | `data.answer_mode` / `data.review_required` / `data.source_count` / `data.user_intent` | 同非流式响应体，含义见第 3、4 节。 |
| `result` | `data.received_at` / `data.answered_at` | 接收/完成时间。 |
| `done` | `data.ok` (`boolean`) | 是否成功结束。 |

典型顺序：多个 `delta` → 一个 `result`（拿到完整 `answer` 和 `user_intent`）→ 一个 `done`。

---

## 7. 错误与超时

### 7.1 统一错误结构

非 2xx 时返回：

```json
{ "error": { "code": "BAD_REQUEST", "message": "message is required" } }
```

| 错误码 | HTTP | 含义 |
| --- | ---: | --- |
| `BAD_REQUEST` | 400 | 请求参数无效/缺失/格式错误。 |
| `NOT_FOUND` | 404 | 路由不存在。 |
| `INTERNAL_ERROR` | 500 | 服务端内部错误。 |

### 7.2 超时兜底（注意）

当服务端处理超时，接口**仍返回 HTTP `200`**，但 `answer` 为兜底文案：

```
当前在线回复暂时不可用，请稍后再试。
```

此时其它字段为默认值（`user_intent` 为 `null`）。你侧按正常成功响应展示即可，无需特殊报错。

---

## 8. 调用示例

### 8.1 curl（非流式）

```bash
curl -i -X POST http://127.0.0.1:9025/api/v1/customer/chat \
  -H 'Content-Type: application/json' \
  -d '{
    "message": "数据中心IP我想买1000个，能不能给我优惠？",
    "session_id": "s_456",
    "user_id": "u_123",
    "message_id": "msg_user_001",
    "answer_message_id": "msg_assistant_001",
    "client_channel": "web",
    "entrypoint": "external",
    "history": [
      { "role": "user", "content": "你们有数据中心IP吗？" },
      { "role": "assistant", "content": "有的，数据中心 IP 提供固定出口。" }
    ]
  }'
```

### 8.2 curl（流式）

```bash
curl -N -X POST http://127.0.0.1:9025/api/v1/customer/chat \
  -H 'Content-Type: application/json' \
  -d '{ "message": "怎么切换静态IP？", "session_id": "s_456", "stream": true, "entrypoint": "external" }'
```

### 8.3 TypeScript（非流式 fetch）

```ts
type UserIntent =
  | { type: "wecom" | "refund" | "switch_ip" }
  | { type: "discount"; extra: { product_type: string; quantity: number } };

type CustomerChatResponse = {
  answer: string;
  answer_mode: "evidence" | "mixed" | "self_answer" | "clarification" | "refusal";
  review_required: boolean;
  source_count: number;
  user_intent: UserIntent | null;
  received_at: string;
  answered_at: string;
};

async function askBot(message: string, history: { role: "user" | "assistant"; content: string }[]) {
  const res = await fetch("https://<your-host>/api/v1/customer/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ message, history, session_id: "s_456", client_channel: "web", entrypoint: "external" }),
  });
  if (!res.ok) {
    const err = await res.json();
    throw new Error(`${err.error?.code}: ${err.error?.message}`);
  }
  const data: CustomerChatResponse = await res.json();
  return data;
}
```

---

## 9. 当前验证状态

截至 2026-06-15，Customer Chat 已完成一轮多轮高风险 smoke 回归：

| 批次 | Case | Turn | 通过率 | Guard Regression |
| --- | ---: | ---: | ---: | ---: |
| `multi-turn-smoke-high-risk-r11-full-2026-06-15` | 8/8 | 26/26 | 100% | 0 |

覆盖的高风险链路包括：

- 价格 → 购买 → 售后 → 企业微信联系方式，不把“加微信”误判成“微信支付”。
- 平台归属地选型 → “抖音显示本地”排障，能切到 troubleshooting 并说明平台 IP 库、缓存和延迟。
- 正常选型 → 平台风控边界 → 回到固定城市出口/购买，不让安全边界污染后续正常咨询。
- 通用“能改 IP 不”会先追问哪类产品，不直接给错误教程。

结果报告：

- [2026-06-15-multi-turn-high-risk-r11-full.md](/Users/chenhao/Project/wikios/docs/customer-chat-test-tasks/results/2026-06-15-multi-turn-high-risk-r11-full.md)
- [CUSTOMER_CHAT_MULTI_TURN_TEST_PLAN.md](/Users/chenhao/Project/wikios/docs/CUSTOMER_CHAT_MULTI_TURN_TEST_PLAN.md)

---

## 10. 对接清单

- [ ] 通过你自己的后端转发到 `POST /api/v1/customer/chat`，并做用户鉴权/限流。
- [ ] 多轮场景务必回传 `history`（按旧→新）。
- [ ] 同一个客户会话保持同一个 `session_id`。
- [ ] 手机 App 请求传 `client_channel="mobile_app"`；其它渠道默认 `web`。
- [ ] 生产环境 `entrypoint="external"`、`simulation=false`。
- [ ] 始终展示 `answer`；其余字段按需使用。
- [ ] 处理 `user_intent`（`null` 表示无意图；`discount` 读取 `extra.product_type` / `extra.quantity`）。
- [ ] 超时兜底文案按正常 200 处理。
- [ ] 如需流式，传 `stream=true` 并按 `delta` / `result` / `done` 解析。

如需更细的接口字段或后台审计接口，参见仓库内 `docs/API.md`。
