package service

import "testing"

func routerWithSignals(signals CustomerRouterIntentSignals, product, quantity string) *CustomerRouterOutput {
	out := &CustomerRouterOutput{UserIntentSignals: signals}
	out.Slots.PrimaryProduct = product
	out.Slots.Quantity = quantity
	return out
}

func TestResolveCustomerUserIntentNilRouter(t *testing.T) {
	if got := resolveCustomerUserIntent(nil); got != nil {
		t.Fatalf("expected nil intent for nil router, got %+v", got)
	}
}

func TestResolveCustomerUserIntentNoSignals(t *testing.T) {
	got := resolveCustomerUserIntent(routerWithSignals(CustomerRouterIntentSignals{}, "static_ip", "10"))
	if got != nil {
		t.Fatalf("expected nil intent when no signals fire, got %+v", got)
	}
}

func TestResolveCustomerUserIntentRefund(t *testing.T) {
	got := resolveCustomerUserIntent(routerWithSignals(CustomerRouterIntentSignals{RefundStrong: true}, "static_ip", ""))
	if got == nil || got.Type != customerUserIntentRefund {
		t.Fatalf("expected refund intent, got %+v", got)
	}
	if got.Extra != nil {
		t.Fatalf("refund intent must not carry extra, got %+v", got.Extra)
	}
}

func TestResolveCustomerUserIntentDiscount(t *testing.T) {
	got := resolveCustomerUserIntent(routerWithSignals(CustomerRouterIntentSignals{DiscountStrong: true}, "datacenter_ip", "1000个"))
	if got == nil || got.Type != customerUserIntentDiscount {
		t.Fatalf("expected discount intent, got %+v", got)
	}
	if got.Extra == nil || got.Extra.ProductType != "datacenter_ip" || got.Extra.Quantity != 1000 {
		t.Fatalf("expected discount extra {datacenter_ip,1000}, got %+v", got.Extra)
	}
}

func TestResolveCustomerUserIntentDiscountRequiresProduct(t *testing.T) {
	for _, product := range []string{"", "unknown", "dynamic_ip"} {
		got := resolveCustomerUserIntent(routerWithSignals(CustomerRouterIntentSignals{DiscountStrong: true}, product, "1000"))
		if got != nil {
			t.Fatalf("expected no discount intent for product %q, got %+v", product, got)
		}
	}
}

func TestResolveCustomerUserIntentDiscountRequiresQuantity(t *testing.T) {
	got := resolveCustomerUserIntent(routerWithSignals(CustomerRouterIntentSignals{DiscountStrong: true}, "static_ip", "想要一些"))
	if got != nil {
		t.Fatalf("expected no discount intent without parseable quantity, got %+v", got)
	}
}

func TestResolveCustomerUserIntentSwitchIP(t *testing.T) {
	got := resolveCustomerUserIntent(routerWithSignals(CustomerRouterIntentSignals{SwitchIP: true}, "static_ip", ""))
	if got == nil || got.Type != customerUserIntentSwitchIP {
		t.Fatalf("expected switch_ip intent, got %+v", got)
	}
}

func TestResolveCustomerUserIntentSwitchIPRequiresKnownProduct(t *testing.T) {
	got := resolveCustomerUserIntent(routerWithSignals(CustomerRouterIntentSignals{SwitchIP: true}, "unknown", ""))
	if got != nil {
		t.Fatalf("expected no switch_ip intent for unknown product, got %+v", got)
	}
}

func TestResolveCustomerUserIntentSwitchIPExcludesDynamic(t *testing.T) {
	got := resolveCustomerUserIntent(routerWithSignals(CustomerRouterIntentSignals{SwitchIP: true}, "dynamic_ip", ""))
	if got != nil {
		t.Fatalf("expected no switch_ip intent for dynamic_ip, got %+v", got)
	}
}

func TestResolveCustomerUserIntentWecomNeedsBothSignals(t *testing.T) {
	if got := resolveCustomerUserIntent(routerWithSignals(CustomerRouterIntentSignals{WantsHuman: true}, "unknown", "")); got != nil {
		t.Fatalf("expected no wecom intent without wechat signal, got %+v", got)
	}
	if got := resolveCustomerUserIntent(routerWithSignals(CustomerRouterIntentSignals{WantsWechat: true}, "unknown", "")); got != nil {
		t.Fatalf("expected no wecom intent without human signal, got %+v", got)
	}
	got := resolveCustomerUserIntent(routerWithSignals(CustomerRouterIntentSignals{WantsHuman: true, WantsWechat: true}, "unknown", ""))
	if got == nil || got.Type != customerUserIntentWecom {
		t.Fatalf("expected wecom intent when both signals fire, got %+v", got)
	}
}

func TestResolveCustomerUserIntentPriority(t *testing.T) {
	// All signals fire; refund has the highest priority.
	all := CustomerRouterIntentSignals{
		WantsHuman:     true,
		WantsWechat:    true,
		RefundStrong:   true,
		SwitchIP:       true,
		DiscountStrong: true,
	}
	got := resolveCustomerUserIntent(routerWithSignals(all, "datacenter_ip", "1000"))
	if got == nil || got.Type != customerUserIntentRefund {
		t.Fatalf("expected refund to win on priority, got %+v", got)
	}

	// Without refund, discount wins over switch_ip and wecom.
	noRefund := all
	noRefund.RefundStrong = false
	got = resolveCustomerUserIntent(routerWithSignals(noRefund, "datacenter_ip", "1000"))
	if got == nil || got.Type != customerUserIntentDiscount {
		t.Fatalf("expected discount to win over switch_ip/wecom, got %+v", got)
	}

	// Without refund/discount-eligibility, switch_ip wins over wecom.
	noDiscount := noRefund
	got = resolveCustomerUserIntent(routerWithSignals(noDiscount, "static_ip", ""))
	if got == nil || got.Type != customerUserIntentSwitchIP {
		t.Fatalf("expected switch_ip to win over wecom, got %+v", got)
	}
}

func TestParseCustomerQuantity(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"1000", 1000, true},
		{"10个", 10, true},
		{" 100 个独享 ", 100, true},
		{"约 50 台", 50, true},
		{"", 0, false},
		{"想要一些", 0, false},
		{"0", 0, false},
	}
	for _, c := range cases {
		got, ok := parseCustomerQuantity(c.in)
		if ok != c.ok || got != c.want {
			t.Fatalf("parseCustomerQuantity(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
