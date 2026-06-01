# Customer Chat 审计 JSONL 改造计划

## 1. 背景

Customer Chat 当前已经收敛为单一链路：

```text
用户问题
  ↓
Router：理解意图、选择 Specialist、生成 retrieval_queries
  ↓
Retrieval：服务层按 Router 输出执行受控检索、过滤、缓存
  ↓
Specialist：基于 Router JSON + candidate_pages 输出结构化答案
  ↓
Final：返回客户可见答案
```

为了后续审计、问题复盘、回归测试、知识库改进和人工标注，需要把每一轮 Customer Chat 的完整链路保存为 JSONL。

本方案只定义第一版审计 JSONL 的落地结构与边界，不涉及模型微调、自动学习、训练数据清洗等后续能力。

---

## 2. 目标

### 2.1 核心目标

- 每一轮 Customer Chat 保存一行 JSONL。
- 能完整复盘一次回答是如何产生的。
- 能清楚看到：
  - 用户问了什么。
  - Router 输出了什么 JSON。
  - 服务层实际检索了什么。
  - Specialist 输入和输出是什么。
  - 最终返回给客户的答案是什么。
  - 每个阶段耗时多少。
  - 模型 thinking 开启时的完整思考内容是什么。
- 为后续后台审查、自学习数据沉淀、回归测试集生成提供基础数据。

### 2.2 非目标

第一版不做：

- 不做模型微调。
- 不做自动改知识库。
- 不做自动学习闭环。
- 不做持久化向量库。
- 不把 JSONL 当作直接训练数据。
- 不让 Specialist 直接操作 shell 或直接读取文件系统。
- 不改变当前 Router + Retrieval + Specialist 的职责边界。

---

## 3. 文件形态

### 3.1 存储格式

采用 JSONL：

```text
一轮问答 = 一行 JSON
```

示例：

```jsonl
{"schema_version":"customer_chat_audit.v1","record_type":"customer_chat_trace","trace_id":"trace_xxx"}
{"schema_version":"customer_chat_audit.v1","record_type":"customer_chat_trace","trace_id":"trace_yyy"}
```

### 3.2 建议文件路径

第一版按现有 workspace 下的 `customer_chat_logs` 目录按日期拆分：

```text
<workspace>/customer_chat_logs/2026-05-28.jsonl
```

后续可按环境或租户扩展：

```text
<workspace>/customer_chat_logs/local/2026-05-28.jsonl
<workspace>/customer_chat_logs/prod/2026-05-28.jsonl
```

---

## 4. 顶级字段

第一版固定以下顶级字段：

```json
{
  "schema_version": "customer_chat_audit.v1",
  "record_type": "customer_chat_trace",
  "trace_id": "trace_xxx",
  "session_id": "xxx",

  "time": {},
  "runtime": {},
  "request": {},
  "router": {},
  "retrieval": {},
  "specialist": {},
  "final": {},
  "error": null,
  "review": {}
}
```

第一版 JSONL 顶层只允许以上标准字段。客户问题、最终答案、耗时、模型输出等信息必须放在对应嵌套对象中，不再额外写入 `question`、`answer`、`answer_mode`、`message`、`logged_at`、`received_at`、`answered_at` 等兼容性顶层字段。

字段职责：

| 顶级字段 | 职责 |
| --- | --- |
| `schema_version` | 审计 JSONL schema 版本 |
| `record_type` | 当前记录类型，第一版固定为 `customer_chat_trace` |
| `trace_id` | 一次请求的全链路追踪 ID |
| `session_id` | 会话 ID，用于串联多轮对话 |
| `time` | 请求接收、完成、总耗时等时间信息 |
| `runtime` | 运行环境、模型配置、代码版本等快照 |
| `request` | 用户本轮输入和完整对话上下文，历史只保留问题与答案 |
| `router` | Router 调用、thinking、原始输出、解析输出 |
| `retrieval` | 服务层受控检索过程和结果 |
| `specialist` | Specialist 调用、thinking、原始输出、解析输出 |
| `final` | 最终客户可见答案 |
| `error` | 失败信息，成功时为 `null` |
| `review` | 人工审查和后续标注占位 |

---

## 5. 职责边界

### 5.1 Router

Router 只负责：

- 理解用户本轮问题和必要历史指代。
- 选择 Specialist。
- 生成结构化 Router JSON。
- 判断是否需要检索。
- 生成 `retrieval_queries`。

Router 不负责：

