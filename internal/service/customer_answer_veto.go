package service

import (
	"regexp"
	"strings"
)

const (
	customerSanitizeDeprecatedPricing = "deprecated_pricing"
	customerSanitizeInternalContext   = "internal_context_removed"
	customerSanitizeBelowQuoteFacts   = "below_quote_facts"
)

var customerAnswerYuanRE = regexp.MustCompile(`(\d+)\s*元`)

func sanitizeCustomerVisibleAnswer(answer string, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) (string, bool) {
	sanitized, changed, _ := sanitizeCustomerVisibleAnswerWithReason(answer, parsed, routerOutput)
	return sanitized, changed
}

func sanitizeCustomerVisibleAnswerWithReason(answer string, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) (string, bool, string) {
	return VetoCustomerVisibleAnswerWithParsed(answer, parsed, routerOutput, CustomerQuoteFacts{})
}

func VetoCustomerVisibleAnswer(answer string, facts CustomerQuoteFacts) (string, bool, string) {
	return VetoCustomerVisibleAnswerWithParsed(answer, customerChatLLMOutput{}, nil, facts)
}

func VetoCustomerVisibleAnswerWithParsed(answer string, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput, facts CustomerQuoteFacts) (string, bool, string) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false, ""
	}
	if customerAnswerIsAllowedInternalBoundaryRefusal(answer, parsed, routerOutput) {
		return answer, false, ""
	}
	parts := splitCustomerAnswerSentences(answer)
	if len(parts) == 0 {
		parts = []string{answer}
	}
	kept := make([]string, 0, len(parts))
	reason := ""
	for _, part := range parts {
		if customerVisibleAnswerLeaksInternalContext(part) {
			reason = customerSanitizeInternalContext
			continue
		}
		if customerAnswerUsesDeprecatedPricing(part) {
			reason = customerSanitizeDeprecatedPricing
			continue
		}
		if customerAnswerQuotesBelowFacts(part, facts) {
			reason = customerSanitizeBelowQuoteFacts
			continue
		}
		kept = append(kept, part)
	}
	if reason == "" {
		return answer, false, ""
	}
	return strings.TrimSpace(strings.Join(kept, "")), true, reason
}

func customerAnswerQuotesBelowFacts(part string, facts CustomerQuoteFacts) bool {
	limit := facts.UnitPrice
	if limit <= 0 {
		limit = facts.FloorPrice
	}
	if limit <= 0 {
		return false
	}
	for _, match := range customerAnswerYuanRE.FindAllStringSubmatch(part, -1) {
		n := atoiBounded(match[1], 1, 999)
		if n > 0 && n < limit {
			return true
		}
	}
	return false
}

func splitCustomerAnswerSentences(answer string) []string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return nil
	}
	parts := []string{}
	start := 0
	runes := []rune(answer)
	for i, r := range runes {
		switch r {
		case '。', '！', '？', '!', '?', '\n':
			segment := strings.TrimSpace(string(runes[start : i+1]))
			if segment != "" {
				parts = append(parts, segment)
			}
			start = i + 1
		}
	}
	if start < len(runes) {
		segment := strings.TrimSpace(string(runes[start:]))
		if segment != "" {
			parts = append(parts, segment)
		}
	}
	return parts
}
