# 客服控制面分层 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]` / `- [x]`) syntax for tracking.

**本地进度（2026-09-17）：** Task 1–5 实现与单测已完成。Task 6 的回归夹具、Wiki 申请口径改写、全量单测已完成。未做：各任务「仅当用户要求」的 git 提交，以及 Task 6 Step 4 部署后线上手测。

**Goal:** 把客服从「提示词加减 / 整句替换」拧成三层：数字由代码核算，提示词只负责说话，代码只否决不代写，避免僵硬、乱答和死循环。

**Architecture:** 议价不再让模型对着价格页心算。Router 抽出产品 / 带宽 / 数量后，Go 查表得到本轮 `quote_facts`（可报价、已到底、缺槽位）。Specialist 只把这些数字说成人话。服务端可以删违禁词、拦低于底价的数字；删空了只允许模型重写一次，禁止再塞固定客套话。会话里的上一轮报价由服务端从历史里解析，不信任 Router 的 `history_summary`。

**Tech Stack:** Go 1.25、现有 `internal/service` Customer Chat Router v1、Specialist prompt（`internal/llm/prompts/customer_specialist_*.md`）、`go test ./internal/service/ ./internal/api/`。

## Global Constraints

- 始终使用简体中文对客；单位只允许 `元/条/月`。
- 对客价不得低于当前档位 `floor`；不得编比 `quote_facts.unit_price` 更低的数字。
- 不对客说数量档、价格档、`11-30`、系统定价、修改订单金额、资料库、知识库、帮您提交/申请/办理。
- 代码不得用整句固定话术替换客户可见 `answer`（禁止再使用 `customerDeprecatedPricingFallback` / `customerInternalContextFallback`）。
- 寒暄解卡只改 Router specialist，不贴接待套话。
- 价格表数字以线上 Wiki 价格页为准，实现前先和产品锁定一版；本计划里的区间只作接口示例，不得把过期折扣档（`4折`–`9折`、`元/个`）写进表。
- 不在本计划扩大 Router 关键词改写；不新增更多 playbook 句式拦截。
- 每个任务先写失败测试，再写最小实现；未要求时不 git commit。

---

## 为什么要改

现在三件事挤在一个旋钮上：

1. 提示词又管像不像人说话，又管价格对不对 → 写多了僵，写少了乱。
2. 知识页仍可能教「先报高、再低走申请」，和提示词打架。
3. `sanitizeCustomerVisibleAnswer` 删空后会塞固定句，写进下一轮上下文，再被复述，形成死循环。

目标分层：

```text
客户原话
  → Router（分诊 + slots，寒暄只改 specialist）
  → QuoteEngine（查表，产出 quote_facts）
  → Specialist（只组织语言）
  → Veto（删违禁 / 拦错价）
      删空 → 同轮重写 1 次
      再空 → 本轮失败，不代答
```

本计划不解决：排障归因、人工工单、动态/海外完整价目（表里没有就 `unsupported`，模型只追问或说没有公开价）。Wiki 价格页口径对齐是 Task 6 的运维项，和代码可并行。

---

## 文件责任

