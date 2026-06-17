package service

import (
	"strconv"
	"strings"
)

// Business intents surfaced to the API caller. These are deterministic
// conclusions derived from the router's raw intent signals plus slots, not
// raw model output.
const (
	customerUserIntentWecom    = "wecom"
	customerUserIntentRefund   = "refund"
	customerUserIntentSwitchIP = "switch_ip"
	customerUserIntentDiscount = "discount"
)

// CustomerUserIntent is the resolved business intent for a single turn. Type is
// one of the customerUserIntent* constants. Extra is only populated for the
// discount intent.
type CustomerUserIntent struct {
	Type  string                   `json:"type"`
	Extra *CustomerUserIntentExtra `json:"extra,omitempty"`
}

// CustomerUserIntentExtra carries the structured fields a discount intent needs
// to be actionable: the explicit product type and the requested quantity.
type CustomerUserIntentExtra struct {
	ProductType string `json:"product_type"`
	Quantity    int    `json:"quantity"`
}

// resolveCustomerUserIntent maps the router's raw intent signals and slots into
// a single business intent. It enforces the hard gating conditions and the
// priority order refund > discount > switch_ip > wecom, returning nil when no
// intent fires.
func resolveCustomerUserIntent(router *CustomerRouterOutput) *CustomerUserIntent {
	if router == nil {
		return nil
	}
	signals := router.UserIntentSignals
	product := strings.TrimSpace(router.Slots.PrimaryProduct)

	// refund: a strong, explicit refund desire is the highest-priority intent.
	if signals.RefundStrong {
		return &CustomerUserIntent{Type: customerUserIntentRefund}
	}

	// discount: strong discount desire AND an explicit product (known, not
	// dynamic) AND a parseable quantity.
	if signals.DiscountStrong && customerProductIsExplicitNonDynamic(product) {
		if qty, ok := parseCustomerQuantity(router.Slots.Quantity); ok {
			return &CustomerUserIntent{
				Type: customerUserIntentDiscount,
				Extra: &CustomerUserIntentExtra{
					ProductType: product,
					Quantity:    qty,
				},
			}
		}
	}

	// switch_ip: a switch desire with an explicit non-dynamic product. Unknown
	// product must stay in clarification, otherwise the service can guess the
	// wrong switch path.
	if signals.SwitchIP && customerProductIsExplicitNonDynamic(product) {
		return &CustomerUserIntent{Type: customerUserIntentSwitchIP}
	}

	// wecom: the customer wants a human AND explicitly asks for WeChat / WeCom.
	if signals.WantsHuman && signals.WantsWechat {
		return &CustomerUserIntent{Type: customerUserIntentWecom}
	}

	return nil
}

// customerProductIsExplicitNonDynamic reports whether the product slot names a
// concrete product that is not dynamic IP (required for the discount intent).
func customerProductIsExplicitNonDynamic(product string) bool {
	product = strings.TrimSpace(product)
	switch product {
	case "", "unknown", "dynamic_ip":
		return false
	default:
		return true
	}
}

// parseCustomerQuantity extracts the first positive integer from a free-form
// quantity slot such as "1000", "10个", or "100 个独享". It returns false when
// no positive integer is present.
func parseCustomerQuantity(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	var digits strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
			continue
		}
		if digits.Len() > 0 {
			break
		}
	}
	if digits.Len() == 0 {
		return 0, false
	}
	value, err := strconv.Atoi(digits.String())
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}
