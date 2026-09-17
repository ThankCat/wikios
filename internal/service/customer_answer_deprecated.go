package service

import "strings"

func compactCustomerVisibleText(text string) string {
	compact := strings.ToLower(strings.TrimSpace(text))
	return strings.NewReplacer(" ", "", "\n", "", "\r", "", "\t", "").Replace(compact)
}

func customerAnswerUsesDeprecatedPricing(answer string) bool {
	compact := compactCustomerVisibleText(answer)
	if compact == "" {
		return false
	}
	for _, marker := range []string{
		"5-20个9折",
		"21-50个8折",
		"51-100个7折",
		"101-200个6折",
		"201-300个5折",
		"300个以上4折",
		"25至70元/个/月",
		"300至800元/个/月",
		"25至70",
		"300至800",
		"25-70",
		"25到70",
		"25~70",
		"300-800",
		"300到800",
		"300~800",
		"17.5元/个",
		"52.5元/个",
		"22.5元/个/月",
		"独享型不参与数量折扣",
		"独享不参与数量折扣",
		"元/个/月",
		"元/个",
		"4折",
		"5折",
		"6折",
		"7折",
		"8折",
		"9折",
		"折后",
		"多买多优惠",
	} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}

func customerKnowledgePageHasDeprecatedPricing(content string) bool {
	return customerAnswerUsesDeprecatedPricing(content)
}
