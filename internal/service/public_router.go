package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"wikios/internal/llm"
)

const publicRouterPromptFile = "public_router_system.md"

type PublicRouterOutput struct {
	Specialist        string            `json:"specialist"`
	Intent            string            `json:"intent"`
	RewrittenQuestion string            `json:"rewritten_question"`
	HistorySummary    string            `json:"history_summary"`
	Slots             PublicRouterSlots `json:"slots"`
	MissingInfo       []string          `json:"missing_info"`
	RiskFlags         []string          `json:"risk_flags"`
	NeedsRetrieval    bool              `json:"needs_retrieval"`
	RetrievalQueries  []string          `json:"retrieval_queries"`
	AnswerPolicy      string            `json:"answer_policy"`
}

type PublicRouterSlots struct {
	Product           string                  `json:"product"`
	Products          []string                `json:"products,omitempty"`
	ProductResolution PublicProductResolution `json:"product_resolution,omitempty"`
	StaticType        string                  `json:"static_type"`
	IPType            string                  `json:"ip_type"`
	Bandwidth         string                  `json:"bandwidth"`
	Quantity          string                  `json:"quantity"`
	Scenario          string                  `json:"scenario"`
	Platform          string                  `json:"platform"`
	Device            string                  `json:"device"`
	ErrorCode         string                  `json:"error_code"`
}

