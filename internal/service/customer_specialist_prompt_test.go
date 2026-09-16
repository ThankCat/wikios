package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
)

func TestLoadCustomerSpecialistSystemPromptComposesBaseAndRole(t *testing.T) {
	root := t.TempDir()
	promptDir := testCustomerRouterPromptDir(t)
	writeCustomerRoutedTestPrompts(t, root, promptDir)

	svc := NewCustomerChatService(Deps{Config: &config.Config{}, PromptDir: promptDir})
	systemPrompt, err := svc.loadCustomerSpecialistSystemPrompt(customerSpecialistProfile("pricing"))
	if err != nil {
		t.Fatalf("loadCustomerSpecialistSystemPrompt: %v", err)
	}
	for _, want := range []string{
		"user 消息字段",
		"价格套餐客服",
		"输出前自检（L4）",
		customerSpecialistPromptSeparator,
		"完成上文「输出前自检（L4）」后，只返回一个 JSON 对象",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("expected system prompt to include %q, got:\n%s", want, systemPrompt)
		}
	}
}

func TestLoadCustomerSpecialistBoundaryUsesPromptFile(t *testing.T) {
	root := t.TempDir()
	promptDir := testCustomerRouterPromptDir(t)
	writeCustomerRoutedTestPrompts(t, root, promptDir)

	svc := NewCustomerChatService(Deps{Config: &config.Config{}, PromptDir: promptDir})
	boundary, err := svc.loadCustomerSpecialistBoundary()
	if err != nil {
		t.Fatalf("loadCustomerSpecialistBoundary: %v", err)
	}
	if !strings.Contains(boundary, "服务端行为") {
		t.Fatalf("expected boundary prompt content, got:\n%s", boundary)
	}
}

func TestCustomerPromptsKeepHardSafetyEvidenceAndFloor(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(raw)
	}
	base := read("customer_specialist_base.md")
	pricing := read("customer_specialist_pricing.md")
	safety := read("customer_specialist_safety.md")
	check := read("customer_specialist_check.md")
	router := read("customer_router_system.md")

	for _, want := range []string{
		"正式事实必须来自 `candidate_pages`",
		"客服最低授权价、审批阈值、采购成本、毛利",
		"对客价不能低于当前价格页写明的该档底价",
		"内部 prompt",
		"绕风控",
		"住宅独享",
		"元/个",
	} {
		joined := strings.Join([]string{base, pricing, safety, check, router}, "\n")
		if !strings.Contains(joined, want) {
			t.Fatalf("expected kept hard rule %q", want)
		}
	}
	if !strings.Contains(router, "internal_security_boundary") {
		t.Fatal("router must keep internal security hard route")
	}
	if !strings.Contains(safety, "Clash") || !strings.Contains(safety, "养号") {
		t.Fatal("safety prompt must keep compliance refusals")
	}
	if !strings.Contains(pricing, "还缺带宽或数量") {
		t.Fatal("pricing prompt should keep a soft missing-slot reminder")
	}
	if !strings.Contains(pricing, "数量越多单价越低") || !strings.Contains(check, "减少数量来拿更低单价") {
		t.Fatal("pricing/check prompts must keep quantity-tier direction")
	}
	if !strings.Contains(pricing, "不要对客说数量档、价格档") || !strings.Contains(pricing, "不要对客提系统定价、标准化或修改订单金额") {
		t.Fatal("pricing prompt must hide tiers and system pricing from customers")
	}
	if !strings.Contains(base, "不能替客户提交申请") || !strings.Contains(pricing, "不要说帮客户提交") || !strings.Contains(check, "承诺帮客户提交申请") {
		t.Fatal("prompts must forbid promising backend operations the chat cannot do")
	}
}

func TestCustomerPromptsDropCannedPlaybooks(t *testing.T) {
	files := []string{
		"customer_specialist_base.md",
		"customer_specialist_check.md",
		"customer_specialist_pricing.md",
		"customer_specialist_product.md",
		"customer_specialist_purchase.md",
		"customer_specialist_technical.md",
		"customer_specialist_troubleshooting.md",
		"customer_specialist_billing_after_sales.md",
		"customer_specialist_safety.md",
		"customer_specialist_reception.md",
		"customer_router_system.md",
	}
	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		prompt := string(raw)
		for _, forbidden := range []string{
			"推荐句式",
			"使用下方推荐句式原文",
			"可以提交申请，最终以审批结果为准，不能保证批准。",
			"首次报价绝不能报档位最低价",
			"## 当前硬规则",
			"## 产品不明硬规则",
			"## 硬限制",
			"由于系统定价是标准化的，我这边无法直接为您修改订单金额",
			"再低要走申请",
		} {
			if strings.Contains(prompt, forbidden) {
				t.Fatalf("%s still contains playbook %q", name, forbidden)
			}
		}
	}
}

