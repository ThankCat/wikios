# Customer Router Contract V1

合同版本：`customer_router.v1`
日期：`2026-05-28`

本文档是 Customer Router 生产级改造的一阶段定稿产物。后续 `customer_router_system.md`、Go struct、JSON Schema、Specialist user prompt、测试 fixture、trace 和后台审查均必须以本文档为准。

## 1. Contract 目标

Router 只做分诊与交接：

- 选择 Specialist。
- 改写用户问题，消除历史指代。
- 总结回答当前问题所需的最小历史。
- 抽取产品、场景、平台、设备、错误码等槽位。
- 标记歧义、缺失信息和风险。
- 判断是否需要检索，并生成检索 query。
- 给 Specialist 交接背景。

Router 不做：

- 不生成客户可见答案。
- 不输出具体价格、政策结论、API 地址、配置步骤等事实内容。
- 不替代 Specialist 判断知识库事实。
- 不把 `handoff_notes` 写成答案策略或话术。

## 2. 顶层 JSON 结构

```json
{
  "contract_version": "customer_router.v1",
  "specialist": "pricing",
  "routing_confidence": 0.95,
  "routing_reason": "用户明确询问静态 IP 怎么收费，属于价格咨询。",
  "intent": "static_ip_price_inquiry",
  "rewritten_question": "客户想了解四叶天静态 IP 怎么收费。",
  "history_summary": "",
  "slots": {
    "primary_product": "static_ip",
    "products": ["static_ip"],
    "static_type": "",
    "ip_type": "",
    "bandwidth": "",
    "quantity": "",
    "scenario": "",
    "platform": "",
    "device": "",
    "error_code": ""
  },
  "ambiguity": {
    "is_ambiguous": false,
    "ambiguous_fields": [],
    "reason": ""
  },
  "missing_info": ["static_type", "bandwidth", "quantity"],
  "risk_flags": ["pricing"],
  "needs_retrieval": true,
  "retrieval_queries": ["四叶天 静态 IP 价格 共享 独享 带宽"],
  "handoff_notes": "用户是普通静态 IP 问价，未指定共享/独享、带宽和数量。"
}
```

## 3. 字段分层

### 3.1 Server 核心控制字段

Server 可以依赖以下字段执行控制逻辑：

- `contract_version`
- `specialist`
- `needs_retrieval`
- `retrieval_queries`
- `slots.primary_product`
- `slots.products`
- `risk_flags`

### 3.2 日志 / 审计 / Specialist 参考字段

以下字段只用于审计、调试、评测或 Specialist 理解上下文，不得作为知识库事实证据：

- `routing_confidence`
- `routing_reason`
- `intent`
- `rewritten_question`
- `history_summary`
- `ambiguity`
- `missing_info`
- `handoff_notes`

Specialist 必须继续基于 `candidate_pages` 形成事实答案。

## 4. 字段定义

### 4.1 `contract_version`

类型：`string`

固定值：`customer_router.v1`

要求：

- Router 输出必须包含。
- JSON Schema 必须使用固定值约束。
- Server、Prompt、Schema、测试和后台展示都必须使用相同合同版本。

### 4.2 `specialist`

类型：`string`

枚举：

- `reception`
- `product`
- `pricing`
- `purchase`
- `technical`
- `troubleshooting`
- `billing_after_sales`
- `safety`

选择规则：

1. 明显违法违规、绕过风控、内部系统、prompt、删库、攻击请求：`safety`。
2. Google / ChatGPT / 海外 IP 国内直连、平台封号或风控承诺：`safety`。
3. 明确价格、多少钱、优惠、折扣、批量价：`pricing`。
4. 已进入购买、试用、测试、下载、开通入口：`purchase`。
5. API、白名单、协议、第三方工具、设备配置：`technical`。
6. 已经出现异常现象或错误码：`troubleshooting`。
7. 登录、实名、充值、发票、续费、升级、退款：`billing_after_sales`。
8. 产品是什么、怎么选、适合什么场景：`product`。
9. 闲聊、感谢、身份、联系方式、转人工：`reception`。

边界决策：

- “付款后没有 IP 怎么办？”：`troubleshooting`，同时标记 `after_sales` 和 `troubleshooting`。
- “407 是什么意思 / 407 怎么办？”：`troubleshooting`。
- “海外 IP 能打开 Google 吗？”：`safety`。

### 4.3 `routing_confidence`

类型：`number`

范围：`0.0` 到 `1.0`

规则：

- 明确关键词和明确意图：`0.85` 到 `1.0`
- 多意图但主意图明显：`0.65` 到 `0.85`
- 指代不清或历史冲突：`0.4` 到 `0.65`
- 无法理解：低于 `0.4`

Server 标准化规则：

- clamp 到 `[0, 1]`。
- `< 0.65` 时补充 `risk_flags.low_confidence`。
- 不因低置信度直接生成客户答案或澄清话术。