| 文件                                                                            | 责任                                                                 |
| ------------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| Create: `internal/service/testdata/customer_pricing_table.v1.json`              | 对客可报价区间，唯一数字源。                                         |
| Create: `internal/service/customer_quote.go`                                    | 表加载、槽位解析、`ResolveCustomerQuote`。                           |
| ~~Create:~~ `internal/service/customer_quote_test.go`                           | ~~报价引擎单测。~~                                                   |
| ~~Create:~~ `internal/service/customer_answer_veto.go`                          | 否决：删泄漏句、拦错价；不代写。从现有 sanitize 迁出。               |
| Create: `internal/service/customer_answer_veto_test.go`                         | 否决与「删空不塞套话」单测。                                         |
| Create: `internal/service/customer_conversation_hygiene.go`                     | 历史角色清洗、上一轮报价解析。                                       |
| Create: `internal/service/customer_conversation_hygiene_test.go`                | 脏历史与报价提取单测。                                               |
| Modify: `internal/service/customer_routed_pipeline.go`                          | 注入 `quote_facts`；定价已核算时不再塞整页价格正文；否决后重写一次。 |
| Modify: `internal/service/customer_chat_service.go`                             | 删除两句 fallback 常量；sanitize 改为调用 veto。                     |
| Modify: `internal/service/customer_specialist_prompt.go`                        | JSON 后缀去掉与 veto 重复的长禁令。                                  |
| Modify: `internal/llm/prompts/customer_specialist_base.md`                      | 瘦身：说话 + JSON；数字看 `quote_facts`。                            |
| Modify: `internal/llm/prompts/customer_specialist_pricing.md`                   | 删掉心算底价长段，改为「只用 quote_facts」。                         |
| Modify: `internal/llm/prompts/customer_specialist_check.md`                     | L4 去掉已由代码保证的计价项。                                        |
| Modify: `internal/llm/prompts/customer_specialist_boundary.md`                  | 写明：服务端不代答；删空只重写一次。                                 |
| Modify: `docs/CUSTOMER_SPECIALIST_PROMPT_SOP.md`                                | 更新分层：价格事实来自 quote_facts，不再写「只认 candidate_pages」。 |
| Modify: `internal/service/testdata/customer_specialist_pricing_regression.json` | 数字与锁定表对齐；断言改为看 quote_facts 注入。                      |
| Modify: `internal/service/customer_specialist_prompt_test.go`                   | 契约改为新提示词，不再要求大段底价禁令。                             |

现有 `customer_chat_service.go` 已过大。本计划把报价和否决拆到新文件，不先做大搬家。

---

## 锁定接口（后续任务必须同名同字段）

```go
type CustomerPricingBand struct {
	MinQty int `json:"min_qty"`
	MaxQty int `json:"max_qty"` // 含；最后一档用 0 表示无上限
	Low    int `json:"low"`     // 对客底价
	High   int `json:"high"`    // 首报默认取这个
}

type CustomerPricingSKU struct {
	Product   string                 `json:"product"`   // static_shared | residential_shared | residential_dedicated
	Bandwidth string                 `json:"bandwidth"` // 5M | 10M | 20M
	Bands     []CustomerPricingBand  `json:"bands"`
}

type CustomerPricingTable struct {
	Version string               `json:"version"` // 必须是 customer_pricing.v1
	SKUs    []CustomerPricingSKU `json:"skus"`
}

type CustomerQuoteInput struct {
	Product    string // 上表 product；未知为 ""
	Bandwidth  string
	Quantity   int    // 解析失败为 0
	NamedPrice int    // 本轮客户点名的单价；没有为 0
	LastQuoted int    // 服务端从历史解析的已报单价；没有为 0
}

// Status: missing_slots | quoted | at_floor | below_floor | unsupported
type CustomerQuoteFacts struct {
	Status       string   `json:"status"`
	Missing      []string `json:"missing,omitempty"` // bandwidth / quantity / product
	Product      string   `json:"product,omitempty"`
	Bandwidth    string   `json:"bandwidth,omitempty"`
	Quantity     int      `json:"quantity,omitempty"`
	UnitPrice    int      `json:"unit_price,omitempty"`    // 本轮允许说出口的单价
	FloorPrice   int      `json:"floor_price,omitempty"`
	HighPrice    int      `json:"high_price,omitempty"`
	MonthlyTotal int      `json:"monthly_total,omitempty"` // UnitPrice * Quantity
	CanGoLower   bool     `json:"can_go_lower"`
	Speak        string   `json:"speak"` // 给模型的一行中文事实，不含档位
}

func LoadCustomerPricingTable(path string) (CustomerPricingTable, error)
func DefaultCustomerPricingTable() (CustomerPricingTable, error) // 读 testdata/customer_pricing_table.v1.json
func ResolveCustomerQuote(table CustomerPricingTable, in CustomerQuoteInput) CustomerQuoteFacts
func ParseCustomerQuantity(text string) int
func ParseCustomerNamedPrice(text string) int
func LastQuotedUnitPrice(history []ChatMessage) int
func SanitizeCustomerHistory(history []ChatMessage) []ChatMessage
func VetoCustomerVisibleAnswer(answer string, facts CustomerQuoteFacts) (kept string, changed bool, emptyReason string)
```

议价规则（写进测试，不要写进长提示词）：

