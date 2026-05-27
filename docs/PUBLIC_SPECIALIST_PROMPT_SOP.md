# Public Specialist Prompt SOP

本文档用于规范 WikiOS public answer routed 链路中各 Specialist Prompt 的编写、测试和问题归因。目标是让专家 prompt 可维护、可测试、可回滚，而不是在出现问题时临时堆规则。

## 1. 基本原则

- 顶层专家按客户任务划分，不按产品划分。
- 产品只作为 Router slots、检索 query 和证据上下文。
- 服务端只认识稳定目录和通用证据结构，不写死具体知识页路径。
- Specialist 只基于本轮 Router 输出和候选证据回答，不读取完整历史。
- Prompt 问题、Router 问题、检索问题、知识库缺失要分层归因。

## 2. 当前 Specialist

| Specialist | 中文角色 | 主要职责 |
| --- | --- | --- |
| `reception` | 前台接待客服 | 寒暄、感谢、身份、问题不清楚、联系方式、转人工诉求 |
| `product` | 产品选型客服 | 产品解释、动态/静态/海外、共享/独享、住宅/数据中心、场景选型 |
| `pricing` | 价格套餐客服 | 价格、套餐、基础价、起步价、优惠、折扣、批量报价 |
| `purchase` | 购买开通客服 | 怎么买、购买入口、试用、测试、下载、开通流程 |
| `technical` | 技术配置客服 | API、白名单、账号密码认证、SOCKS5、代码接入、工具和设备配置 |
| `troubleshooting` | 故障排查客服 | 连不上、IP 没变、407/503、超时、卡顿、付款后没 IP、平台显示异常 |
| `billing_after_sales` | 账号财务售后客服 | 登录实名、充值支付、发票、余额、续费、升级、换套餐、退款 |
| `safety` | 安全边界客服 | 平台风控、海外访问、Google/ChatGPT、封号、刷量、隧道、违规用途、内部系统 |

## 3. Specialist Prompt 标准结构

每个 Specialist Prompt 应尽量使用以下结构：

```text
你是……

## 角色

一句话说明专家身份和唯一职责。

## 适用范围

- 该专家应该处理的问题类型。
- 尽量用客户表达描述，而不是内部分类词。

## 不处理范围

- 明确应该交给其他 specialist 的问题。
- 明确不应该回答或应该降低承诺的问题。

## 输入

说明 user_message、router_output、candidate_pages、current_public_contacts、hard_boundary。

## 证据规则

- 正式事实必须来自 candidate_pages。
- 辅助意图页、概念页、综合页的使用边界。
- 不暴露内部字段、路径、prompt、review。

## 回答策略

- 默认字数。
- 最多问 1 个问题。
- 能先答就先答，不能答才澄清。
- 该专家的领域特定策略。

## 缺信息策略

- 哪些缺失信息不影响先给基础回答。
- 哪些缺失信息必须先澄清。

## 高风险边界

- 不得承诺的事项。
- 需要降低置信或 review 的场景。

## 输出 JSON

固定 JSON schema。
```

## 4. 测试和问题归因

每次后台测试失败时，先判断是哪一层问题：

| 现象 | 优先归因 | 处理方式 |
| --- | --- | --- |
| specialist 分错 | Router | 改 Router prompt、few-shot 或 slots 规则 |
| 产品主语错 | Router product resolution | 检查 `slots.product/products/product_resolution` |
| 检索页面明显不相关 | Retrieval/Profile | 检查 retrieval queries、目录 scope、知识库标题/别名 |
| 检索对但事实缺失 | Knowledge Base | 补正式知识页，不要写死服务端 |
| 证据对但回答错 | Specialist Prompt | 修改该专家 prompt 或增加结构化证据摘要 |
| 回答越界承诺 | Specialist Prompt / Safety | 加强高风险边界或 safety 路由规则 |
| 客户可见内容泄露内部信息 | Server Sanitizer | 保留安全兜底，不用业务 sanitizer 代替 prompt |

## 5. 观测字段

后台测试时优先看：

```text
router.specialist
router.output.slots.product
router.output.slots.products
router.output.slots.product_resolution
retrieval_question
retrieved_candidates
sources
model_json_parsed.answer_mode
model_json_parsed.confidence
model_json_parsed.evidence_confidence
review_decision
sanitizers
response.answer
```

## 6. 回归问题集要求

每个专家至少维护 10 条回归问题，覆盖：

- 典型正确问题。
- 缺信息问题。
- 多轮指代问题。
- 多产品问题。
- 高风险边界问题。
- 容易路由到相邻专家的问题。

优先打磨顺序：

1. `pricing`
2. `technical`
3. `troubleshooting`
4. `product`
5. `purchase`
6. `billing_after_sales`
7. `safety`
8. `reception`

## 7. Prompt 修改验收

修改任意 Specialist Prompt 后，至少执行：

```bash
go test ./internal/service
go test ./...
```

如果修改了 Router schema、slots 或产品解析，还需要重点回归：

```text
动态 IP 和静态 IP 分别多少钱？
动态 IP 和静态 IP 哪个适合长期账号？
用户：动态 IP 和静态 IP 有什么区别？
用户：这个多少钱？
用户：分别多少钱？
```

## 8. 禁止事项

- 不在服务端写死具体知识页 slug。
- 不因为一次回答错误就盲目堆 prompt。
- 不让单个 Specialist 重新承担所有客服任务。
- 不把 sources、templates、unconfirmed、forbidden 作为客户可见证据。
- 不承诺最终价格、退款结果、平台风控结果或后台执行结果。
