package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
	"wikios/internal/llm"
)

type customerRouterTestLLM struct {
	text     string
	messages []llm.Message
}

func (m *customerRouterTestLLM) Chat(ctx context.Context, model string, messages []llm.Message) (string, error) {
	return m.StreamChat(ctx, model, messages, nil)
}

func (m *customerRouterTestLLM) StreamChat(_ context.Context, _ string, messages []llm.Message, onDelta func(string)) (string, error) {
	m.messages = append([]llm.Message(nil), messages...)
	if onDelta != nil {
		onDelta(m.text)
	}
	return m.text, nil
}

func TestRouteCustomerQuestionNormalizesRouterOutputV1(t *testing.T) {
	llmClient := &customerRouterTestLLM{text: `{
		"contract_version": "customer_router.v1",
		"specialist": "unknown",
		"routing_confidence": 0.42,
		"routing_reason": " 用户本轮明确询问静态 IP。 ",
		"intent": "static_ip_price_inquiry",
		"rewritten_question": "",
		"history_summary": "客户之前问过海外 IP，但本轮问静态 IP。",
		"slots": {
			"primary_product": " static_ip ",
			"products": [" static_ip ", "dynamic_ip", "static_ip"],
			"static_type": "",
			"ip_type": "",
			"bandwidth": " 5M ",
			"quantity": "",
			"scenario": "",
			"platform": "",
			"device": "",
			"error_code": ""
		},
		"ambiguity": {
			"is_ambiguous": false,
			"ambiguous_fields": ["primary_product", "primary_product"],
			"reason": " 用户本轮明确询问静态 IP。 "
		},
		"missing_info": ["quantity", "quantity", ""],
		"risk_flags": ["pricing", "pricing"],
		"needs_retrieval": true,
		"retrieval_queries": [" 四叶天 静态 IP 价格 ", "四叶天 静态 IP 价格"],
		"handoff_notes": " 普通静态 IP 问价。 "
	}`}
	svc := NewCustomerChatService(Deps{
		Config:    &config.Config{},
		LLM:       llmClient,
		PromptDir: testCustomerRouterPromptDir(t),
	})

	output, _, _, err := svc.routeCustomerQuestion(context.Background(), CustomerChatRequest{Question: "静态IP 怎么卖的?"}, "2026-05-22T10:00:00Z", RuntimeCustomerQuerySettings{})
	if err != nil {
		t.Fatalf("routeCustomerQuestion: %v", err)
	}
	if output.ContractVersion != customerRouterContractVersion {
		t.Fatalf("expected contract version %q, got %q", customerRouterContractVersion, output.ContractVersion)
	}
	if output.Specialist != "product" {
		t.Fatalf("expected unknown specialist to normalize to product, got %q", output.Specialist)
	}
	if output.RoutingConfidence != 0.42 || output.RoutingReason != "用户本轮明确询问静态 IP。" {
		t.Fatalf("unexpected routing fields: confidence=%v reason=%q", output.RoutingConfidence, output.RoutingReason)
	}
	if !containsString(output.RiskFlags, "low_confidence") {
		t.Fatalf("expected low confidence risk flag, got %+v", output.RiskFlags)
	}
	if output.RewrittenQuestion != "静态IP 怎么卖的?" {
		t.Fatalf("expected missing rewritten question to fall back to original, got %q", output.RewrittenQuestion)
	}
	if output.Slots.PrimaryProduct != "static_ip" || output.Slots.Bandwidth != "5M" {
		t.Fatalf("unexpected slots: %+v", output.Slots)
	}
	if got := strings.Join(output.Slots.Products, ","); got != "static_ip,dynamic_ip" {
		t.Fatalf("expected products to normalize and dedupe, got %+v", output.Slots.Products)
	}
	if output.Ambiguity.IsAmbiguous || strings.Join(output.Ambiguity.AmbiguousFields, ",") != "primary_product" || output.Ambiguity.Reason != "用户本轮明确询问静态 IP。" {
		t.Fatalf("unexpected ambiguity: %+v", output.Ambiguity)
	}
	if len(output.MissingInfo) != 1 || output.MissingInfo[0] != "quantity" {
		t.Fatalf("expected missing info to dedupe, got %+v", output.MissingInfo)
	}
	if len(output.RetrievalQueries) != 1 || output.RetrievalQueries[0] != "四叶天 静态 IP 价格" {
		t.Fatalf("expected retrieval query to normalize, got %+v", output.RetrievalQueries)
	}
	if output.HandoffNotes != "普通静态 IP 问价。" {
		t.Fatalf("unexpected handoff notes: %q", output.HandoffNotes)
	}
}

func TestRouteCustomerQuestionDefaultsV1Slots(t *testing.T) {
	output := normalizeCustomerRouterOutput(CustomerRouterOutput{
		RewrittenQuestion: "客户想了解动态 IP 怎么收费。",
		Slots:             CustomerRouterSlots{PrimaryProduct: "dynamic_ip"},
	}, CustomerChatRequest{Question: "动态 IP 怎么卖？"})
	if output.ContractVersion != customerRouterContractVersion {
		t.Fatalf("expected contract version to default, got %+v", output.ContractVersion)
	}
	if output.Slots.PrimaryProduct != "dynamic_ip" {
		t.Fatalf("expected primary product to stay dynamic_ip, got %+v", output.Slots)
	}
	if len(output.Slots.Products) != 1 || output.Slots.Products[0] != "dynamic_ip" {
		t.Fatalf("expected products to default from primary product, got %+v", output.Slots.Products)
	}
}