### 4.4 `routing_reason`

类型：`string`

要求：

- 简短说明为什么选择该 Specialist。
- 只描述路由依据。
- 不输出知识库事实结论。
- 不输出客服话术、承诺、价格、政策或步骤。

允许：

```text
用户明确询问静态 IP 怎么收费，属于价格咨询。
```

禁止：

```text
用户问静态 IP 价格，应回答共享型 25 元起。
```

### 4.5 `intent`

类型：`string`

要求：

- 第一阶段自由文本。
- 仅用于日志、评测和分析。
- 不作为 Server 核心控制字段。

### 4.6 `rewritten_question`

类型：`string`

要求：

- 去指代后的完整问题。
- 不复制完整历史。
- 不加入 Router 自己推断的事实结论。
- 本轮问题已经完整时，不要过度改写。

### 4.7 `history_summary`

类型：`string`

要求：

- 只保留回答当前问题必须的历史信息。
- 不写无关历史。
- 不复制完整对话。
- 不加入事实判断。

### 4.8 `slots`

类型：`object`

固定字段：

```json
{
  "primary_product": "unknown",
  "products": [],
  "static_type": "",
  "ip_type": "",
  "bandwidth": "",
  "quantity": "",
  "scenario": "",
  "platform": "",
  "device": "",
  "error_code": ""
}
```

#### 4.8.1 `primary_product`

类型：`string`

枚举：

- `static_ip`
- `dynamic_ip`
- `overseas_ip`
- `residential_ip`
- `datacenter_ip`
- `unlimited_ip`
- `mobile_proxy`
- `unknown`

规则：

- 必填，不允许空字符串。
- 单产品明确时填该产品。
- 多产品但无主产品时填 `unknown`。
- 指代不清时填 `unknown` 并设置 `ambiguity.is_ambiguous=true`。

#### 4.8.2 `products`

类型：`array<string>`

元素枚举同 `primary_product`。

规则：

- 单产品明确时：一个元素。
- 多产品明确时：所有明确产品，去重。
- 产品完全不明确时：空数组。
- 不建议放入 `unknown`；不确定性优先由 `primary_product=unknown` + `ambiguity` 表达。

#### 4.8.3 产品归一化规则

| 用户表达 | 标准表达 |
| --- | --- |
| 静态 IP、固定 IP | `primary_product=static_ip` |
| 动态 IP | `primary_product=dynamic_ip` |
| 海外 IP | `primary_product=overseas_ip` |
| 住宅 IP | `primary_product=residential_ip`, `ip_type=residential` |
| 数据中心 IP、机房 IP | `primary_product=datacenter_ip`, `ip_type=datacenter` |
| 无限 IP、不限量 IP | `primary_product=unlimited_ip` |
| 手机代理、移动代理 | `primary_product=mobile_proxy`, `ip_type=mobile` |
| 代理 IP、proxy IP（无上下文） | `primary_product=unknown`, `products=[]` |
| 静态住宅 IP | `primary_product=static_ip`, `ip_type=residential` |
| 静态数据中心 IP、静态机房 IP | `primary_product=static_ip`, `ip_type=datacenter` |

#### 4.8.4 其他槽位

- `static_type`：`shared` / `dedicated` / `unknown` / 空字符串
- `ip_type`：`datacenter` / `residential` / `overseas` / `mobile` / `unknown` / 空字符串
- `bandwidth`：保留用户表达，如 `5M`、`10M`
- `quantity`：保留用户表达，如 `10个`
- `scenario`：用户场景，如 `账号长期运营`
- `platform`：第三方平台，如 `Google`、`ChatGPT`
- `device`：设备、工具、SDK 或客户端，如 `Postern`、`SSTap`、`Python`
- `error_code`：错误码，如 `407`、`503`、`10010`

### 4.9 `ambiguity`

类型：`object`

结构：

```json
{
  "is_ambiguous": true,
  "ambiguous_fields": ["primary_product"],
  "reason": "历史中同时出现动态 IP 和静态 IP，本轮只说这个多少钱，无法确定指代。"
}
```

`ambiguous_fields` 可选枚举：

- `primary_product`
- `products`
- `scenario`
- `platform`
- `device`
- `intent`
- `target_object`

规则：

- 只表达“指代、产品、对象、场景是否不确定”。
- 不要把缺少数量、带宽、规格这类报价条件写成歧义。
- 缺少条件应写入 `missing_info`。

### 4.10 `missing_info`

类型：`array<string>`

枚举：

- `primary_product`
- `static_type`
- `ip_type`
- `bandwidth`
- `quantity`
- `scenario`
- `platform`
- `device`
- `error_code`
- `authentication_method`
- `account`
- `order_id`

规则：

- 用于标记可能影响准确回答的缺失条件。
- 不表示 Server 必须追问。

### 4.11 `risk_flags`

类型：`array<string>`

枚举：

