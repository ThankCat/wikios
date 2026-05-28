package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"wikios/internal/llm"
)

const customerRouterPromptFile = "customer_router_system.md"
const customerRouterContractVersion = "customer_router.v1"
const customerRouterLowConfidenceThreshold = 0.65

type CustomerRouterOutput struct {
	ContractVersion   string                  `json:"contract_version"`
	Specialist        string                  `json:"specialist"`
	RoutingConfidence float64                 `json:"routing_confidence"`
	RoutingReason     string                  `json:"routing_reason"`
	Intent            string                  `json:"intent"`
	RewrittenQuestion string                  `json:"rewritten_question"`
	HistorySummary    string                  `json:"history_summary"`
	Slots             CustomerRouterSlots     `json:"slots"`
	Ambiguity         CustomerRouterAmbiguity `json:"ambiguity"`
	MissingInfo       []string                `json:"missing_info"`
	RiskFlags         []string                `json:"risk_flags"`
	NeedsRetrieval    bool                    `json:"needs_retrieval"`
	RetrievalQueries  []string                `json:"retrieval_queries"`
	HandoffNotes      string                  `json:"handoff_notes"`
}

type CustomerRouterSlots struct {
	PrimaryProduct string   `json:"primary_product"`
	Products       []string `json:"products"`
	StaticType     string   `json:"static_type"`
	IPType         string   `json:"ip_type"`
	Bandwidth      string   `json:"bandwidth"`
	Quantity       string   `json:"quantity"`
	Scenario       string   `json:"scenario"`
	Platform       string   `json:"platform"`
	Device         string   `json:"device"`
	ErrorCode      string   `json:"error_code"`
}

type CustomerRouterAmbiguity struct {
	IsAmbiguous     bool     `json:"is_ambiguous"`
	AmbiguousFields []string `json:"ambiguous_fields"`
	Reason          string   `json:"reason"`
}

type customerRouterTraceResult struct {
	Output      *CustomerRouterOutput `json:"output,omitempty"`
	Error       string                `json:"error,omitempty"`
	RawChars    int                   `json:"raw_chars,omitempty"`
	PromptChars int                   `json:"prompt_chars,omitempty"`
}

func (s *CustomerChatService) routeCustomerQuestion(ctx context.Context, req CustomerChatRequest, receivedAt string, settings RuntimeCustomerQuerySettings) (*CustomerRouterOutput, string, LLMTrace, error) {
	systemPrompt, err := s.loadPrompt(customerRouterPromptFile)
	if err != nil {
		return nil, "", LLMTrace{}, err
	}
	userPrompt := customerRouterUserPrompt(req, receivedAt)
	text, trace, err := s.executeLLMTraceWithOptionsAndResponseFormat(ctx, nil, llmModelIDToken(settings.RouterModelID), []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, "llm customer router", nil, settings.RouterEnableThinking, customerRouterResponseFormat())
	if err != nil {
		return nil, text, trace, err
	}
	var output CustomerRouterOutput
	if err := llm.DecodeJSONObject(text, &output); err != nil {
		return nil, text, trace, fmt.Errorf("decode customer router output: %w", err)
	}
	if strings.TrimSpace(output.ContractVersion) != customerRouterContractVersion {
		return nil, text, trace, fmt.Errorf("invalid customer router contract_version: %q", strings.TrimSpace(output.ContractVersion))
	}
	normalized := normalizeCustomerRouterOutput(output, req)
	if err := validateCustomerRouterOutput(normalized); err != nil {
		return nil, text, trace, err
	}
	return normalized, text, trace, nil
}