type PublicProductResolution struct {
	Primary     string   `json:"primary,omitempty"`
	All         []string `json:"all,omitempty"`
	FromHistory bool     `json:"from_history,omitempty"`
	Confidence  float64  `json:"confidence,omitempty"`
	Ambiguous   bool     `json:"ambiguous,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

type publicRouterTraceResult struct {
	Output      *PublicRouterOutput `json:"output,omitempty"`
	Error       string              `json:"error,omitempty"`
	RawChars    int                 `json:"raw_chars,omitempty"`
	PromptChars int                 `json:"prompt_chars,omitempty"`
}

func (s *PublicQueryService) routePublicQuestion(ctx context.Context, req PublicAnswerRequest, receivedAt string, settings RuntimePublicQuerySettings) (*PublicRouterOutput, string, error) {
	systemPrompt, err := s.loadPrompt(publicRouterPromptFile)
	if err != nil {
		return nil, "", err
	}
	userPrompt := publicRouterUserPrompt(req, receivedAt)
	text, _, err := s.executeLLMTraceWithOptionsAndResponseFormat(ctx, nil, llmModelIDToken(settings.RouterModelID), []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, "llm public router", nil, settings.RouterEnableThinking, publicRouterResponseFormat())
	if err != nil {
		return nil, text, err
	}
	var output PublicRouterOutput
	if err := llm.DecodeJSONObject(text, &output); err != nil {
		return nil, text, fmt.Errorf("decode public router output: %w", err)
	}
	return normalizePublicRouterOutput(output, req), text, nil
}

func publicRouterUserPrompt(req PublicAnswerRequest, receivedAt string) string {
	return strings.Join([]string{
		"current_time:",
		receivedAt,
		"",
		"current_public_time:",
		formatPublicBeijingTime(receivedAt),
		"",
		"user_message:",
		strings.TrimSpace(req.Question),
		"",
		"conversation_context:",
		formatRouterConversationContext(req.History, 10),
	}, "\n")
}

func formatRouterConversationContext(history []ChatMessage, maxTurns int) string {
	if len(history) == 0 {
		return "[]"
	}
	if maxTurns <= 0 {
		maxTurns = 10
	}
	start := 0
	if len(history) > maxTurns {
		start = len(history) - maxTurns
	}
	lines := make([]string, 0, len(history)-start)
	for _, item := range history[start:] {
		role := strings.ToLower(strings.TrimSpace(item.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		block := []string{"-"}
		if timeText := strings.TrimSpace(item.CreatedAt); timeText != "" {
			block = append(block, "  created_at: "+timeText)
		}
		block = append(block, "  role: "+role, "  content: |")
		for _, line := range strings.Split(truncateForPrompt(content, 400), "\n") {
			block = append(block, "    "+line)
		}
		lines = append(lines, strings.Join(block, "\n"))
	}
	if len(lines) == 0 {
		return "[]"
	}
	return strings.Join(lines, "\n")
}

func normalizePublicRouterOutput(output PublicRouterOutput, req PublicAnswerRequest) *PublicRouterOutput {
	output.Specialist = normalizePublicSpecialist(output.Specialist)
	output.Intent = strings.TrimSpace(output.Intent)
	output.RewrittenQuestion = strings.TrimSpace(output.RewrittenQuestion)
	if output.RewrittenQuestion == "" {
		output.RewrittenQuestion = strings.TrimSpace(req.Question)
	}
	output.HistorySummary = truncateForPrompt(strings.TrimSpace(output.HistorySummary), 500)
	output.Slots = normalizePublicRouterSlots(output.Slots)
	output.MissingInfo = normalizePublicRouterList(output.MissingInfo, 8)
	output.RiskFlags = normalizePublicRouterList(output.RiskFlags, 8)
	output.RetrievalQueries = normalizePublicRouterList(output.RetrievalQueries, 3)
	if len(output.RetrievalQueries) == 0 && strings.TrimSpace(output.RewrittenQuestion) != "" {
		output.RetrievalQueries = []string{output.RewrittenQuestion}
	}
	output.AnswerPolicy = truncateForPrompt(strings.TrimSpace(output.AnswerPolicy), 500)
	return &output
}

func normalizePublicSpecialist(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "reception", "product", "pricing", "purchase", "technical", "troubleshooting", "billing_after_sales", "safety":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "product"
	}
}

func normalizePublicRouterSlots(slots PublicRouterSlots) PublicRouterSlots {
	normalized := PublicRouterSlots{
		Product:           strings.TrimSpace(slots.Product),
		Products:          normalizePublicRouterList(slots.Products, 8),
		ProductResolution: normalizePublicProductResolution(slots.ProductResolution),
		StaticType:        strings.TrimSpace(slots.StaticType),
		IPType:            strings.TrimSpace(slots.IPType),
		Bandwidth:         strings.TrimSpace(slots.Bandwidth),
		Quantity:          strings.TrimSpace(slots.Quantity),
		Scenario:          strings.TrimSpace(slots.Scenario),
		Platform:          strings.TrimSpace(slots.Platform),
		Device:            strings.TrimSpace(slots.Device),
		ErrorCode:         strings.TrimSpace(slots.ErrorCode),
	}
	if len(normalized.Products) == 0 && normalized.Product != "" {
		normalized.Products = []string{normalized.Product}
	}
	if normalized.Product == "" && len(normalized.Products) == 1 {
		normalized.Product = normalized.Products[0]
	}
	if normalized.ProductResolution.Primary == "" {
		normalized.ProductResolution.Primary = normalized.Product
	}
	if len(normalized.ProductResolution.All) == 0 {
		normalized.ProductResolution.All = append([]string(nil), normalized.Products...)
	}
	return normalized
}

func normalizePublicProductResolution(resolution PublicProductResolution) PublicProductResolution {
	return PublicProductResolution{
		Primary:     strings.TrimSpace(resolution.Primary),
		All:         normalizePublicRouterList(resolution.All, 8),
		FromHistory: resolution.FromHistory,
		Confidence:  clampConfidence(resolution.Confidence),
		Ambiguous:   resolution.Ambiguous,
		Reason:      truncateForPrompt(strings.TrimSpace(resolution.Reason), 240),
	}
}

func normalizePublicRouterList(items []string, limit int) []string {
	if limit <= 0 {
		limit = len(items)
	}
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, truncateForPrompt(item, 240))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func publicRouterTraceSummary(output *PublicRouterOutput, raw string, promptChars int, err error) publicRouterTraceResult {
	result := publicRouterTraceResult{
		Output:      output,
		RawChars:    len([]rune(strings.TrimSpace(raw))),
		PromptChars: promptChars,
	}
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func publicRouterTraceMap(result publicRouterTraceResult) map[string]any {
	raw, err := json.Marshal(result)
	if err != nil {
		return map[string]any{"error": "marshal router trace result failed"}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{"error": "decode router trace result failed"}
	}
	return out
}