func TestRouteCustomerQuestionRequiresRetrievalQueryWhenNeeded(t *testing.T) {
	llmClient := &customerRouterTestLLM{text: `{
		"contract_version": "customer_router.v1",
		"specialist": "pricing",
		"routing_confidence": 0.9,
		"routing_reason": "用户询问价格。",
		"intent": "price",
		"rewritten_question": "客户想了解静态 IP 价格。",
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
		"ambiguity": {"is_ambiguous": false, "ambiguous_fields": [], "reason": ""},
		"missing_info": [],
		"risk_flags": ["pricing"],
		"needs_retrieval": true,
		"retrieval_queries": [],
		"handoff_notes": "普通问价。"
	}`}
	svc := NewCustomerChatService(Deps{Config: &config.Config{}, LLM: llmClient, PromptDir: testCustomerRouterPromptDir(t)})
	_, _, _, err := svc.routeCustomerQuestion(context.Background(), CustomerChatRequest{Question: "静态 IP 价格"}, "2026-05-22T10:00:00Z", RuntimeCustomerQuerySettings{})
	if err == nil || !strings.Contains(err.Error(), "retrieval_queries is empty") {
		t.Fatalf("expected retrieval query validation error, got %v", err)
	}
}

func TestRouteCustomerQuestionRejectsInvalidContractVersion(t *testing.T) {
	llmClient := &customerRouterTestLLM{text: `{
		"contract_version": "customer_router.legacy",
		"specialist": "pricing",
		"routing_confidence": 0.9,
		"routing_reason": "用户询问价格。",
		"intent": "price",
		"rewritten_question": "客户想了解静态 IP 价格。",
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
		"ambiguity": {"is_ambiguous": false, "ambiguous_fields": [], "reason": ""},
		"missing_info": [],
		"risk_flags": ["pricing"],
		"needs_retrieval": true,
		"retrieval_queries": ["静态 IP 价格"],
		"handoff_notes": "用户是普通静态 IP 问价。"
	}`}
	svc := NewCustomerChatService(Deps{Config: &config.Config{}, LLM: llmClient, PromptDir: testCustomerRouterPromptDir(t)})
	_, _, _, err := svc.routeCustomerQuestion(context.Background(), CustomerChatRequest{Question: "静态 IP 价格"}, "2026-05-22T10:00:00Z", RuntimeCustomerQuerySettings{})
	if err == nil || !strings.Contains(err.Error(), "contract_version") {
		t.Fatalf("expected contract version validation error, got %v", err)
	}
}

func TestCustomerRouterResponseFormatRequiresV1Fields(t *testing.T) {
	format := customerRouterResponseFormat()
	if format == nil || format.JSONSchema == nil {
		t.Fatalf("expected router response format schema")
	}
	schema := format.JSONSchema.Schema
	required, _ := schema["required"].([]any)
	for _, want := range []string{"contract_version", "routing_confidence", "routing_reason", "ambiguity", "handoff_notes"} {
		if !containsAnyValue(required, want) {
			t.Fatalf("expected router schema to require %q, got %+v", want, required)
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties["answer_"+"policy"]; ok {
		t.Fatalf("router schema must not expose legacy answer policy")
	}
	if _, ok := properties["product_"+"resolution"]; ok {
		t.Fatalf("router schema must not expose legacy product resolution")
	}
}

func TestRouteCustomerQuestionPromptUsesRecentConversationOnly(t *testing.T) {
	llmClient := &customerRouterTestLLM{text: `{
		"contract_version": "customer_router.v1",
		"specialist": "pricing",
		"routing_confidence": 0.9,
		"routing_reason": "用户询问价格。",
		"intent": "price",
		"rewritten_question": "客户想了解静态 IP 价格。",
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
		"ambiguity": {"is_ambiguous": false, "ambiguous_fields": [], "reason": ""},
		"missing_info": [],
		"risk_flags": ["pricing"],
		"needs_retrieval": true,
		"retrieval_queries": ["静态 IP 价格"],
		"handoff_notes": "普通问价。"
	}`}
	history := make([]ChatMessage, 0, 12)
	for index := 0; index < 12; index++ {
		history = append(history, ChatMessage{Role: "user", Content: "历史问题" + string(rune('A'+index))})
	}
	svc := NewCustomerChatService(Deps{
		Config:    &config.Config{},
		LLM:       llmClient,
		PromptDir: testCustomerRouterPromptDir(t),
	})

	if _, _, _, err := svc.routeCustomerQuestion(context.Background(), CustomerChatRequest{Question: "这个多少钱？", History: history}, "2026-05-22T10:00:00Z", RuntimeCustomerQuerySettings{}); err != nil {
		t.Fatalf("routeCustomerQuestion: %v", err)
	}
	if len(llmClient.messages) != 2 {
		t.Fatalf("expected system and user messages, got %+v", llmClient.messages)
	}
	userPrompt := llmClient.messages[1].Content
	if strings.Contains(userPrompt, "历史问题A") || strings.Contains(userPrompt, "历史问题B") {
		t.Fatalf("expected router prompt to drop oldest history, got %q", userPrompt)
	}
	if !strings.Contains(userPrompt, "历史问题C") || !strings.Contains(userPrompt, "历史问题L") {
		t.Fatalf("expected router prompt to keep latest 10 history turns, got %q", userPrompt)
	}
}

func containsAnyValue(items []any, want string) bool {
	for _, item := range items {
		if value, ok := item.(string); ok && value == want {
			return true
		}
	}
	return false
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func testCustomerRouterPromptDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, customerRouterPromptFile), []byte("router prompt"), 0o644); err != nil {
		t.Fatalf("write router prompt: %v", err)
	}
	return dir
}

func boolPtr(value bool) *bool {
	return &value
}