1. 缺 `product` / `bandwidth` / `quantity` → `missing_slots`，`UnitPrice=0`，`Speak` 只写缺什么。
2. 表里没有该 SKU → `unsupported`。
3. 查到档位 `[Low, High]`。
4. 尚无 `LastQuoted`：若 `NamedPrice` 在 `[Low, High]` 则报 `NamedPrice`，否则报 `High`。`CanGoLower = UnitPrice > Low`。
5. 已有 `LastQuoted` 且 `LastQuoted > Low`：客人要便宜且未点名 → 报 `Low`；点名且 `NamedPrice >= Low` → 报 `NamedPrice`（若高于上次已报价，仍报点名且不超过 `High`）；点名 `< Low` → `below_floor`，`UnitPrice=Low`。
6. `LastQuoted == Low` 再压 → `at_floor`，`UnitPrice=Low`，`CanGoLower=false`。
7. `Speak` 示例：`本轮可说：静态 IP 5M 15条，20元/条/月，月费300元。还可以再低。` 或 `已经是底价 20元/条/月，不能再低。` 禁止出现档位数字区间标签。

Router slots 映射：

- `primary_product=static_ip` 且 `ip_type!=residential` → `static_shared`
- `ip_type=residential` 且 `static_type=dedicated`（或客户说独享）→ `residential_dedicated`
- `ip_type=residential` 且共享 / 未分类型 → 共享可报 `residential_shared`；未分类型且价格不同 → `missing product`（先问共享还是独享）

---

### Task 1: 报价引擎

**Files:**

- Create: `internal/service/testdata/customer_pricing_table.v1.json`
- Create: `internal/service/customer_quote.go`
- Create: `internal/service/customer_quote_test.go`

**Interfaces:**

- Consumes: 无
- Produces: `LoadCustomerPricingTable`、`DefaultCustomerPricingTable`、`ResolveCustomerQuote`、`ParseCustomerQuantity`、`ParseCustomerNamedPrice`、`CustomerQuoteFacts`

- [x] **Step 1: 和产品锁定表数字，写入 JSON**

打开线上 Wiki 价格页（当前代码常量是 `wiki/knowledge/si-ye-tian-static-ip-pricing.md`）。把静态共享 / 住宅共享 / 住宅独享 × 5M/10M/20M 的数量档抄进 `customer_pricing_table.v1.json`。`version` 必须为 `customer_pricing.v1`。

若线上 5M 11-30 与仓库回归夹具不一致（夹具曾写 20-25，线上会话曾按 18-23），**以产品当场确认的线上页为准**，并在 Task 6 改回归夹具。

JSON 形状：

```json
{
  "version": "customer_pricing.v1",
  "skus": [
    {
      "product": "static_shared",
      "bandwidth": "5M",
      "bands": [
        { "min_qty": 1, "max_qty": 10, "low": 20, "high": 25 },
        { "min_qty": 11, "max_qty": 30, "low": 18, "high": 23 }
      ]
    }
  ]
}
```

上面 `low/high` 是形状示例，实现时换成锁定值。

- [x] **Step 2: 写失败测试**

`internal/service/customer_quote_test.go` 至少包含：

```go
func TestParseCustomerQuantityAndNamedPrice(t *testing.T) {
	if got := ParseCustomerQuantity("5M要15条"); got != 15 {
		t.Fatalf("quantity=%d", got)
	}
	if got := ParseCustomerNamedPrice("我想20元一条每月"); got != 20 {
		t.Fatalf("named=%d", got)
	}
}

func TestResolveCustomerQuoteFirstQuoteUsesHigh(t *testing.T) {
	table := mustPricingTable(t)
	got := ResolveCustomerQuote(table, CustomerQuoteInput{
		Product: "static_shared", Bandwidth: "5M", Quantity: 15,
	})
	if got.Status != "quoted" || got.UnitPrice != got.HighPrice || got.CanGoLower != true {
		t.Fatalf("%+v", got)
	}
}

func TestResolveCustomerQuoteNamedPriceAtOrAboveFloor(t *testing.T) {
	table := mustPricingTable(t)
	band := lookupBand(t, table, "static_shared", "5M", 15)
	got := ResolveCustomerQuote(table, CustomerQuoteInput{
		Product: "static_shared", Bandwidth: "5M", Quantity: 15,
		LastQuoted: band.High, NamedPrice: band.Low,
	})
	if got.UnitPrice != band.Low || got.Status != "quoted" {
		t.Fatalf("want named floor, got %+v", got)
	}
}

func TestResolveCustomerQuoteBelowFloorStaysAtFloor(t *testing.T) {
	table := mustPricingTable(t)
	band := lookupBand(t, table, "static_shared", "5M", 15)
	got := ResolveCustomerQuote(table, CustomerQuoteInput{
		Product: "static_shared", Bandwidth: "5M", Quantity: 15,
		LastQuoted: band.Low, NamedPrice: 10,
	})
	if got.Status != "below_floor" || got.UnitPrice != band.Low || got.CanGoLower {
		t.Fatalf("%+v", got)
	}
}

func TestResolveCustomerQuoteMissingSlots(t *testing.T) {
	got := ResolveCustomerQuote(mustPricingTable(t), CustomerQuoteInput{Product: "static_shared"})
	if got.Status != "missing_slots" || len(got.Missing) == 0 {
		t.Fatalf("%+v", got)
	}
}
```