- 不直接检索知识库。
- 不读取 wiki 文件。
- 不生成客户可见答案。
- 不判断具体业务事实，例如价格、接口参数、故障结论。

### 5.2 Retrieval

Retrieval 是服务层受控能力，负责：

- 根据 Router 的 `retrieval_queries` 执行 qmd/wiki 检索。
- 按 Specialist scope 过滤证据。
- 读取候选页面。
- 执行缓存。
- 记录检索耗时、命中、miss、sources、candidate pages。

Retrieval 不负责：

- 不生成客户答案。
- 不总结业务结论注入给 Specialist。
- 不替换 Specialist 答案。

### 5.3 Specialist

Specialist 负责：

- 接收 Router JSON。
- 接收服务层提供的 candidate pages。
- 基于证据和角色规则输出结构化 JSON。
- 生成最终候选答案字段 `answer`。

Specialist 不负责：

- 不直接操作 shell。
- 不直接读取文件系统。
- 不绕过 Retrieval 自己检索。

---

## 6. 字段定义草案

### 6.1 `time`

```json
"time": {
  "received_at": "2026-05-28T10:12:30+08:00",
  "answered_at": "2026-05-28T10:12:40+08:00",
  "total_duration_ms": 9846
}
```

### 6.2 `runtime`

```json
"runtime": {
  "environment": "local",
  "simulation": true,
  "git_commit": "15460d9",
  "customer_chat_mode": "routed",
  "router_model_id": "llm_qwen_20260528",
  "specialist_model_id": "llm_qwen_20260528",
  "router_contract_version": "customer_router.v1"
}
```

### 6.3 `request`

```json
"request": {
  "message": "静态 IP 怎么卖？",
  "history_turns": 2,
  "history_message_count": 4,
  "history_summary": "用户之前询问过静态 IP 类型",
  "conversation_context": [
    {
      "question": "静态 IP 有哪几种？",
      "answer": "静态 IP 通常可按共享/独享、数据中心/住宅等维度区分。"
    },
    {
      "question": "共享和独享有什么区别？",
      "answer": "共享型是多个用户共用资源，独享型是单独分配给一个用户使用。"
    }
  ]
}
```

第一版保存完整对话上下文，但只保存客户可见问答文本：

- `message`：本轮用户问题，对应 `/api/v1/customer/chat` 请求字段。
- `history_turns`：历史问答轮数。
- `history_message_count`：原始历史消息条数，包含 user 和 assistant 消息；通常等于 `history_turns * 2`，但异常/未完成对话可能不同。
- `history_summary`：Router 生成的必要历史摘要，用于快速审计指代关系。
- `conversation_context`：本轮之前的完整历史问答列表，只记录每轮的 `question` 和 `answer`。

`conversation_context` 不保存：

- Router JSON。
- Specialist JSON。
- 检索结果。
- Prompt。
- thinking。
- 内部调试字段。

当前轮最终答案统一放在 `final.answer`，不在 `request.conversation_context` 中重复记录。

---

## 7. Router 审计结构

```json
"router": {
  "model": {
    "id": "llm_qwen_20260528",
    "name": "qwen3.6-flash",
    "thinking_enabled": false
  },
  "duration_ms": 2182,
  "thinking": {
    "enabled": false,
    "saved": false,
    "content": null,
    "chars": 0
  },
  "raw_output": "...",
  "output": {
    "contract_version": "customer_router.v1",
    "specialist": "pricing",
    "routing_confidence": 0.95,
    "routing_reason": "用户明确询问静态 IP 收费，属于价格咨询。",
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
    "retrieval_queries": ["四叶天 静态 IP 价格 共享 独享"],
    "handoff_notes": "普通静态 IP 问价。不要在 Router 中写价格事实，交给 pricing 基于候选证据回答。"
  }
}
```

### 7.1 Router thinking 规则

Router 中必须固定存在 `thinking` 字段。

如果 Router thinking 关闭：

```json
"thinking": {
  "enabled": false,
  "saved": false,
  "content": null,
  "chars": 0
}
```

如果 Router thinking 开启且模型返回了思考内容：

```json
"thinking": {
  "enabled": true,
  "saved": true,
  "content": "完整 Router 思考内容……",
  "chars": 1800
}
```

如果 Router thinking 开启但模型没有返回 reasoning：

```json
"thinking": {
  "enabled": true,
  "saved": false,
  "content": null,
  "chars": 0,
  "unavailable_reason": "model_did_not_return_reasoning"
}
```

---

## 8. Retrieval 审计结构

Retrieval 放在顶级字段，不放在 Specialist 内部，避免误解为 Specialist 自己执行检索。

