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

type publicRouterTestLLM struct {
	text     string
	messages []llm.Message
}

func (m *publicRouterTestLLM) Chat(ctx context.Context, model string, messages []llm.Message) (string, error) {
	return m.StreamChat(ctx, model, messages, nil)
}

func (m *publicRouterTestLLM) StreamChat(_ context.Context, _ string, messages []llm.Message, onDelta func(string)) (string, error) {
	m.messages = append([]llm.Message(nil), messages...)
	if onDelta != nil {
		onDelta(m.text)
	}
	return m.text, nil
}

func TestRoutePublicQuestionNormalizesRouterOutput(t *testing.T) {
	llmClient := &publicRouterTestLLM{text: `{
		"specialist": "unknown",
		"intent": "static_ip_price_inquiry",
		"rewritten_question": "",
		"history_summary": "客户之前问过海外 IP，但本轮问静态 IP。",
		"slots": {
			"product": " static_ip ",
			"products": [" static_ip ", "dynamic_ip", "static_ip"],
			"product_resolution": {
				"primary": " static_ip ",
				"all": [" static_ip ", "dynamic_ip", "dynamic_ip"],
				"from_history": true,
				"confidence": 1.4,
				"ambiguous": false,
				"reason": " 用户本轮明确询问静态 IP。 "
			},
			"bandwidth": " 5M "
		},
		"missing_info": ["quantity", "quantity", ""],
		"risk_flags": ["pricing", "pricing"],
		"needs_retrieval": true,
		"retrieval_queries": [],
		"answer_policy": "普通问价。"
	}`}
	svc := NewPublicQueryService(Deps{
		Config:    &config.Config{},
		LLM:       llmClient,
		PromptDir: testPublicRouterPromptDir(t),
	})

	output, _, err := svc.routePublicQuestion(context.Background(), PublicAnswerRequest{Question: "静态IP 怎么卖的?"}, "2026-05-22T10:00:00Z", RuntimePublicQuerySettings{})
	if err != nil {
		t.Fatalf("routePublicQuestion: %v", err)
	}
	if output.Specialist != "product" {
		t.Fatalf("expected unknown specialist to normalize to product, got %q", output.Specialist)
	}
	if output.RewrittenQuestion != "静态IP 怎么卖的?" {
		t.Fatalf("expected missing rewritten question to fall back to original, got %q", output.RewrittenQuestion)
	}
	if output.Slots.Product != "static_ip" || output.Slots.Bandwidth != "5M" {
		t.Fatalf("unexpected slots: %+v", output.Slots)
	}
	if got := strings.Join(output.Slots.Products, ","); got != "static_ip,dynamic_ip" {
		t.Fatalf("expected products to normalize and dedupe, got %+v", output.Slots.Products)
	}
	if output.Slots.ProductResolution.Primary != "static_ip" ||
		strings.Join(output.Slots.ProductResolution.All, ",") != "static_ip,dynamic_ip" ||
		!output.Slots.ProductResolution.FromHistory ||
		output.Slots.ProductResolution.Confidence != 1 ||
		output.Slots.ProductResolution.Ambiguous ||
		output.Slots.ProductResolution.Reason != "用户本轮明确询问静态 IP。" {
		t.Fatalf("unexpected product resolution: %+v", output.Slots.ProductResolution)
	}
	if len(output.MissingInfo) != 1 || output.MissingInfo[0] != "quantity" {
		t.Fatalf("expected missing info to dedupe, got %+v", output.MissingInfo)
	}
	if len(output.RetrievalQueries) != 1 || output.RetrievalQueries[0] != output.RewrittenQuestion {
		t.Fatalf("expected retrieval query fallback, got %+v", output.RetrievalQueries)
	}
}

func TestRoutePublicQuestionDefaultsProductResolution(t *testing.T) {
	output := normalizePublicRouterOutput(PublicRouterOutput{
		RewrittenQuestion: "客户想了解动态 IP 怎么收费。",
		Slots:             PublicRouterSlots{Product: "dynamic_ip"},
	}, PublicAnswerRequest{Question: "动态 IP 怎么卖？"})
	if output.Slots.Product != "dynamic_ip" {
		t.Fatalf("expected product to stay dynamic_ip, got %+v", output.Slots)
	}
	if len(output.Slots.Products) != 1 || output.Slots.Products[0] != "dynamic_ip" {
		t.Fatalf("expected products to default from product, got %+v", output.Slots.Products)
	}
	if output.Slots.ProductResolution.Primary != "dynamic_ip" ||
		len(output.Slots.ProductResolution.All) != 1 ||
		output.Slots.ProductResolution.All[0] != "dynamic_ip" {
		t.Fatalf("expected product resolution to default from product/products, got %+v", output.Slots.ProductResolution)
	}
}

func TestRoutePublicQuestionPromptUsesRecentConversationOnly(t *testing.T) {
	llmClient := &publicRouterTestLLM{text: `{
		"specialist": "pricing",
		"intent": "price",
		"rewritten_question": "客户想了解静态 IP 价格。",
		"history_summary": "",
		"slots": {},
		"missing_info": [],
		"risk_flags": ["pricing"],
		"needs_retrieval": true,
		"retrieval_queries": ["静态 IP 价格"],
		"answer_policy": "普通问价。"
	}`}
	history := make([]ChatMessage, 0, 12)
	for index := 0; index < 12; index++ {
		history = append(history, ChatMessage{Role: "user", Content: "历史问题" + string(rune('A'+index))})
	}
	svc := NewPublicQueryService(Deps{
		Config:    &config.Config{},
		LLM:       llmClient,
		PromptDir: testPublicRouterPromptDir(t),
	})

	if _, _, err := svc.routePublicQuestion(context.Background(), PublicAnswerRequest{Question: "这个多少钱？", History: history}, "2026-05-22T10:00:00Z", RuntimePublicQuerySettings{}); err != nil {
		t.Fatalf("routePublicQuestion: %v", err)
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

func testPublicRouterPromptDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, publicRouterPromptFile), []byte("router prompt"), 0o644); err != nil {
		t.Fatalf("write router prompt: %v", err)
	}
	return dir
}

func boolPtr(value bool) *bool {
	return &value
}