- [x] **Step 3: 跑测试确认失败**

Run: `go test ./internal/service/ -run 'TestParseCustomerQuantity|TestResolveCustomerQuote' -count=1`

Expected: FAIL，函数未定义。

- [x] **Step 4: 写最小实现**

`customer_quote.go`：`embed` 或 `os.ReadFile` 读 `testdata/customer_pricing_table.v1.json`。数量解析吃 `15条` / `15个` / `15`。点名解析吃 `20元一条` / `20元/条/月` / `我想20`。`Speak` 只用中文事实句。

- [x] **Step 5: 再跑测试**

Run: `go test ./internal/service/ -run 'TestParseCustomerQuantity|TestResolveCustomerQuote' -count=1`

Expected: PASS。

- [ ] **Step 6: 提交（仅当用户要求）**

```bash
git add internal/service/customer_quote.go internal/service/customer_quote_test.go internal/service/testdata/customer_pricing_table.v1.json
git commit -m "$(cat <<'EOF'
feat: 客服报价改为查表，不再让模型心算底价

EOF
)"
```

---

### Task 2: 把 quote_facts 注入 Specialist

**Files:**

- Modify: `internal/service/customer_routed_pipeline.go`（`customerSpecialistDecisionPrompt` 及定价检索组装）
- Modify: `internal/service/customer_specialist_pricing_regression_test.go`
- Modify: `internal/service/customer_routed_pipeline_test.go`（若已有 prompt 快照）

**Interfaces:**

- Consumes: `ResolveCustomerQuote`、`ParseCustomerQuantity`、`ParseCustomerNamedPrice`、`LastQuotedUnitPrice`（Task 5 未完成前，本任务可先用临时 `LastQuotedUnitPrice` 扫 assistant 文本里的 `元/条/月`，Task 5 再换成清洗后历史）
- Produces: user prompt 中的 `quote_facts:` 块；定价已 `quoted|at_floor|below_floor` 时 `candidate_pages` 改为一行「价格已由服务端核算，见 quote_facts」，避免 Wiki 旧口径教模型去申请

- [x] **Step 1: 写失败测试**

在 `customer_specialist_pricing_regression_test.go` 的 prompt 断言中增加：

```go
if !strings.Contains(userPrompt, "quote_facts:") {
	t.Fatal("pricing specialist prompt must include quote_facts")
}
```

另写：

```go
func TestCustomerSpecialistDecisionPromptOmitsPricingPageWhenQuoteResolved(t *testing.T) {
	svc := NewCustomerChatService(Deps{Config: &config.Config{}})
	router := CustomerRouterOutput{
		Specialist: "pricing",
		Slots: CustomerRouterSlots{
			PrimaryProduct: "static_ip",
			IPType:         "datacenter",
			Bandwidth:      "5M",
			Quantity:       "15",
		},
	}
	evidence := customerSpecialistEvidenceResult{
		Profile: customerSpecialistProfile("pricing"),
		ContentBlocks: []string{
			"wiki/knowledge/si-ye-tian-static-ip-pricing.md\n首次报价报最高价，再低走申请。",
		},
	}
	prompt := svc.customerSpecialistDecisionPrompt(
		CustomerChatRequest{Question: "太贵了能便宜点吗"},
		"2026-09-17T10:00:00+08:00",
		&router,
		evidence.Profile,
		evidence,
		RuntimeSupportSettings{},
		"hard",
	)
	if strings.Contains(prompt, "再低走申请") || strings.Contains(prompt, "首次报价报最高价") {
		t.Fatalf("resolved quote must not send conflicting pricing page: %s", prompt)
	}
	if !strings.Contains(prompt, "quote_facts:") {
		t.Fatal("missing quote_facts")
	}
}
```