```json
"retrieval": {
  "requested_by": "router",
  "executed_by": "service",
  "target_specialist": "pricing",
  "scope": "pricing",
  "duration_ms": 816,
  "source_count": 1,

  "attempted_queries": [
    "四叶天 静态 IP 价格 共享 独享"
  ],
  "executed_queries": [
    "四叶天 静态 IP 价格 共享 独享"
  ],
  "skipped_query_count": 0,

  "qmd_cache_hits": 0,
  "qmd_cache_misses": 1,
  "page_cache_hits": 0,
  "page_cache_misses": 2,

  "query_timings": [
    {
      "query_index": 1,
      "query": "四叶天 静态 IP 价格 共享 独享",
      "cache": "miss",
      "duration_ms": 815,
      "result_count": 4
    }
  ],

  "page_timings": [
    {
      "path": "wiki/knowledge/si-ye-tian-static-ip-pricing.md",
      "cache": "miss",
      "duration_ms": 0,
      "body_chars": 3189,
      "success": true
    }
  ],

  "candidates": [
    {
      "path": "wiki/knowledge/si-ye-tian-static-ip-pricing.md",
      "title": "四叶天静态 IP 价格规则",
      "score": 0.93,
      "confidence": "high"
    }
  ],

  "sources": [
    {
      "path": "wiki/knowledge/si-ye-tian-static-ip-pricing.md",
      "title": "四叶天静态 IP 价格规则",
      "confidence": "high"
    }
  ],

  "candidate_page_paths": [
    "wiki/knowledge/si-ye-tian-static-ip-pricing.md"
  ],

  "evidence_preview": [
    {
      "path": "wiki/knowledge/si-ye-tian-static-ip-pricing.md",
      "title": "四叶天静态 IP 价格规则",
      "preview": "..."
    }
  ]
}
```

### 8.1 Evidence 正文保存策略

第一版建议默认只保存：

- path
- title
- confidence
- score
- preview
- body_chars

不默认保存完整 candidate page 正文，避免 JSONL 体积过大。

如后续确实需要完整回放，可增加配置项：

```text
save_full_evidence=true
```

---

## 9. Specialist 审计结构

```json
"specialist": {
  "name": "pricing",
  "model": {
    "id": "llm_qwen_20260528",
    "name": "qwen3.6-flash",
    "thinking_enabled": true
  },
  "duration_ms": 6843,
  "thinking": {
    "enabled": true,
    "saved": true,
    "content": "完整 Specialist 思考内容……",
    "chars": 8568
  },
  "input": {
    "user_message": "静态 IP 怎么卖？",
    "router_output_ref": "router.output",
    "candidate_page_paths_ref": "retrieval.candidate_page_paths"
  },
  "raw_output": "...",
  "output": {
    "answer_mode": "evidence",
    "answer": "四叶天静态 IP 按个/月计费，共享型和独享型价格不同，具体以带宽、IP 类型和数量为准。您需要共享还是独享？",
    "review_question": "",
    "confidence": 0.9,
    "evidence_confidence": 0.9,
    "review_required": false,
    "review_reason": "",
    "suggested_target_path": "",
    "sources": [
      {
        "path": "wiki/knowledge/si-ye-tian-static-ip-pricing.md",
        "confidence": "high"
      }
    ],
    "notes": ""
  }
}
```

### 9.1 Specialist thinking 规则

Specialist 中必须固定存在 `thinking` 字段。

如果 Specialist thinking 关闭：

```json
"thinking": {
  "enabled": false,
  "saved": false,
  "content": null,
  "chars": 0
}
```

如果 Specialist thinking 开启且模型返回了完整思考内容：

```json
"thinking": {
  "enabled": true,
  "saved": true,
  "content": "完整 Specialist 思考内容……",
  "chars": 8568
}
```

如果 Specialist thinking 开启但模型没有返回 reasoning：

```json
"thinking": {
  "enabled": true,
  "saved": false,
  "content": null,
  "chars": 0,
  "unavailable_reason": "model_did_not_return_reasoning"
}
```

---

## 10. Final 审计结构

```json
"final": {
  "answer": "四叶天静态 IP 按个/月计费，共享型和独享型价格不同，具体以带宽、IP 类型和数量为准。您需要共享还是独享？",
  "answer_mode": "evidence",
  "source_count": 1,
  "review_required": false
}
```

`final.answer` 必须与 Specialist 输出中的 `output.answer` 一致。