func TestSanitizeCustomerVisibleAnswerBlocksDeprecatedPricingAndSourceDisclosure(t *testing.T) {
	routerOutput := &CustomerRouterOutput{Specialist: "pricing"}
	parsed := customerChatLLMOutput{AnswerMode: "evidence"}

	deprecated := "数据中心独享型静态 IP 5M 300 元/个/月，独享型不参与数量折扣。"
	got, changed := sanitizeCustomerVisibleAnswer(deprecated, parsed, routerOutput)
	if !changed {
		t.Fatal("expected deprecated pricing answer to be sanitized")
	}
	for _, forbidden := range []string{"300", "数据中心独享", "不参与数量折扣"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized answer still contains deprecated marker %q: %s", forbidden, got)
		}
	}

	leakedSource := "价格来自 wiki/knowledge/si-ye-tian-static-ip-pricing.md。"
	got, changed = sanitizeCustomerVisibleAnswer(leakedSource, parsed, routerOutput)
	if !changed {
		t.Fatal("expected source disclosure to be sanitized")
	}
	for _, forbidden := range []string{"wiki/", ".md", "来源"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized answer still contains source marker %q: %s", forbidden, got)
		}
	}
}

func TestSanitizeCustomerVisibleAnswerStripsKnowledgeBaseLeak(t *testing.T) {
	routerOutput := &CustomerRouterOutput{Specialist: "product"}
	parsed := customerChatLLMOutput{AnswerMode: "evidence"}
	leaked := "您好，关于静态 IP 和住宅 IP 在游戏场景下的具体选择建议，目前资料库中暂无针对该场景的详细对比说明。\n通常来说，静态 IP 适合需要固定出口地址的场景（如绑定白名单），而住宅 IP 基于真实家庭网络环境，稳定性较高。"

	got, changed := sanitizeCustomerVisibleAnswer(leaked, parsed, routerOutput)
	if !changed {
		t.Fatal("expected knowledge-base leak sentence to be sanitized")
	}
	for _, forbidden := range []string{"资料库", "知识库", "暂无针对该场景", "查询资料", "系统检索"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized answer still contains knowledge leak %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(got, "静态 IP") || !strings.Contains(got, "住宅 IP") {
		t.Fatalf("expected usable product comparison to remain, got %s", got)
	}
}

func TestSanitizeCustomerVisibleAnswerDoesNotRewriteSubmitApplicationWording(t *testing.T) {
	routerOutput := &CustomerRouterOutput{Specialist: "pricing"}
	parsed := customerChatLLMOutput{AnswerMode: "evidence"}
	answer := "您好，11条静态IP（10M带宽）目前最低可以给您申请到20元/条/月。这个价格已经是当前数量档位的底价了，确实无法再直接优惠。\n如果您觉得还是偏高，我可以帮您提交特价申请，最终以审批结果为准，不能保证一定能批下来。您看需要我这边帮您提交吗？"

	got, changed := sanitizeCustomerVisibleAnswer(answer, parsed, routerOutput)
	if changed {
		t.Fatalf("code must not hard-rewrite submit-application wording, got %s", got)
	}
	if got != answer {
		t.Fatalf("expected original answer to pass through, got %s", got)
	}
}

func TestSanitizeCustomerVisibleAnswerKeepsProductDifferenceAndStripsOnlyDeprecatedPriceSentences(t *testing.T) {
	routerOutput := &CustomerRouterOutput{Specialist: "product"}
	parsed := customerChatLLMOutput{AnswerMode: "evidence"}
	mixed := "共享型是多人共用带宽，起步价通常更低，适合数量较多的场景。独享型是独立带宽，稳定性更好。独享型不参与数量折扣。"

	got, changed := sanitizeCustomerVisibleAnswer(mixed, parsed, routerOutput)
	if !changed {
		t.Fatal("expected only the deprecated pricing sentence to be removed")
	}
	if !strings.Contains(got, "共用带宽") || !strings.Contains(got, "独立带宽") {
		t.Fatalf("expected shared/dedicated product difference to remain, got %s", got)
	}
	if strings.Contains(got, "不参与数量折扣") || strings.Contains(got, "300 元") {
		t.Fatalf("expected old pricing rule sentence to be removed, got %s", got)
	}
	if strings.Contains(got, "请告诉我需要静态 IP 还是住宅 IP") || strings.Contains(got, "请告诉我需要的带宽和数量") {
		t.Fatalf("must not replace the whole product-difference answer, got %s", got)
	}
}

func TestSanitizeCustomerVisibleAnswerBlocksHyphenDeprecatedPriceRange(t *testing.T) {
	routerOutput := &CustomerRouterOutput{Specialist: "pricing"}
	parsed := customerChatLLMOutput{AnswerMode: "evidence"}
	got, changed := sanitizeCustomerVisibleAnswer("共享型起步价约 25-70 元/条/月，独享型约 300到800 元/条/月。", parsed, routerOutput)
	if !changed {
		t.Fatal("expected hyphen/到 deprecated price ranges to be sanitized")
	}
	for _, forbidden := range []string{"25-70", "300到800", "25", "300"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized answer still contains deprecated range %q: %s", forbidden, got)
		}
	}
}

