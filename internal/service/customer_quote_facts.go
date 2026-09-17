package service

import (
	"encoding/json"
	"strings"
)

func BuildCustomerQuoteFacts(req CustomerChatRequest, routerOutput *CustomerRouterOutput, profile CustomerSpecialistProfile) CustomerQuoteFacts {
	if profile.Name != "pricing" && !customerRouterLooksPriceQuestion(req.Question) {
		return CustomerQuoteFacts{
			Status: customerQuoteStatusNotApplicable,
			Speak:  "本轮不是报价核算。",
		}
	}
	table, err := DefaultCustomerPricingTable()
	if err != nil {
		return CustomerQuoteFacts{
			Status: customerQuoteStatusUnsupported,
			Speak:  "价格表不可用，不要编数字。",
		}
	}
	slots := CustomerRouterSlots{}
	if routerOutput != nil {
		slots = routerOutput.Slots
	}
	last := lastQuotedOfferFromHistory(req.History)
	in := CustomerQuoteInput{
		Product:       customerQuoteProductFromSlots(slots),
		Bandwidth:     firstNonEmpty(normalizeCustomerBandwidth(slots.Bandwidth), normalizeCustomerBandwidth(req.Question)),
		Quantity:      ParseCustomerQuantity(strings.TrimSpace(slots.Quantity + " " + req.Question)),
		NamedPrice:    ParseCustomerNamedPrice(req.Question),
		LastQuoted:    last.UnitPrice,
		WantsDiscount: customerLooksDiscountRequest(req.Question),
	}
	if in.Quantity == 0 {
		in.Quantity = ParseCustomerQuantity(slots.Quantity)
	}
	if quoteLastOfferSpecChanged(table, in, last) {
		in.LastQuoted = 0
	}
	return ResolveCustomerQuote(table, in)
}

func quoteLastOfferSpecChanged(table CustomerPricingTable, in CustomerQuoteInput, last lastQuotedOffer) bool {
	if last.UnitPrice <= 0 {
		return false
	}
	if last.Bandwidth != "" && in.Bandwidth != "" && last.Bandwidth != in.Bandwidth {
		return true
	}
	lastQty := last.Quantity
	if lastQty <= 0 {
		lastQty = in.Quantity
	}
	lastBW := last.Bandwidth
	if lastBW == "" {
		lastBW = in.Bandwidth
	}
	if in.Product == "" || lastBW == "" || in.Bandwidth == "" || lastQty <= 0 || in.Quantity <= 0 {
		return false
	}
	lastBand, lastOK := table.lookupBand(in.Product, lastBW, lastQty)
	curBand, curOK := table.lookupBand(in.Product, in.Bandwidth, in.Quantity)
	if !lastOK || !curOK {
		return last.Bandwidth != in.Bandwidth || last.Quantity != in.Quantity
	}
	return lastBand != curBand
}

func customerQuoteProductFromSlots(slots CustomerRouterSlots) string {
	product := strings.TrimSpace(slots.PrimaryProduct)
	if product == "" || product == "unknown" {
		return ""
	}
	if product == "static_ip" && slots.IPType == "residential" {
		switch strings.TrimSpace(slots.StaticType) {
		case "dedicated", "独享", "住宅独享":
			return customerQuoteProductResidentialDedicated
		case "shared", "共享", "住宅共享":
			return customerQuoteProductResidentialShared
		default:
			return ""
		}
	}
	if product == "static_ip" {
		return customerQuoteProductStaticShared
	}
	return ""
}

func customerQuoteFactsResolved(facts CustomerQuoteFacts) bool {
	switch facts.Status {
	case customerQuoteStatusQuoted, customerQuoteStatusAtFloor, customerQuoteStatusBelowFloor:
		return true
	default:
		return false
	}
}

func formatCustomerQuoteFactsBlock(facts CustomerQuoteFacts) string {
	raw, err := json.Marshal(facts)
	if err != nil {
		return facts.Speak
	}
	return string(raw)
}

func applyQuoteFactsToEvidence(profile CustomerSpecialistProfile, facts CustomerQuoteFacts, evidence customerSpecialistEvidenceResult) customerSpecialistEvidenceResult {
	if profile.Name != "pricing" || !customerQuoteFactsResolved(facts) {
		return evidence
	}
	evidence.ContentBlocks = []string{"价格已由服务端核算，见 quote_facts。不要另编数字。"}
	return evidence
}