`final.source_count` 只统计 Specialist 最终输出 `specialist.output.sources` 中实际引用的 source 数量。服务层提供给 Specialist 的证据源数量放在 `retrieval.source_count`，两者不能混用。

服务层只允许做：

```text
strings.TrimSpace
```

不允许：

- 服务层改写答案。
- 服务层替换答案。
- 服务层注入业务结论。
- 服务层 fallback 成另一套客服话术。

---

## 11. Error 审计结构

成功时：

```json
"error": null
```

失败时：

```json
"error": {
  "stage": "specialist_parse",
  "message": "specialist returned empty answer",
  "raw_output": "..."
}
```

建议固定 `stage` 枚举：

```text
router_call
router_parse
retrieval
specialist_call
specialist_parse
final_response
```

---

## 12. Review 审计结构

第一版先写入占位字段：

```json
"review": {
  "status": "unreviewed",
  "is_good_answer": null,
  "error_type": "",
  "correct_answer": "",
  "note": "",
  "reviewed_by": "",
  "reviewed_at": ""
}
```

后续人工审查可以单独写入 review JSONL，或在数据库中维护 review 状态。

---

## 13. 完整 V1 示例

```json
{
  "schema_version": "customer_chat_audit.v1",
  "record_type": "customer_chat_trace",
  "trace_id": "trace_xxx",
  "session_id": "test-external-chat-interface",
  "time": {
    "received_at": "2026-05-28T10:12:30+08:00",
    "answered_at": "2026-05-28T10:12:40+08:00",
    "total_duration_ms": 9846
  },
  "runtime": {
    "environment": "local",
    "simulation": true,
    "git_commit": "15460d9",
    "customer_chat_mode": "routed",
    "router_model_id": "llm_qwen_20260528",
    "specialist_model_id": "llm_qwen_20260528",
    "router_contract_version": "customer_router.v1"
  },
  "request": {
    "message": "静态 IP 怎么卖？",
    "history_turns": 2,
    "history_message_count": 4,
    "history_summary": "用户之前询问过静态 IP 类型",
    "conversation_context": [
      {
        "question": "静态 IP 有哪几种？",
        "answer": "静态 IP 通常可按共享/独享、数据中心/住宅等维度区分。"
      },
      {
        "question": "共享和独享有什么区别？",
        "answer": "共享型是多个用户共用资源，独享型是单独分配给一个用户使用。"
      }
    ]
  },
  "router": {
    "model": {
      "id": "llm_qwen_20260528",
      "name": "qwen3.6-flash",
      "thinking_enabled": false
    },
    "duration_ms": 2182,
    "thinking": {
      "enabled": false,
      "saved": false,
      "content": null,
      "chars": 0
    },
    "raw_output": "...",
    "output": {
      "contract_version": "customer_router.v1",
      "specialist": "pricing",
      "routing_confidence": 0.95,
      "routing_reason": "用户明确询问静态 IP 收费，属于价格咨询。",
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
      "retrieval_queries": ["四叶天 静态 IP 价格 共享 独享"],
      "handoff_notes": "普通静态 IP 问价。不要在 Router 中写价格事实，交给 pricing 基于候选证据回答。"
    }
  },
  "retrieval": {
    "requested_by": "router",
    "executed_by": "service",
    "target_specialist": "pricing",
    "scope": "pricing",
    "duration_ms": 816,
    "source_count": 1,
    "attempted_queries": ["四叶天 静态 IP 价格 共享 独享"],
    "executed_queries": ["四叶天 静态 IP 价格 共享 独享"],
    "skipped_query_count": 0,
    "qmd_cache_hits": 0,
    "qmd_cache_misses": 1,
    "page_cache_hits": 0,
    "page_cache_misses": 2,
    "candidate_page_paths": ["wiki/knowledge/si-ye-tian-static-ip-pricing.md"],
    "sources": [
      {
        "path": "wiki/knowledge/si-ye-tian-static-ip-pricing.md",
        "title": "四叶天静态 IP 价格规则",
        "confidence": "high"
      }
    ]
  },
  "specialist": {
    "name": "pricing",
    "model": {
      "id": "llm_qwen_20260528",
      "name": "qwen3.6-flash",
      "thinking_enabled": true
    },
    "duration_ms": 6843,
    "thinking": {
      "enabled": true,
      "saved": true,
      "content": "完整 Specialist 思考内容……",
      "chars": 8568
    },
    "input": {
      "user_message": "静态 IP 怎么卖？",
      "router_output_ref": "router.output",
      "candidate_page_paths_ref": "retrieval.candidate_page_paths"
    },
    "raw_output": "...",
    "output": {
      "answer_mode": "evidence",
      "answer": "四叶天静态 IP 按个/月计费，共享型和独享型价格不同，具体以带宽、IP 类型和数量为准。您需要共享还是独享？",
      "review_question": "",
      "confidence": 0.9,
      "evidence_confidence": 0.9,
      "review_required": false,
      "review_reason": "",
      "suggested_target_path": "",
      "sources": [
        {
          "path": "wiki/knowledge/si-ye-tian-static-ip-pricing.md",
          "confidence": "high"
        }
      ],
      "notes": ""
    }
  },
  "final": {
    "answer": "四叶天静态 IP 按个/月计费，共享型和独享型价格不同，具体以带宽、IP 类型和数量为准。您需要共享还是独享？",
    "answer_mode": "evidence",
    "source_count": 1,
    "review_required": false
  },
  "error": null,
  "review": {
    "status": "unreviewed",
    "is_good_answer": null,
    "error_type": "",
    "correct_answer": "",
    "note": "",
    "reviewed_by": "",
    "reviewed_at": ""
  }
}
```