func customerRouterUserPrompt(req CustomerChatRequest, receivedAt string) string {
	return strings.Join([]string{
		"current_time:",
		receivedAt,
		"",
		"current_customer_time:",
		formatCustomerBeijingTime(receivedAt),
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

func normalizeCustomerRouterOutput(output CustomerRouterOutput, req CustomerChatRequest) *CustomerRouterOutput {
	output.ContractVersion = customerRouterContractVersion
	output.Specialist = normalizeCustomerSpecialist(output.Specialist)
	output.RoutingConfidence = clampConfidence(output.RoutingConfidence)
	output.RoutingReason = truncateForPrompt(strings.TrimSpace(output.RoutingReason), 240)
	output.Intent = strings.TrimSpace(output.Intent)
	output.RewrittenQuestion = strings.TrimSpace(output.RewrittenQuestion)
	if output.RewrittenQuestion == "" {
		output.RewrittenQuestion = strings.TrimSpace(req.Question)
	}
	output.HistorySummary = truncateForPrompt(strings.TrimSpace(output.HistorySummary), 500)
	output.Slots = normalizeCustomerRouterSlots(output.Slots)
	output.Ambiguity = normalizeCustomerRouterAmbiguity(output.Ambiguity)
	output.MissingInfo = normalizeCustomerRouterEnumList(output.MissingInfo, 12, normalizeCustomerRouterMissingInfo)
	output.RiskFlags = normalizeCustomerRouterEnumList(output.RiskFlags, 12, normalizeCustomerRouterRiskFlag)
	if output.RoutingConfidence < customerRouterLowConfidenceThreshold {
		output.RiskFlags = appendUniqueString(output.RiskFlags, "low_confidence")
	}
	output.RetrievalQueries = normalizeCustomerRouterList(output.RetrievalQueries, 3)
	if !output.NeedsRetrieval {
		output.RetrievalQueries = nil
	}
	output.HandoffNotes = truncateForPrompt(strings.TrimSpace(output.HandoffNotes), 500)
	return &output
}

func normalizeCustomerSpecialist(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "reception", "product", "pricing", "purchase", "technical", "troubleshooting", "billing_after_sales", "safety":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "product"
	}
}

func normalizeCustomerRouterSlots(slots CustomerRouterSlots) CustomerRouterSlots {
	products := normalizeCustomerRouterEnumList(slots.Products, 8, normalizeCustomerRouterProduct)
	normalized := CustomerRouterSlots{
		PrimaryProduct: normalizeCustomerRouterProduct(slots.PrimaryProduct),
		Products:       products,
		StaticType:     normalizeCustomerRouterStaticType(slots.StaticType),
		IPType:         normalizeCustomerRouterIPType(slots.IPType),
		Bandwidth:      strings.TrimSpace(slots.Bandwidth),
		Quantity:       strings.TrimSpace(slots.Quantity),
		Scenario:       strings.TrimSpace(slots.Scenario),
		Platform:       strings.TrimSpace(slots.Platform),
		Device:         strings.TrimSpace(slots.Device),
		ErrorCode:      strings.TrimSpace(slots.ErrorCode),
	}
	if normalized.PrimaryProduct == "" {
		normalized.PrimaryProduct = "unknown"
	}
	if normalized.PrimaryProduct != "unknown" && len(normalized.Products) == 0 {
		normalized.Products = []string{normalized.PrimaryProduct}
	}
	return normalized
}

func normalizeCustomerRouterAmbiguity(ambiguity CustomerRouterAmbiguity) CustomerRouterAmbiguity {
	return CustomerRouterAmbiguity{
		IsAmbiguous:     ambiguity.IsAmbiguous,
		AmbiguousFields: normalizeCustomerRouterEnumList(ambiguity.AmbiguousFields, 8, normalizeCustomerRouterAmbiguousField),
		Reason:          truncateForPrompt(strings.TrimSpace(ambiguity.Reason), 240),
	}
}

func normalizeCustomerRouterList(items []string, limit int) []string {
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

func normalizeCustomerRouterEnumList(items []string, limit int, normalize func(string) string) []string {
	if limit <= 0 {
		limit = len(items)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = normalize(item)
		if item == "" {
			continue
		}
		out = appendUniqueString(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func appendUniqueString(items []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return items
	}
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func normalizeCustomerRouterProduct(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "static_ip", "dynamic_ip", "overseas_ip", "residential_ip", "datacenter_ip", "unlimited_ip", "mobile_proxy", "unknown":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "unknown"
	}
}

func normalizeCustomerRouterStaticType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "shared", "dedicated", "unknown":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "unknown"
	}
}

func normalizeCustomerRouterIPType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "datacenter", "residential", "overseas", "mobile", "unknown":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "unknown"
	}
}

func normalizeCustomerRouterMissingInfo(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "primary_product", "static_type", "ip_type", "bandwidth", "quantity", "scenario", "platform", "device", "error_code", "authentication_method", "account", "order_id":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeCustomerRouterRiskFlag(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pricing", "discount", "refund", "billing", "platform_risk", "overseas_access", "compliance", "internal", "illegal", "technical", "troubleshooting", "after_sales", "low_confidence":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeCustomerRouterAmbiguousField(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "primary_product", "products", "scenario", "platform", "device", "intent", "target_object":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func validateCustomerRouterOutput(output *CustomerRouterOutput) error {
	if output == nil {
		return fmt.Errorf("missing customer router output")
	}
	if output.ContractVersion != customerRouterContractVersion {
		return fmt.Errorf("invalid customer router contract_version: %q", output.ContractVersion)
	}
	if strings.TrimSpace(output.Specialist) == "" {
		return fmt.Errorf("missing customer router specialist")
	}
	if strings.TrimSpace(output.Slots.PrimaryProduct) == "" {
		return fmt.Errorf("missing customer router primary_product")
	}
	if output.NeedsRetrieval && len(output.RetrievalQueries) == 0 {
		return fmt.Errorf("customer router needs_retrieval=true but retrieval_queries is empty")
	}
	if !output.NeedsRetrieval && len(output.RetrievalQueries) != 0 {
		return fmt.Errorf("customer router needs_retrieval=false but retrieval_queries is not empty")
	}
	return nil
}

func customerRouterTraceSummary(output *CustomerRouterOutput, raw string, promptChars int, err error) customerRouterTraceResult {
	result := customerRouterTraceResult{
		Output:      output,
		RawChars:    len([]rune(strings.TrimSpace(raw))),
		PromptChars: promptChars,
	}
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func customerRouterTraceMap(result customerRouterTraceResult) map[string]any {
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