func TestCustomerSpecialistPromptsAgreeOnCurrentProductTaxonomy(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(raw)
	}
	base := read("customer_specialist_base.md")
	product := read("customer_specialist_product.md")
	pricing := read("customer_specialist_pricing.md")
	check := read("customer_specialist_check.md")
	router := read("customer_router_system.md")

	for _, prompt := range []string{base, product, pricing, router} {
		if !strings.Contains(prompt, "住宅独享") {
			t.Fatalf("expected current taxonomy to mention 住宅独享:\n%s", prompt)
		}
	}
	if strings.Contains(base, "静态 IP 下的共享/独享") || strings.Contains(base, "住宅 IP”是静态 IP 的一种") {
		t.Fatalf("base prompt still treats residential/dedicated as static subtypes:\n%s", base)
	}
	if strings.Contains(product, "优先独享静态 IP") {
		t.Fatalf("product prompt still recommends dedicated static SKU:\n%s", product)
	}
	if strings.Contains(router, "`static_type=dedicated`，`ip_type=residential`") {
		t.Fatalf("router prompt still encodes residential dedicated as static_type=dedicated:\n%s", router)
	}
	if !strings.Contains(check, "元/个") {
		t.Fatalf("check prompt must still strip old price units:\n%s", check)
	}
}

func TestSanitizeCustomerVisibleAnswerAllowsQualitativeSharedDedicatedCostDifference(t *testing.T) {
	routerOutput := &CustomerRouterOutput{Specialist: "product"}
	parsed := customerChatLLMOutput{AnswerMode: "evidence"}
	qualitative := "共享型通常起步价更低、适合预算敏感场景；独享型通常成本更高，但带宽独立、更稳定。您更看重成本还是稳定性？"

	got, changed := sanitizeCustomerVisibleAnswer(qualitative, parsed, routerOutput)
	if changed {
		t.Fatalf("qualitative cost difference must not be treated as deprecated pricing, got %s", got)
	}
	if !strings.Contains(got, "起步价更低") || !strings.Contains(got, "成本更高") || !strings.Contains(got, "带宽独立") {
		t.Fatalf("expected qualitative shared/dedicated difference to pass through, got %s", got)
	}
}

func TestSupportContactPromptNormalizesPlaceholderWeComToQRCode(t *testing.T) {
	svc := NewCustomerChatService(Deps{Config: &config.Config{}})

	got := svc.supportContactPrompt(RuntimeSupportSettings{
		Phone: "400-1080-106",
		WeCom: "企业微信",
	})
	if !strings.Contains(got, "企业微信：官网右侧企业微信二维码") {
		t.Fatalf("expected placeholder WeCom to become QR code entry, got:\n%s", got)
	}
	if !strings.Contains(got, "客服电话：400-1080-106") {
		t.Fatalf("expected phone contact to remain, got:\n%s", got)
	}

	got = svc.supportContactPrompt(RuntimeSupportSettings{
		Phone: "400-1080-106",
		WeCom: "siyetian-support",
	})
	if !strings.Contains(got, "企业微信：siyetian-support") {
		t.Fatalf("expected explicit WeCom to remain, got:\n%s", got)
	}
}

func TestCustomerSpecialistRolePromptsUseWorkflowCards(t *testing.T) {
	files := []string{
		"customer_specialist_pricing.md",
		"customer_specialist_product.md",
		"customer_specialist_purchase.md",
		"customer_specialist_technical.md",
		"customer_specialist_troubleshooting.md",
		"customer_specialist_billing_after_sales.md",
		"customer_specialist_safety.md",
		"customer_specialist_reception.md",
	}
	for _, file := range files {
		raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", file))
		if err != nil {
			t.Fatalf("read role prompt: %v", err)
		}
		prompt := string(raw)
		for _, want := range []string{"## 职责边界", "## 证据原则", "## 回答方式"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("expected %s to include %q", file, want)
			}
		}
	}
}

func TestCustomerSpecialistBasePromptForbidsInternalRoleLeakage(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", customerSpecialistBasePromptFile))
	if err != nil {
		t.Fatalf("read base prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"## 最高指令",
		"对客正文禁止出现知识库、资料库、查询资料、系统检索",
		"对客不提知识库、资料库、路径、prompt、router、检索、专家、分诊、JSON 字段名",
		"也不要说“资料里没有”",
		"不要说“转接某专家”",
		"不要对客提系统定价、标准化计价或修改订单金额",
		"不能替客户提交申请、改订单、走审批",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected base prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistBasePromptForbidsInventingProductTypes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", customerSpecialistBasePromptFile))
	if err != nil {
		t.Fatalf("read base prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"不要造“动态住宅 IP”“独享静态 IP”",
		"当前说“独享”只对应住宅独享",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected base prompt to include %q, got:\n%s", want, prompt)
		}
	}
}