---

## 14. 第一阶段落地任务

### 14.1 后端结构

- 新增 Customer Chat audit JSONL record 结构体。
- 固定 `schema_version=customer_chat_audit.v1`。
- 固定 `record_type=customer_chat_trace`。
- Router 和 Specialist 都写入 `thinking` 字段。
- 关闭 thinking 时 `content=null`，不要省略字段。
- 开启 thinking 且有内容时保存完整 thinking 内容。

### 14.2 写入逻辑

- Customer Chat 请求完成后写入一行 JSONL。
- 成功和失败都要写入。
- 写入失败不应影响客户请求主链路，但必须记录服务日志。
- 每行 JSON 必须是单行。

### 14.3 配置

第一版复用现有 Customer Chat 日志配置控制 JSONL 写入、脱敏和保留天数。

已落地/沿用配置语义：

```text
customer_query.answer_log.enabled
customer_query.answer_log.redact
customer_query.answer_log.retention_days
```

后续如需更细粒度控制，再新增 `save_full_prompt`、`save_full_evidence`、`save_reasoning` 等独立配置。

当前已确认：

- Router thinking 开启时保存完整 thinking。
- Specialist thinking 开启时保存完整 thinking。
- 关闭 thinking 时 thinking 字段保留，`content=null`。

### 14.4 后台审查页面

后续后台审查详情页应按以下分组展示：

```text
总览
Router
Retrieval
Specialist
Final
Error
Review
Raw JSON
```

其中 Router 面板必须清楚显示：

- Router 原始 JSON。
- Router 交给 Specialist 的完整结构化内容。

Specialist 面板必须清楚显示：

- Specialist 原始输出。
- Specialist 解析后的 JSON。
- Specialist thinking。

---

## 15. 验收标准

### 15.1 JSONL 结构验收

- 每轮问答生成一行 JSONL。
- JSONL 每行可被标准 JSON parser 解析。
- 顶级字段完整存在。
- `retrieval` 在顶级，不在 `specialist` 内。
- `router.thinking` 和 `specialist.thinking` 固定存在。
- thinking 关闭时 `content=null`。
- thinking 开启时保存完整思考内容。

### 15.2 链路审计验收

任意一条 JSONL 应能回答：

- 用户问了什么？
- Router 为什么分配给这个 Specialist？
- Router 给了哪些 retrieval queries？
- 服务层实际执行了哪些检索？
- 命中了哪些知识库页面？
- Specialist 基于哪些 candidate pages 回答？
- Specialist 输出的原始 JSON 是什么？
- 最终客户看到的答案是什么？
- 哪个阶段最耗时？
- 模型开启 thinking 时，完整思考内容是什么？

### 15.3 边界验收

- 服务层不得因为 JSONL 审计而改写答案。
- JSONL 写入失败不得导致客户请求失败。
- Specialist 不获得 shell 权限。
- Retrieval 仍由服务层受控执行。
- 不在代码中写死知识库业务路径。

---

## 16. 后续扩展方向

后续可以基于 JSONL 增加：

- 审查标注 JSONL。
- 错误归因统计。
- 高频问题统计。
- 回归测试集自动生成。
- 知识库缺口发现。
- Router 路由准确率评估。
- Specialist 回答质量评估。
- Prompt 版本对比。
- 模型配置对比。

但这些都应建立在第一版稳定的审计 JSONL 之上。
