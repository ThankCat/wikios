package service

import (
	"regexp"
	"strings"
)

type lastQuotedOffer struct {
	UnitPrice int
	Bandwidth string
	Quantity  int
}

var (
	customerQuotedYuanPerLineRE = regexp.MustCompile(`(\d+)\s*元\s*/\s*条\s*/\s*月`)
	customerQuotedYuanYiTiaoRE  = regexp.MustCompile(`(\d+)\s*元\s*一条`)
)

func SanitizeCustomerHistory(history []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, 0, len(history))
	for i, item := range history {
		role := strings.ToLower(strings.TrimSpace(item.Role))
		content := strings.TrimSpace(item.Content)
		if (role != "user" && role != "assistant") || content == "" {
			continue
		}
		if role == "assistant" && customerRouterLooksGreetingTurn(content) && !previousUserWasGreeting(history, i) {
			continue
		}
		out = append(out, ChatMessage{
			ID:        item.ID,
			Role:      role,
			Content:   content,
			CreatedAt: item.CreatedAt,
		})
	}
	return out
}

func LastQuotedUnitPrice(history []ChatMessage) int {
	return lastQuotedOfferFromHistory(history).UnitPrice
}

func lastQuotedOfferFromHistory(history []ChatMessage) lastQuotedOffer {
	history = SanitizeCustomerHistory(history)
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" {
			continue
		}
		price := parseQuotedUnitPrice(history[i].Content)
		if price <= 0 {
			continue
		}
		offer := lastQuotedOffer{
			UnitPrice: price,
			Bandwidth: normalizeCustomerBandwidth(history[i].Content),
			Quantity:  ParseCustomerQuantity(history[i].Content),
		}
		if prev := previousUserContent(history, i); prev != "" {
			if offer.Bandwidth == "" {
				offer.Bandwidth = normalizeCustomerBandwidth(prev)
			}
			if offer.Quantity == 0 {
				offer.Quantity = ParseCustomerQuantity(prev)
			}
		}
		return offer
	}
	return lastQuotedOffer{}
}

func parseQuotedUnitPrice(text string) int {
	for _, re := range []*regexp.Regexp{customerQuotedYuanPerLineRE, customerQuotedYuanYiTiaoRE} {
		matches := re.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 {
			continue
		}
		if n := atoiBounded(matches[len(matches)-1][1], 1, 999); n > 0 {
			return n
		}
	}
	return 0
}

func previousUserContent(history []ChatMessage, index int) string {
	for i := index - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(history[i].Role), "user") {
			return strings.TrimSpace(history[i].Content)
		}
	}
	return ""
}

func previousUserWasGreeting(history []ChatMessage, index int) bool {
	for i := index - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(history[i].Role), "user") {
			return customerRouterLooksGreetingTurn(history[i].Content)
		}
	}
	return false
}