- [x] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run 'TestCustomerSpecialistPricingRegressionFixture|TestCustomerSpecialistDecisionPromptOmitsPricingPageWhenQuoteResolved' -count=1`

Expected: FAIL，prompt 没有 `quote_facts` 或仍含申请口径。

- [x] **Step 3: 实现注入**

在 `customerSpecialistDecisionPrompt` 里，`router_output` 之后插入：

```text
quote_facts:
<json 一行或缩进 YAML 均可，字段必须是 CustomerQuoteFacts>
```

组装输入：

```go
in := CustomerQuoteInput{
	Product:    customerQuoteProductFromSlots(routerOutput.Slots),
	Bandwidth:  strings.TrimSpace(routerOutput.Slots.Bandwidth),
	Quantity:   ParseCustomerQuantity(routerOutput.Slots.Quantity + " " + req.Question),
	NamedPrice: ParseCustomerNamedPrice(req.Question),
	LastQuoted: LastQuotedUnitPrice(req.History),
}
facts := ResolveCustomerQuote(table, in)
```

仅当 `profile.Name == "pricing"` 或本轮问题像问价时注入；其他专家给 `status: "not_applicable"`，避免产品专家开始报数字。

当 `facts.Status` 为 `quoted|at_floor|below_floor` 时，把 `candidateText` 换成 `价格已由服务端核算，见 quote_facts。不要另编数字。`

- [x] **Step 4: 再跑测试**

Run: `go test ./internal/service/ -run 'TestCustomerSpecialistPricingRegressionFixture|TestCustomerSpecialistDecisionPromptOmitsPricingPageWhenQuoteResolved' -count=1`

Expected: PASS。

- [ ] **Step 5: 提交（仅当用户要求）**

```bash
git commit -m "$(cat <<'EOF'
feat: 定价专家改为使用服务端 quote_facts

EOF
)"
```

---

### Task 3: 否决不代写，删空只重写一次

**Files:**

- Create: `internal/service/customer_answer_veto.go`
- Create: `internal/service/customer_answer_veto_test.go`
- Modify: `internal/service/customer_chat_service.go`（`sanitizeCustomerVisibleAnswerWithReason` 改为调 veto；删除两句 fallback 常量）
- Modify: `internal/service/customer_routed_pipeline.go`（sanitize 之后：若 `emptyReason != ""`，带「不要写被删内容，数字必须等于 quote_facts」再调一次 Specialist，仍空则返回 parse/empty 错误）
- Modify: `internal/service/customer_specialist_prompt_test.go`（原先断言 sanitize 后变成 fallback 的用例，改为断言空串或保留未违规句）

**Interfaces:**

- Consumes: `CustomerQuoteFacts`、现有 `customerVisibleAnswerLeaksInternalContext` / `customerAnswerUsesDeprecatedPricing`
- Produces: `VetoCustomerVisibleAnswer`；pipeline 最多 1 次 specialist rewrite

- [x] **Step 1: 写失败测试**

```go
func TestVetoCustomerVisibleAnswerDoesNotInsertFallback(t *testing.T) {
	kept, changed, reason := VetoCustomerVisibleAnswer(
		"根据资料库，5M 可以帮您提交特价申请到 10 元/条/月。",
		CustomerQuoteFacts{Status: "at_floor", UnitPrice: 20, FloorPrice: 20},
	)
	if kept != "" {
		t.Fatalf("expected empty after veto, got %q", kept)
	}
	if !changed || reason == "" {
		t.Fatal("expected veto reason")
	}
	if strings.Contains(kept, "请告诉我需要的带宽") || strings.Contains(kept, "我可以直接回答") {
		t.Fatal("veto must not author a replacement sentence")
	}
}

