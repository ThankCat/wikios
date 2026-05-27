package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
)

type publicRoutedPipelineTestLLM struct {
	routerText               string
	routerErr                error
	specialistText           string
	specialistErr            error
	specialistWaitForContext bool
	calls                    []string
	messages                 [][]llm.Message
	models                   []string
}

func (m *publicRoutedPipelineTestLLM) Chat(ctx context.Context, model string, messages []llm.Message) (string, error) {
	return m.StreamChat(ctx, model, messages, nil)
}

func (m *publicRoutedPipelineTestLLM) StreamChat(ctx context.Context, model string, messages []llm.Message, onDelta func(string)) (string, error) {
	system := ""
	if len(messages) > 0 {
		system = messages[0].Content
	}
	m.models = append(m.models, model)
	m.messages = append(m.messages, append([]llm.Message(nil), messages...))
	if strings.Contains(system, "客服经理 Router") {
		m.calls = append(m.calls, "router")
		if m.routerErr != nil {
			return "", m.routerErr
		}
		if onDelta != nil {
			onDelta(m.routerText)
		}
		return m.routerText, nil
	}
	m.calls = append(m.calls, "specialist")
	if m.specialistWaitForContext {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if m.specialistErr != nil {
		return "", m.specialistErr
	}
	if onDelta != nil {
		onDelta(m.specialistText)
	}
	return m.specialistText, nil
}

func TestAnswerRoutedPricingUsesSpecialistAnswer(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:     `{"specialist":"pricing","intent":"static_ip_price_inquiry","rewritten_question":"客户想了解四叶天静态 IP 怎么收费。","history_summary":"","slots":{"product":"static_ip"},"missing_info":["static_type","bandwidth","quantity"],"risk_flags":["pricing"],"needs_retrieval":true,"retrieval_queries":["四叶天 静态 IP 价格"],"answer_policy":"普通问价，只回答公开基础价或起步价。"}`,
		specialistText: `{"answer_mode":"evidence","answer":"我们静态 IP 分共享型和独享型，按月计费。共享型 25 元/个/月起，独享型 300 元/个/月起。您更偏长期固定账号，还是批量业务使用？","review_question":"","confidence":0.9,"evidence_confidence":0.9,"review_required":false,"review_reason":"","suggested_target_path":"","sources":[{"path":"wiki/knowledge/si-ye-tian-static-ip-pricing.md","confidence":"high"}],"notes":""}`,
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	resp, err := svc.answerRouted(context.Background(), "trace-routed-pricing", PublicAnswerRequest{
		Question:   "静态IP 怎么卖的?",
		PersistLog: boolPtr(false),
	}, nil, DefaultRuntimeSettings(svc.deps.Config))
	if err != nil {
		t.Fatalf("answerRouted: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Answer, "共享型 25 元/个/月起") {
		t.Fatalf("expected specialist pricing answer, got %#v", resp)
	}
	if resp.Details == nil || resp.Details["specialist"] != "pricing" {
		t.Fatalf("expected pricing specialist details, got %+v", resp.Details)
	}
	if len(llmClient.calls) != 2 || llmClient.calls[0] != "router" || llmClient.calls[1] != "specialist" {
		t.Fatalf("expected router then specialist calls, got %+v", llmClient.calls)
	}
}

func TestAnswerRoutedUsesConfiguredRouterAndSpecialistModels(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:     `{"specialist":"pricing","intent":"static_ip_price_inquiry","rewritten_question":"客户想了解四叶天静态 IP 怎么收费。","history_summary":"","slots":{"product":"static_ip"},"missing_info":[],"risk_flags":["pricing"],"needs_retrieval":true,"retrieval_queries":["四叶天 静态 IP 价格"],"answer_policy":"普通问价。"}`,
		specialistText: `{"answer_mode":"evidence","answer":"共享型 25 元/个/月起。","review_question":"","confidence":0.9,"evidence_confidence":0.9,"review_required":false,"review_reason":"","suggested_target_path":"","sources":[{"path":"wiki/knowledge/si-ye-tian-static-ip-pricing.md","confidence":"high"}],"notes":""}`,
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	settings := DefaultRuntimeSettings(svc.deps.Config)
	settings.PublicQuery.RouterModelID = "router-fast"
	settings.PublicQuery.SpecialistModelID = "specialist-main"
	resp, err := svc.answerRouted(context.Background(), "trace-routed-models", PublicAnswerRequest{
		Question:   "静态IP 怎么卖的?",
		PersistLog: boolPtr(false),
	}, nil, settings)
	if err != nil {
		t.Fatalf("answerRouted: %v", err)
	}
	if resp == nil || resp.Answer != "共享型 25 元/个/月起。" {
		t.Fatalf("expected specialist answer, got %#v", resp)
	}
	if got := strings.Join(llmClient.models, ","); got != llmModelIDToken("router-fast")+","+llmModelIDToken("specialist-main") {
		t.Fatalf("expected configured router and specialist models, got %s", got)
	}
}

func TestPricingSpecialistPromptDefinesGenericStartingPrice(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "llm", "prompts", "public_specialist_pricing.md"))
	if err != nil {
		t.Fatalf("read pricing prompt: %v", err)
	}
	prompt := string(content)
	if strings.Contains(prompt, "derived_evidence_summary") {
		t.Fatalf("pricing prompt must not reference service-derived evidence summary:\n%s", prompt)
	}
	if !strings.Contains(prompt, "candidate_pages") {
		t.Fatalf("expected pricing prompt to rely on candidate_pages, got:\n%s", prompt)
	}
}