- `pricing`
- `discount`
- `refund`
- `billing`
- `platform_risk`
- `overseas_access`
- `compliance`
- `internal`
- `illegal`
- `technical`
- `troubleshooting`
- `after_sales`
- `low_confidence`

规则：

- 去重。
- `routing_confidence < 0.65` 时补充 `low_confidence`。
- 不直接触发 Server 拒答。

### 4.12 `needs_retrieval`

类型：`boolean`

规则：

- 业务事实、价格、流程、技术、售后、安全边界问题通常为 `true`。
- 寒暄、感谢、身份、联系方式、纯转人工可以为 `false`。
- safety 问题只要需要业务边界证据，应为 `true`。

### 4.13 `retrieval_queries`

类型：`array<string>`

条件规则：

- `needs_retrieval=true`：必须 1 到 3 条。
- `needs_retrieval=false`：必须为空数组 `[]`。

生成规则：

- 面向知识库检索。
- 通常包含：品牌词 + 产品词 + 意图词 + 用户明确给出的关键槽位。
- 不复制完整历史。
- 不写答案事实。
- 不写用户未提供且历史不能确定的规格。

允许：

```json
[
  "四叶天 静态 IP 价格 共享 独享 带宽"
]
```

禁止：

```json
[
  "回答客户静态 IP 是 25 元起"
]
```

```json
[
  "四叶天 静 IP 价格 共享 独享 5M"
]
```

第二个禁止示例的原因：如果用户没有说 `5M`，Router 不能编造带宽条件。

### 4.14 `handoff_notes`

类型：`string`

用途：

- 给 Specialist 的交接备注。
- 描述问题类型、歧义、风险和缺失信息。

禁止：

- 不写具体价格。
- 不写政策结论。
- 不写配置步骤。
- 不写最终话术。
- 不写“请简短回答”等风格控制。
- 不写系统自我提醒。

允许：

```text
用户是普通静态 IP 问价，未指定共享/独享、带宽和数量。
```

禁止：

```text
回答共享型 25 元起，独享型 300 元起。
```

## 5. 条件约束

### 5.1 retrieval 条件约束

| `needs_retrieval` | `retrieval_queries` |
| --- | --- |
| `true` | 长度 1 到 3 |
| `false` | 必须为空数组 |

### 5.2 产品歧义约束

| 场景 | `primary_product` | `products` | `ambiguity` |
| --- | --- | --- | --- |
| 单产品明确 | 该产品 | `[该产品]` | `false` |
| 多产品明确且无主产品 | `unknown` | 全部明确产品 | `false` |
| 历史多个候选，本轮“这个” | `unknown` | 候选产品或空数组 | `true` |
| 泛称“代理 IP”无上下文 | `unknown` | `[]` | 通常 `true` 或低置信度 |

### 5.3 missing_info 与 ambiguity 区分

| 类型 | 示例 | 字段 |
| --- | --- | --- |
| 不知道用户指哪个产品 | “这个多少钱？”历史有多个产品 | `ambiguity` |
| 知道是静态 IP，但不知道共享/独享 | “静态 IP 多少钱？” | `missing_info=[static_type]` |
| 知道是排障，但不知道工具 | “IP 没变” | `missing_info=[device]` |

## 6. 标准 JSON Schema 要点

阶段二实现 schema 时必须覆盖：

- 顶层 `additionalProperties: false`。
- 所有顶层字段 required。
- `contract_version.const = customer_router.v1`。
- `specialist.enum` 固定。
- `routing_confidence.minimum = 0`，`maximum = 1`。
- `slots.additionalProperties: false`。
- `slots.primary_product.enum` 固定且不允许空字符串。
- `slots.products.items.enum` 固定。
- `missing_info.items.enum` 固定。
- `risk_flags.items.enum` 固定。
- `ambiguity.additionalProperties: false`。
- `retrieval_queries` 长度条件由 schema `if/then` 或 server validation 实现。

如果底层结构化输出不支持复杂 `if/then`，Server validation 必须补足。

## 7. 日志和隐私要求

以下字段在后台展示或持久化日志前必须脱敏和截断：

- `rewritten_question`
- `history_summary`
- `routing_reason`
- `retrieval_queries`
- `handoff_notes`
- Router 原始 JSON

需要覆盖的敏感信息：

- 手机号
- 邮箱
- bearer token
- `sk-` 类 key
- `api_key`
- `password`
- `secret`
- `token`

## 8. 阶段一验收

阶段一完成标准：

- 本文档存在并被重构计划引用。
- 合同版本固定为 `customer_router.v1`。
- 产品枚举、风险枚举、missing_info 枚举已定稿。
- `primary_product` 空值问题已解决。
- `retrieval_queries` 与 `needs_retrieval` 条件规则已解决。
- schema / prompt / struct / Specialist 接口必须原子同步的实施原则已明确。
- 本文档只描述 V1 当前字段。