func TestVetoCustomerVisibleAnswerBlocksBelowQuoteFacts(t *testing.T) {
	kept, _, _ := VetoCustomerVisibleAnswer(
		"那就给您 10 元/条/月吧。",
		CustomerQuoteFacts{Status: "quoted", UnitPrice: 20, FloorPrice: 18},
	)
	if strings.Contains(kept, "10") {
		t.Fatalf("below-facts price leaked: %q", kept)
	}
}
```

现有 `TestSanitizeCustomerVisibleAnswer*`：期望从「变成 fallback 句」改为「只剩干净分句或空」。

- [x] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run 'TestVetoCustomerVisibleAnswer|TestSanitizeCustomerVisibleAnswer' -count=1`

Expected: FAIL，或旧 sanitize 仍返回 fallback。

- [x] **Step 3: 实现 veto + pipeline 重写**

`VetoCustomerVisibleAnswer`：按句删除泄漏 / 过期价；若 `facts.UnitPrice > 0`，删除对客单价 `< facts.FloorPrice` 或明显不等于本轮允许价且更低的数字（保留问句里客户自己说的「我想 10 元」不适用于 assistant answer）。整段删空则 `kept=""`，**不要**返回常量句。

`customer_routed_pipeline.go` 在第一次 veto 后若 `kept==""`：

1. 再调 `callSpecialist`，user prompt 追加：`上一轮草稿因内部话术或错价被丢弃。请重写 answer，数字必须与 quote_facts 一致，不要提资料库或提交申请。`
2. 再 veto。
3. 仍空：返回 error（与今天 empty answer 相同），**不**写客户可见默认句。`customer_specialist_boundary.md` 已写「没有默认客服话术」，保持一致。

删除：

```go
customerDeprecatedPricingFallback = "请告诉我需要的带宽和数量，我按当前价格核算。"
customerInternalContextFallback   = "我可以直接回答产品、价格、购买或配置问题。"
```

- [x] **Step 4: 再跑测试**

Run: `go test ./internal/service/ ./internal/api/ -count=1`

Expected: PASS。若 e2e 仍断言旧 fallback 或「帮您申请」mock，按 veto 语义改断言，不要恢复代写。

- [ ] **Step 5: 提交（仅当用户要求）**

```bash
git commit -m "$(cat <<'EOF'
fix: 客服否决不再整句替换，避免套话死循环

EOF
)"
```

---

### Task 4: 提示词只留说话

**Files:**

- Modify: `internal/llm/prompts/customer_specialist_base.md`
- Modify: `internal/llm/prompts/customer_specialist_pricing.md`
- Modify: `internal/llm/prompts/customer_specialist_check.md`
- Modify: `internal/llm/prompts/customer_specialist_boundary.md`
- Modify: `internal/service/customer_specialist_prompt.go`
- Modify: `internal/service/customer_specialist_prompt_test.go`
- Modify: `docs/CUSTOMER_SPECIALIST_PROMPT_SOP.md`

**Interfaces:**

- Consumes: Task 2 的 `quote_facts` 字段名
- Produces: 更短的 L2/L3/L4；测试改为断言「quote_facts」而不是大段底价心算说明

- [x] **Step 1: 改测试契约（先红）**

`customer_specialist_prompt_test.go` 现有必须包含「底价看价格页」「不要说帮客户提交」等长句。改成：

```go
mustContain := []string{
	"quote_facts",
	"自然像在线客服",
	"能短则短",
}
mustNotContain := []string{
	"首次报价绝不能报档位最低价",
	"再低要走申请",
	"底价看价格页，不看自己上一轮报过的价",
}
```

JSON 后缀删掉重复禁令，只留「只输出一个 JSON，answer 非空」。