func TestAnswerRoutedKeepsSpecialistAnswerUnchanged(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:     `{"specialist":"product","intent":"static_ip_type_compare","rewritten_question":"客户想比较共享型和独享型静态 IP。","history_summary":"客户前面询问静态 IP 价格。","slots":{"product":"static_ip"},"missing_info":[],"risk_flags":[],"needs_retrieval":true,"retrieval_queries":["共享型 独享型 静态 IP 区别"],"answer_policy":"解释产品差异，不改写为价格兜底。"}`,
		specialistText: `{"answer_mode":"evidence","answer":"共享型静态 IP 是多人共享带宽，起步价更低，适合预算敏感或数量较多的场景，也支持按数量享受折扣；独享型是独立带宽，稳定性更好，适合长期固定账号。您更看重成本还是稳定性？","review_question":"","confidence":0.9,"evidence_confidence":0.9,"review_required":false,"review_reason":"","suggested_target_path":"","sources":[{"path":"wiki/knowledge/si-ye-tian-proxy-ip-products.md","confidence":"high"}],"notes":""}`,
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	resp, err := svc.answerRouted(context.Background(), "trace-routed-product", PublicAnswerRequest{
		Question:   "共享型和独享型有什么区别吗?",
		PersistLog: boolPtr(false),
		History: []ChatMessage{
			{Role: "user", Content: "静态IP 怎么卖的?"},
			{Role: "assistant", Content: "共享型 25 元/个/月起，独享型 300 元/个/月起。"},
		},
	}, nil, DefaultRuntimeSettings(svc.deps.Config))
	if err != nil {
		t.Fatalf("answerRouted: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Answer, "支持按数量享受折扣") || !strings.Contains(resp.Answer, "独享型是独立带宽") {
		t.Fatalf("expected original product specialist answer, got %#v", resp)
	}
	if strings.Contains(resp.Answer, "还需要先确认") {
		t.Fatalf("expected service layer not to replace specialist answer, got %s", resp.Answer)
	}
}

func TestAnswerRoutedPurchaseUsesSpecialistAnswer(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:     `{"specialist":"purchase","intent":"trial_download","rewritten_question":"客户想知道测试 IP 在哪里领取。","history_summary":"","slots":{"product":"proxy_ip"},"missing_info":[],"risk_flags":[],"needs_retrieval":true,"retrieval_queries":["测试 IP 领取 试用"],"answer_policy":"说明测试领取入口和下一步。"}`,
		specialistText: `{"answer_mode":"evidence","answer":"我们测试 IP 一般按页面流程申请或在官方入口领取，先登录后台选择对应产品，再按提示提交测试需求。您要测试动态还是静态 IP？","review_question":"","confidence":0.88,"evidence_confidence":0.9,"review_required":false,"review_reason":"","suggested_target_path":"","sources":[{"path":"wiki/procedures/si-ye-tian-test-trial-procedure.md","confidence":"high"}],"notes":""}`,
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	resp, err := svc.answerRouted(context.Background(), "trace-routed-purchase", PublicAnswerRequest{
		Question:   "测试 IP 在哪里领取？",
		PersistLog: boolPtr(false),
	}, nil, DefaultRuntimeSettings(svc.deps.Config))
	if err != nil {
		t.Fatalf("answerRouted: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Answer, "官方入口领取") {
		t.Fatalf("expected purchase specialist answer, got %#v", resp)
	}
	if resp.Details == nil || resp.Details["specialist"] != "purchase" {
		t.Fatalf("expected routed purchase details, got %+v", resp.Details)
	}
	if len(llmClient.calls) != 2 || llmClient.calls[0] != "router" || llmClient.calls[1] != "specialist" {
		t.Fatalf("expected router then specialist calls, got %+v", llmClient.calls)
	}
}

func TestAnswerRoutedTechnicalUsesSpecialistAnswer(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:     `{"specialist":"technical","intent":"api_whitelist_setup","rewritten_question":"客户想知道怎么添加白名单。","history_summary":"","slots":{"product":"dynamic_ip"},"missing_info":[],"risk_flags":["technical"],"needs_retrieval":true,"retrieval_queries":["添加白名单 API 配置"],"answer_policy":"给出简短配置步骤。"}`,
		specialistText: `{"answer_mode":"evidence","answer":"我们白名单通常先在后台获取当前出口 IP，再添加到授权白名单并保存，随后重新连接代理测试。您现在用的是动态 IP 还是静态 IP？","review_question":"","confidence":0.9,"evidence_confidence":0.9,"review_required":false,"review_reason":"","suggested_target_path":"","sources":[{"path":"wiki/procedures/si-ye-tian-api-whitelist-setup.md","confidence":"high"}],"notes":""}`,
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	resp, err := svc.answerRouted(context.Background(), "trace-routed-technical", PublicAnswerRequest{
		Question:   "怎么添加白名单？",
		PersistLog: boolPtr(false),
	}, nil, DefaultRuntimeSettings(svc.deps.Config))
	if err != nil {
		t.Fatalf("answerRouted: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Answer, "授权白名单") {
		t.Fatalf("expected technical specialist answer, got %#v", resp)
	}
	if resp.Details == nil || resp.Details["specialist"] != "technical" {
		t.Fatalf("expected routed technical details, got %+v", resp.Details)
	}
	if len(llmClient.calls) != 2 || llmClient.calls[0] != "router" || llmClient.calls[1] != "specialist" {
		t.Fatalf("expected router then specialist calls, got %+v", llmClient.calls)
	}
}

func TestAnswerRoutedTroubleshootingUsesSpecialistAnswer(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:     `{"specialist":"troubleshooting","intent":"ip_not_changed","rewritten_question":"客户连接静态 IP 后发现出口 IP 没变。","history_summary":"","slots":{"product":"static_ip"},"missing_info":["tool"],"risk_flags":["troubleshooting"],"needs_retrieval":true,"retrieval_queries":["静态 IP 连接后 IP 没变 排查"],"answer_policy":"先给可执行排查步骤。"}`,
		specialistText: `{"answer_mode":"evidence","answer":"我们可以先按这几步查：确认代理已连接成功，关闭本地直连或分流规则，再用浏览器无痕窗口重新测 IP。您现在用的是什么工具？","review_question":"","confidence":0.86,"evidence_confidence":0.88,"review_required":false,"review_reason":"","suggested_target_path":"","sources":[{"path":"wiki/procedures/si-ye-tian-connection-troubleshooting.md","confidence":"high"}],"notes":""}`,
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	resp, err := svc.answerRouted(context.Background(), "trace-routed-troubleshooting", PublicAnswerRequest{
		Question:   "静态 IP 连接了但是 IP 没变？",
		PersistLog: boolPtr(false),
	}, nil, DefaultRuntimeSettings(svc.deps.Config))
	if err != nil {
		t.Fatalf("answerRouted: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Answer, "无痕窗口重新测 IP") {
		t.Fatalf("expected troubleshooting specialist answer, got %#v", resp)
	}
	if resp.Details == nil || resp.Details["specialist"] != "troubleshooting" {
		t.Fatalf("expected routed troubleshooting details, got %+v", resp.Details)
	}
	if len(llmClient.calls) != 2 || llmClient.calls[0] != "router" || llmClient.calls[1] != "specialist" {
		t.Fatalf("expected router then specialist calls, got %+v", llmClient.calls)
	}
}

func TestAnswerRoutedReceptionUsesSpecialistAnswer(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:     `{"specialist":"reception","intent":"explicit_contact_question","rewritten_question":"客户想了解四叶天客服联系方式。","history_summary":"","slots":{},"missing_info":[],"risk_flags":[],"needs_retrieval":false,"retrieval_queries":[],"answer_policy":"客户明确问联系方式，可以使用公开联系方式。"}`,
		specialistText: `{"answer_mode":"self_answer","answer":"我们客服电话是 400-1080-106，也可以通过企业微信联系。您这边是想咨询购买、配置还是售后问题？","review_question":"","confidence":0.9,"evidence_confidence":0,"review_required":false,"review_reason":"","suggested_target_path":"","sources":[],"notes":""}`,
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	resp, err := svc.answerRouted(context.Background(), "trace-routed-reception", PublicAnswerRequest{
		Question:   "客服电话是多少？",
		PersistLog: boolPtr(false),
	}, nil, DefaultRuntimeSettings(svc.deps.Config))
	if err != nil {
		t.Fatalf("answerRouted: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Answer, "400-1080-106") {
		t.Fatalf("expected reception specialist answer, got %#v", resp)
	}
	if resp.Details == nil || resp.Details["specialist"] != "reception" {
		t.Fatalf("expected routed reception details, got %+v", resp.Details)
	}
	if len(llmClient.calls) != 2 || llmClient.calls[0] != "router" || llmClient.calls[1] != "specialist" {
		t.Fatalf("expected router then specialist calls, got %+v", llmClient.calls)
	}
}

func TestAnswerRoutedBillingAfterSalesUsesSpecialistAnswer(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:     `{"specialist":"billing_after_sales","intent":"invoice_request","rewritten_question":"客户想了解能不能开发票。","history_summary":"","slots":{},"missing_info":[],"risk_flags":["billing"],"needs_retrieval":true,"retrieval_queries":["发票 开票 售后 政策"],"answer_policy":"说明发票处理方式，不承诺固定时效。"}`,
		specialistText: `{"answer_mode":"evidence","answer":"我们支持按规则处理发票需求，通常需要您先确认订单和开票信息，再按页面提示提交。具体类型和时效以当前页面规则为准。","review_question":"","confidence":0.82,"evidence_confidence":0.8,"review_required":false,"review_reason":"","suggested_target_path":"","sources":[{"path":"wiki/policies/si-ye-tian-after-sales-policy.md","confidence":"medium"}],"notes":""}`,
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	resp, err := svc.answerRouted(context.Background(), "trace-routed-billing", PublicAnswerRequest{
		Question:   "能开发票吗？",
		PersistLog: boolPtr(false),
	}, nil, DefaultRuntimeSettings(svc.deps.Config))
	if err != nil {
		t.Fatalf("answerRouted: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Answer, "开票信息") {
		t.Fatalf("expected billing specialist answer, got %#v", resp)
	}
	if resp.Details == nil || resp.Details["specialist"] != "billing_after_sales" {
		t.Fatalf("expected routed billing details, got %+v", resp.Details)
	}
	if len(llmClient.calls) != 2 || llmClient.calls[0] != "router" || llmClient.calls[1] != "specialist" {
		t.Fatalf("expected router then specialist calls, got %+v", llmClient.calls)
	}
}

func TestPublicSpecialistDecisionPromptDoesNotIncludeDerivedEvidence(t *testing.T) {
	svc := newPublicRoutedPipelineTestService(t, &publicRoutedPipelineTestLLM{}, "")
	prompt := svc.publicSpecialistDecisionPrompt(
		PublicAnswerRequest{Question: "静态 IP 怎么卖？"},
		"2026-05-27T10:00:00+08:00",
		&PublicRouterOutput{
			Specialist:        "pricing",
			Intent:            "static_ip_price_inquiry",
			RewrittenQuestion: "客户想了解静态 IP 怎么收费。",
			Slots:             PublicRouterSlots{Product: "static_ip"},
		},
		publicSpecialistEvidenceResult{
			Profile: publicSpecialistProfile("pricing"),
			ContentBlocks: []string{
				"- path: wiki/knowledge/current-static-pricing.md\n  title: 静态 IP 价格\n  confidence: high\n  content: |\n    | IP 类型 | 共享/独享 | 带宽 | 官网原价 |\n    | 数据中心 IP | 独享 | 5M | 300 |",
			},
		},
		RuntimeSupportSettings{},
	)
	if strings.Contains(prompt, "derived_evidence_summary") || strings.Contains(prompt, "共享型最低官网原价") || strings.Contains(prompt, "独享型最低官网原价") {
		t.Fatalf("specialist prompt must not include service-derived evidence, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "candidate_pages:") {
		t.Fatalf("expected specialist prompt to include candidate pages, got:\n%s", prompt)
	}
}

func TestAnswerRoutedReturnsSpecialistAnswerWithoutSanitizeReplacement(t *testing.T) {
	rawAnswer := "请看 wiki/knowledge/internal.md 这条路径。"
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:     `{"specialist":"pricing","intent":"static_ip_price_inquiry","rewritten_question":"客户想了解四叶天静态 IP 怎么收费。","history_summary":"","slots":{"product":"static_ip"},"missing_info":[],"risk_flags":["pricing"],"needs_retrieval":true,"retrieval_queries":["四叶天 静态 IP 价格"],"answer_policy":"普通问价。"}`,
		specialistText: `{"answer_mode":"evidence","answer":"` + rawAnswer + `","review_question":"","confidence":0.9,"evidence_confidence":0.9,"review_required":false,"review_reason":"","suggested_target_path":"","sources":[{"path":"wiki/knowledge/si-ye-tian-static-ip-pricing.md","confidence":"high"}],"notes":""}`,
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	resp, err := svc.answerRouted(context.Background(), "trace-routed-no-sanitize", PublicAnswerRequest{
		Question:   "静态IP 怎么卖的?",
		PersistLog: boolPtr(false),
	}, nil, DefaultRuntimeSettings(svc.deps.Config))
	if err != nil {
		t.Fatalf("answerRouted: %v", err)
	}
	if resp == nil || resp.Answer != rawAnswer {
		t.Fatalf("expected raw specialist answer without sanitize replacement, got %#v", resp)
	}
	if _, ok := resp.Details["sanitizers"]; ok {
		t.Fatalf("routed response must not include sanitizer trace, got %+v", resp.Details["sanitizers"])
	}
}

func TestAnswerRoutedSpecialistFailureReturnsErrorWithoutFallback(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:    `{"specialist":"pricing","intent":"static_ip_price_inquiry","rewritten_question":"客户想了解四叶天静态 IP 怎么收费。","history_summary":"","slots":{"product":"static_ip"},"missing_info":[],"risk_flags":["pricing"],"needs_retrieval":true,"retrieval_queries":["四叶天 静态 IP 价格"],"answer_policy":"普通问价。"}`,
		specialistErr: errors.New("specialist unavailable"),
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	resp, err := svc.answerRouted(context.Background(), "trace-routed-fallback", PublicAnswerRequest{
		Question:   "静态IP 怎么卖的?",
		PersistLog: boolPtr(false),
	}, nil, DefaultRuntimeSettings(svc.deps.Config))
	if err == nil {
		t.Fatalf("expected specialist error, got response %#v", resp)
	}
	if len(llmClient.calls) != 2 || llmClient.calls[0] != "router" || llmClient.calls[1] != "specialist" {
		t.Fatalf("expected router and specialist only, got %+v", llmClient.calls)
	}
}

func TestAnswerRoutedSpecialistEmptyAnswerReturnsErrorWithoutFallback(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:     `{"specialist":"pricing","intent":"static_ip_price_inquiry","rewritten_question":"客户想了解四叶天静态 IP 怎么收费。","history_summary":"","slots":{"product":"static_ip"},"missing_info":[],"risk_flags":["pricing"],"needs_retrieval":true,"retrieval_queries":["四叶天 静态 IP 价格"],"answer_policy":"普通问价。"}`,
		specialistText: `{"answer_mode":"evidence","answer":"","review_question":"","confidence":0.9,"evidence_confidence":0.9,"review_required":false,"review_reason":"","suggested_target_path":"","sources":[{"path":"wiki/knowledge/si-ye-tian-static-ip-pricing.md","confidence":"high"}],"notes":""}`,
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	resp, err := svc.answerRouted(context.Background(), "trace-routed-empty-answer", PublicAnswerRequest{
		Question:   "静态IP 怎么卖的?",
		PersistLog: boolPtr(false),
	}, nil, DefaultRuntimeSettings(svc.deps.Config))
	if err == nil {
		t.Fatalf("expected empty answer error, got response %#v", resp)
	}
	if len(llmClient.calls) != 2 || llmClient.calls[0] != "router" || llmClient.calls[1] != "specialist" {
		t.Fatalf("expected router and specialist only, got %+v", llmClient.calls)
	}
}

func TestAnswerRoutedSpecialistInvalidJSONReturnsErrorWithoutRepairOrFallback(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:     `{"specialist":"pricing","intent":"static_ip_price_inquiry","rewritten_question":"客户想了解四叶天静态 IP 怎么收费。","history_summary":"","slots":{"product":"static_ip"},"missing_info":[],"risk_flags":["pricing"],"needs_retrieval":true,"retrieval_queries":["四叶天 静态 IP 价格"],"answer_policy":"普通问价。"}`,
		specialistText: `不是 JSON，但也不要被服务端当答案发出去`,
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	resp, err := svc.answerRouted(context.Background(), "trace-routed-invalid-json", PublicAnswerRequest{
		Question:   "静态IP 怎么卖的?",
		PersistLog: boolPtr(false),
	}, nil, DefaultRuntimeSettings(svc.deps.Config))
	if err == nil {
		t.Fatalf("expected invalid JSON error, got response %#v", resp)
	}
	if len(llmClient.calls) != 2 || llmClient.calls[0] != "router" || llmClient.calls[1] != "specialist" {
		t.Fatalf("expected router and specialist only, got %+v", llmClient.calls)
	}
}

func TestAnswerRoutedRouterFailureReturnsErrorWithoutFallback(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerErr: errors.New("router unavailable"),
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	resp, err := svc.answerRouted(context.Background(), "trace-router-failed", PublicAnswerRequest{
		Question:   "静态IP 怎么卖的?",
		PersistLog: boolPtr(false),
	}, nil, DefaultRuntimeSettings(svc.deps.Config))
	if err == nil {
		t.Fatalf("expected router error, got response %#v", resp)
	}
	if len(llmClient.calls) != 1 || llmClient.calls[0] != "router" {
		t.Fatalf("expected router only, got %+v", llmClient.calls)
	}
}

func TestAnswerRoutedContextDeadlineDoesNotStartFallback(t *testing.T) {
	llmClient := &publicRoutedPipelineTestLLM{
		routerText:               `{"specialist":"technical","intent":"api_whitelist_setup","rewritten_question":"客户想知道怎么添加白名单。","history_summary":"","slots":{"product":"dynamic_ip"},"missing_info":[],"risk_flags":["technical"],"needs_retrieval":true,"retrieval_queries":["添加白名单 API 配置"],"answer_policy":"给出简短配置步骤。"}`,
		specialistWaitForContext: true,
	}
	svc := newPublicRoutedPipelineTestService(t, llmClient, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	resp, err := svc.answerRouted(ctx, "trace-routed-timeout", PublicAnswerRequest{
		Question:   "API 白名单怎么加？",
		PersistLog: boolPtr(false),
	}, nil, DefaultRuntimeSettings(svc.deps.Config))

	if err == nil {
		t.Fatalf("expected context deadline error, got response %#v", resp)
	}
	if len(llmClient.calls) != 2 || llmClient.calls[0] != "router" || llmClient.calls[1] != "specialist" {
		t.Fatalf("expected router and specialist only, got %+v", llmClient.calls)
	}
}

func newPublicRoutedPipelineTestService(t *testing.T, llmClient llm.Client, promptDir string) *PublicQueryService {
	t.Helper()
	root := t.TempDir()
	writePublicRoutedTestPrompts(t, root, promptDir)
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			raw, err := json.Marshal([]map[string]any{
				{"path": "wiki/knowledge/si-ye-tian-static-ip-pricing.md", "score": 100},
				{"path": "wiki/knowledge/si-ye-tian-proxy-ip-products.md", "score": 90},
				{"path": "wiki/policies/si-ye-tian-safety-boundaries.md", "score": 80},
				{"path": "wiki/procedures/si-ye-tian-test-trial-procedure.md", "score": 75},
				{"path": "wiki/procedures/si-ye-tian-api-whitelist-setup.md", "score": 74},
				{"path": "wiki/procedures/si-ye-tian-connection-troubleshooting.md", "score": 73},
				{"path": "wiki/policies/si-ye-tian-after-sales-policy.md", "score": 72},
			})
			if err != nil {
				return runtime.ToolResult{}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"stdout": string(raw)}}, nil
		}},
		testRuntimeTool{name: "wiki.search_pages"},
		testRuntimeTool{name: "wiki.read_page", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			path, _ := args["path"].(string)
			pages := map[string]string{
				"wiki/knowledge/si-ye-tian-static-ip-pricing.md":               "---\ntitle: 静态 IP 价格\n---\n共享型数据中心 IP：5M 25元/个/月，10M 30元/个/月，20M 70元/个/月起。独享型数据中心 IP：5M 300元/个/月，10M 500元/个/月，20M 800元/个/月。",
				"wiki/knowledge/si-ye-tian-proxy-ip-pricing.md":                "---\ntitle: 代理 IP 价格\n---\n动态代理按套餐计费。",
				"wiki/synthesis/si-ye-tian-purchase-guidance-rules.md":         "---\ntitle: 购买建议\n---\n普通问价只回答公开基础价。",
				"wiki/knowledge/si-ye-tian-proxy-ip-products.md":               "---\ntitle: 产品说明\n---\n动态 IP 适合更换出口，静态 IP 适合固定账号环境。",
				"wiki/policies/si-ye-tian-safety-boundaries.md":                "---\ntitle: 安全边界\n---\n不能承诺特定网站一定可访问。",
				"wiki/procedures/si-ye-tian-purchase-procedure.md":             "---\ntitle: 购买流程\n---\n客户可通过官方入口登录后台，选择产品后按页面提示购买。",
				"wiki/procedures/si-ye-tian-test-trial-procedure.md":           "---\ntitle: 测试试用\n---\n测试 IP 可在官方入口或后台按流程申请，并提交产品类型和测试需求。",
				"wiki/procedures/si-ye-tian-download-installation.md":          "---\ntitle: 下载安装\n---\n客户端下载以官方入口展示为准。",
				"wiki/synthesis/si-ye-tian-official-entry-points.md":           "---\ntitle: 官方入口\n---\n购买、下载和测试应通过官方入口进行。",
				"wiki/procedures/si-ye-tian-api-whitelist-setup.md":            "---\ntitle: API 白名单\n---\n先获取当前出口 IP，添加到后台授权白名单并保存，再重新连接代理测试。",
				"wiki/procedures/si-ye-tian-dynamic-ip-usage.md":               "---\ntitle: 动态 IP 使用\n---\n动态 IP 可按后台配置方式连接使用。",
				"wiki/procedures/si-ye-tian-third-party-tool-configuration.md": "---\ntitle: 第三方工具配置\n---\n第三方工具需按代理协议、地址、端口和认证信息配置。",
				"wiki/procedures/si-ye-tian-device-network-configuration.md":   "---\ntitle: 设备网络配置\n---\n设备网络配置需确认代理连接和本地网络规则。",
				"wiki/procedures/si-ye-tian-connection-troubleshooting.md":     "---\ntitle: 连接排障\n---\nIP 没变时先确认代理连接成功，关闭本地直连或分流规则，再重新测试出口 IP。",
				"wiki/policies/si-ye-tian-after-sales-policy.md":               "---\ntitle: 售后政策\n---\n售后问题需按页面规则处理。",
			}
			content := pages[path]
			if content == "" {
				return runtime.ToolResult{Success: false, RiskLevel: runtime.RiskLow, Error: &runtime.ToolError{Code: "NOT_FOUND", Message: "not found"}}, nil
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": content}}, nil
		}},
	)
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{Root: root, QMDIndex: "test"},
		PublicQuery: config.PublicQueryConfig{CandidateTopK: 4, MaxEvidenceChars: 1800},
	}
	return NewPublicQueryService(Deps{
		Config:    cfg,
		Runtime:   rt,
		LLM:       llmClient,
		Retriever: retrieval.NewQMDRetriever(rt),
		PromptDir: firstNonEmpty(promptDir, root),
	})
}

func writePublicRoutedTestPrompts(t *testing.T, root string, promptDir string) {
	t.Helper()
	dir := firstNonEmpty(promptDir, root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir prompt dir: %v", err)
	}
	prompts := map[string]string{
		publicRouterPromptFile:                     "你是四叶天 public answer 的“客服经理 Router”。",
		"public_specialist_pricing.md":             "你是四叶天代理 IP 的价格套餐客服。",
		"public_specialist_product.md":             "你是四叶天代理 IP 的产品选型客服。",
		"public_specialist_safety.md":              "你是四叶天代理 IP 的安全边界客服。",
		"public_specialist_purchase.md":            "你是四叶天代理 IP 的购买开通客服。",
		"public_specialist_technical.md":           "你是四叶天代理 IP 的技术配置客服。",
		"public_specialist_troubleshooting.md":     "你是四叶天代理 IP 的故障排查客服。",
		"public_specialist_reception.md":           "你是四叶天代理 IP 的前台接待客服。",
		"public_specialist_billing_after_sales.md": "你是四叶天代理 IP 的账号财务售后客服。",
	}
	for name, content := range prompts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write prompt %s: %v", name, err)
		}
	}
}