- [x] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run TestCustomerSpecialistPrompt -count=1`

Expected: FAIL（旧文案还在或新文案不在）。

- [x] **Step 3: 改提示词（目标篇幅）**

`customer_specialist_base.md` 保留：字段说明、JSON schema、软提醒（短、换话题、不转专家）。`必须遵守` 缩到：不编造 quote_facts 以外的价；不提内部字段。泄漏和提交申请交给 veto。

`customer_specialist_pricing.md` 的回答方式改成大约：

```text
有 quote_facts 时，只用里面的 unit_price / monthly_total / speak，用自己的话讲出来。
status=missing_slots 就问 Missing 里的项。
status=at_floor 或 below_floor 就短说这是目前能给的价，不要承诺申请或改订单。
不要解释档位或后台怎么算。
```

`customer_specialist_check.md` 删除第 5 条计价心算（代码已做）。保留安全 / 泄漏自检（模型仍应少生成，veto 兜底）。

`customer_specialist_boundary.md` 补一句：`quote_facts` 是价格事实；服务端否决后不代答，最多重写一次。

SOP 1.1 / 1.2：价格正式事实来自 `quote_facts`；`candidate_pages` 仍用于非价格政策与步骤。删掉「服务端不写死知识页」里与报价表冲突的表述，改成「非价格事实不写死路径；对客价目以 `customer_pricing_table.v1.json` 为准」。

- [x] **Step 4: 再跑测试**

Run: `go test ./internal/service/ -run 'TestCustomerSpecialistPrompt|TestCustomerSpecialistPricingRegression' -count=1`

Expected: PASS。

- [ ] **Step 5: 提交（仅当用户要求）**

```bash
git commit -m "$(cat <<'EOF'
refactor: 客服提示词只留话术，计价交给 quote_facts

EOF
)"
```

---

### Task 5: 会话上下文卫生

**Files:**

- Create: `internal/service/customer_conversation_hygiene.go`
- Create: `internal/service/customer_conversation_hygiene_test.go`
- Modify: `internal/service/customer_router.go`（`formatRouterConversationContext` 先 `SanitizeCustomerHistory`）
- Modify: `internal/service/customer_routed_pipeline.go`（Specialist 上下文同样清洗；`LastQuotedUnitPrice` 走清洗后历史）

**Interfaces:**

- Consumes: `[]ChatMessage`
- Produces: `SanitizeCustomerHistory`、`LastQuotedUnitPrice`

- [x] **Step 1: 写失败测试**

```go
func TestSanitizeCustomerHistoryDropsUserTextLabeledAssistant(t *testing.T) {
	got := SanitizeCustomerHistory([]ChatMessage{
		{Role: "user", Content: "静态IP 5M 15条多少钱"},
		{Role: "assistant", Content: "你好"}, // 测试句被标成 assistant
		{Role: "user", Content: "太贵了"},
	})
	for _, item := range got {
		if item.Role == "assistant" && item.Content == "你好" {
			t.Fatal("short user-like greeting must not stay as assistant quote context")
		}
	}
}

func TestLastQuotedUnitPriceReadsAssistantYuanPerLine(t *testing.T) {
	got := LastQuotedUnitPrice([]ChatMessage{
		{Role: "assistant", Content: "5M 15条可以做到 23 元/条/月。"},
		{Role: "user", Content: "我想20元一条每月"},
	})
	if got != 23 {
		t.Fatalf("last quoted=%d", got)
	}
}
```

清洗规则（写死在测试里）：

- 只保留 `user` / `assistant`。
- assistant 内容若整段等于常见客户寒暄（与 `customerRouterLooksGreetingTurn` 同一集合）且上一轮用户不是在问好，则丢掉（防测试串台）。
- 不把 user 消息改写成 assistant。
- `LastQuotedUnitPrice`：从**最后一条**含 `元/条/月` 的 assistant 消息取最后一个合理整数（1–999）。

- [x] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run 'TestSanitizeCustomerHistory|TestLastQuotedUnitPrice' -count=1`

Expected: FAIL。

- [x] **Step 3: 实现并接到 format context**

`formatRouterConversationContext` 与 `formatCustomerSpecialistConversationContext` 入口先 `SanitizeCustomerHistory`。Task 2 的 `LastQuotedUnitPrice` 改用同一函数。

- [x] **Step 4: 再跑测试**

Run: `go test ./internal/service/ -run 'TestSanitizeCustomerHistory|TestLastQuotedUnitPrice|TestCustomerRouter|TestCustomerSpecialistDecisionPrompt' -count=1`

Expected: PASS。现有寒暄解卡 `Test...UnsticksGreetingAfterPricing` 必须仍绿。

- [ ] **Step 5: 提交（仅当用户要求）**

```bash
git commit -m "$(cat <<'EOF'
fix: 清洗客服历史并按服务端解析上一轮报价

EOF
)"
```

---

### Task 6: 知识页对齐与上线验收

**Files:**

- Modify: `internal/service/testdata/customer_specialist_pricing_regression.json`（数字改成与 `customer_pricing_table.v1.json` 同一档）
- Wiki 仓库（不在本 repo）：`wiki/knowledge/si-ye-tian-static-ip-pricing.md` 及任何仍写「首次最高价 / 再低走申请」的价格页
- 不改 Router 去拉 `si-ye-tian-official-entry-points.md` 当议价证据（Task 2 已在核算成功后丢掉价格页正文）

**Interfaces:**

- Consumes: 锁定后的 `customer_pricing.v1` 数字
- Produces: 回归夹具与 Wiki 口径一致；人工验收清单

- [x] **Step 1: 对齐回归夹具**

把 `pricing_named_floor_after_mid_quote` 等 case 的 candidate 摘要和 `must_include` 改成表里的 `low/high`。跑：

`go test ./internal/service/ -run TestCustomerSpecialistPricingRegressionFixture -count=1`

Expected: PASS。

- [x] **Step 2: 改 Wiki 价格页（外挂知识库）**

删除或改写：首次必须报最高价、再低走申请、客服可提交特批。改成：对客区间与表一致；咨询对话不能代提交。入口链接留在购买页，不要挂在议价检索里。

- [x] **Step 3: 全量测试**

Run: `go test ./internal/service/ ./internal/api/ -count=1`

Expected: PASS。

- [ ] **Step 4: 线上手测清单（部署后）**

用同一会话连续测，不要只截一屏：

1. 「静态IP 5M要15条多少钱」→ 单价等于该档 `high`，单位 `元/条/月`，不说档位。
2. 「太贵了」→ 落到 `low`，不要把上一轮 high 说成已经到底。
3. 「我想20」且 20≥low → 报 20；不要说「已经是 20」。
4. 「我想10」→ 停在 low，不承诺申请。
5. 「你好」→ 只寒暄，不复述议价。
6. 任意轮都不应出现：资料库、帮您提交、系统定价、staticip.html。
7. 打开 `/api/v1/admin/dashboard` 仍应秒回（不要把 git fetch 加回来）。

- [ ] **Step 5: 提交（仅当用户要求；Wiki 页在 Wiki 仓单独提交）**

```bash
git commit -m "$(cat <<'EOF'
test: 对齐价格回归夹具与锁定报价表

EOF
)"
```

---

## 明确不做

- 不用代码生成完整客户回复（包括寒暄、拒价、澄清）。
- 不恢复「帮您提交」整句替换。
- 不把更多销售 / 缺槽位规则加回 Router 硬改写。
- 不在本轮做动态 IP / 海外 IP 全表，除非产品同时给数字。
- 不把 qmd / npm 或交换分区问题并进这条线。

---

## 建议顺序与可交付

| 顺序 | 任务             | 做完后线上应立刻变好的点      |
| ---- | ---------------- | ----------------------------- |
| 1    | 报价引擎         | 本地完成；单测保证查表议价                        |
| 2    | 注入 quote_facts | 本地完成；核算成功不再塞冲突价格页                |
| 3    | 否决不代写       | 本地完成；删空重写一次，不塞套话                  |
| 4    | 瘦提示词         | 本地完成                                          |
| 5    | 上下文卫生       | 本地完成                                          |
| 6    | Wiki + 手测      | 夹具/Wiki 口径/全量单测已完成；线上手测与提交未做 |

1–3 就可以先部署试议价。4 必须在 3 之后，否则一瘦提示词又会漏申请/错价。5 可与 4 并行。6 随时可改 Wiki，不必等代码。

---

## Self-review

**Spec coverage:** 数字归代码 → Task 1–2；提示词只留说话 → Task 4；代码只否决不代写 / 禁止 fallback 循环 → Task 3；脏上下文 → Task 5；知识页打架 → Task 2 丢正文 + Task 6 改页。寒暄解卡沿用现有 Router，不新写套话。

**Placeholder scan:** 价格具体数字要求实现前锁定，计划里的 18–23 只标为形状示例。无 TBD 实现步骤。

**Type consistency:** 全文统一 `CustomerQuoteFacts`、`ResolveCustomerQuote`、`VetoCustomerVisibleAnswer`、`SanitizeCustomerHistory`、`LastQuotedUnitPrice`。
